// The end-to-end story: one incident, from page to close, through every layer
// at once — racing responders, conditional patches, the audit log, offline
// notes, and a protobuf consumer, all against one real server.
package incident_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	deepproto "github.com/brunoga/deep/proto"
	deep "github.com/brunoga/deep/v6"
	"github.com/brunoga/deep/v6/condition"
	"github.com/brunoga/deep/v6/crdt"
	deepws "github.com/brunoga/deep/ws"
	"google.golang.org/protobuf/proto"

	"github.com/brunoga/deep/examples/incident/client"
	"github.com/brunoga/deep/examples/incident/model"
	"github.com/brunoga/deep/examples/incident/pb"
	"github.com/brunoga/deep/examples/incident/server"
)

type presence struct {
	Name string `json:"name"`
	Pos  int    `json:"pos"`
}

func startCommandpost(t *testing.T) string {
	t.Helper()
	store, err := server.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	api := server.NewAPI(store, server.WithToken("sekrit"))
	notes, err := server.NewNotes(t.TempDir(), time.Hour, api.Authorized,
		func(id string) bool { _, err := store.Get(id); return err == nil })
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/ws", notes)
	mux.Handle("/", api)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestIncidentLifecycleEndToEnd(t *testing.T) {
	url := startCommandpost(t)
	ana := client.New(url, "sekrit", "ana")
	bruno := client.New(url, "sekrit", "bruno")
	ctx := context.Background()

	// ── The page goes out. ──────────────────────────────────────────────
	if _, err := ana.Create(model.Incident{
		ID: "inc-1", Title: "checkout errors", Severity: model.Sev2, Status: model.StatusOpen,
	}); err != nil {
		t.Fatal(err)
	}
	for _, task := range []model.Task{
		{ID: "t1", Text: "page db oncall"},
		{ID: "t2", Text: "check error rates"},
	} {
		if _, err := ana.Patch("inc-1", client.AddTask(task)); err != nil {
			t.Fatal(err)
		}
	}

	// ── Eight responders race to claim t1; the If condition arbitrates. ─
	var wins int
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func(who string) {
			defer wg.Done()
			c := client.New(url, "sekrit", who)
			res, err := c.Patch("inc-1", client.ClaimTask("t1", who))
			if err != nil {
				t.Errorf("racer %s: %v", who, err)
				return
			}
			if res.Applied == 1 {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}(string(rune('a' + i)))
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("claim race: %d winners, want exactly 1", wins)
	}

	// ── Notes: both type at once and converge. ──────────────────────────
	anaWS, err := deepws.Dial[presence](ctx, ana.WSURL("inc-1"), "ana")
	if err != nil {
		t.Fatal(err)
	}
	brunoWS, err := deepws.Dial[presence](ctx, bruno.WSURL("inc-1"), "bruno")
	if err != nil {
		t.Fatal(err)
	}
	anaWS.Edit(func(d *crdt.Document) { d.Insert(0, "10:02 failover started\n") })
	brunoWS.Edit(func(d *crdt.Document) { d.Insert(0, "10:01 paged oncall\n") })
	if err := anaWS.Publish(ctx); err != nil {
		t.Fatal(err)
	}
	if err := brunoWS.Publish(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "notes convergence", func() bool {
		a, b := anaWS.Text(), brunoWS.Text()
		return a == b && strings.Contains(a, "10:01") && strings.Contains(a, "10:02")
	})

	// ── Bruno drops, types offline, resumes; nothing is lost. ───────────
	brunoWS.Close(ctx)
	doc := brunoWS.Detach()
	if doc == nil {
		t.Fatal("Detach after Close returned nil")
	}
	doc.Insert(doc.Len(), "10:05 (offline) db recovered\n")

	brunoWS2, err := deepws.Dial[presence](ctx, bruno.WSURL("inc-1"), "bruno", deepws.WithDocument(doc))
	if err != nil {
		t.Fatal(err)
	}
	defer brunoWS2.Close(ctx)
	waitFor(t, "offline edit to reach ana", func() bool {
		return strings.Contains(anaWS.Text(), "offline") && anaWS.Text() == brunoWS2.Text()
	})
	defer anaWS.Close(ctx)

	// ── A protobuf consumer annotates through the wire envelope. ────────
	envelopePatch := deep.Patch[*pb.Incident]{Operations: []deep.Operation{{
		Kind:   deep.OpAdd,
		Path:   "/tasks/statusbot",
		New:    &pb.Task{Id: "statusbot", Text: "tracking", Done: true},
		Unless: &condition.Condition{Op: "exists", Path: "/tasks/statusbot"},
	}}}
	envelope, err := deepproto.ToProto(envelopePatch)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := proto.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("POST", url+"/incidents/inc-1/patch.pb", bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer sekrit")
	req.Header.Set("X-Author", "statusbot")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("envelope patch: %d", resp.StatusCode)
	}

	// ── Wind down: escalate, complete, resolve, close. ──────────────────
	if _, err := ana.Patch("inc-1", client.Escalate(model.Sev1)); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"t1", "t2"} {
		if _, err := ana.Patch("inc-1", client.CompleteTask(id)); err != nil {
			t.Fatal(err)
		}
	}
	// Closing before resolving trips the guard.
	if _, err := ana.Patch("inc-1", client.Close()); err == nil {
		t.Fatal("close before resolve was accepted")
	}
	if _, err := ana.Patch("inc-1", client.SetStatus(model.StatusResolved)); err != nil {
		t.Fatal(err)
	}
	closeRes, err := ana.Patch("inc-1", client.Close())
	if err != nil {
		t.Fatal(err)
	}

	// ── The audit log tells the whole story, and stays reversible. ──────
	history, err := ana.History("inc-1")
	if err != nil {
		t.Fatal(err)
	}
	authors := map[string]bool{}
	for _, entry := range history {
		authors[entry.Author] = true
	}
	if !authors["ana"] || !authors["statusbot"] {
		t.Fatalf("audit authors: %v", authors)
	}

	// Reopen by undoing the close; the closed state is one Reverse away.
	if _, err := ana.Undo("inc-1", closeRes.Seq); err != nil {
		t.Fatal(err)
	}
	final, err := ana.Get("inc-1")
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != model.StatusResolved || final.Severity != model.Sev1 {
		t.Fatalf("final state: %+v", final)
	}
	if len(final.Tasks) != 3 {
		t.Fatalf("tasks: %+v", final.Tasks)
	}
}
