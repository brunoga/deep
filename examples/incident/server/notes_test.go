package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/brunoga/deep/v6/crdt"
	deepws "github.com/brunoga/deep/ws"
)

type presence struct {
	Name string `json:"name"`
	Line int    `json:"line"`
}

func newNotesServer(t *testing.T, idle time.Duration) (*httptest.Server, *Notes, string) {
	t.Helper()
	dir := t.TempDir()
	notes, err := NewNotes(dir, idle,
		func(r *http.Request) bool { return r.URL.Query().Get("token") == "sekrit" },
		func(id string) bool { return id == "inc-1" },
	)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(notes)
	t.Cleanup(srv.Close)
	return srv, notes, dir
}

func wsURL(srv *httptest.Server, room, token string) string {
	return "ws" + strings.TrimPrefix(srv.URL, "http") + "/?room=" + room + "&token=" + token
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

// Two responders type into the same incident's notes and converge; each sees
// the other in the presence view.
func TestNotesConvergenceAndPresence(t *testing.T) {
	srv, _, _ := newNotesServer(t, time.Hour)
	ctx := context.Background()

	ana, err := deepws.Dial[presence](ctx, wsURL(srv, "inc-1", "sekrit"), "ana")
	if err != nil {
		t.Fatal(err)
	}
	defer ana.Close(ctx)
	bruno, err := deepws.Dial[presence](ctx, wsURL(srv, "inc-1", "sekrit"), "bruno")
	if err != nil {
		t.Fatal(err)
	}
	defer bruno.Close(ctx)

	ana.Edit(func(d *crdt.Document) { d.Insert(0, "10:02 db failover started\n") })
	if err := ana.Publish(ctx); err != nil {
		t.Fatal(err)
	}
	bruno.Edit(func(d *crdt.Document) { d.Insert(0, "10:01 paged oncall\n") })
	if err := bruno.Publish(ctx); err != nil {
		t.Fatal(err)
	}

	waitFor(t, "convergence", func() bool {
		a, b := ana.Text(), bruno.Text()
		return a == b && strings.Contains(a, "10:01") && strings.Contains(a, "10:02")
	})

	if err := ana.Announce(ctx, presence{Name: "Ana", Line: 3}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "presence", func() bool {
		states := bruno.Awareness().States()
		p, ok := states["ana"]
		return ok && p.Name == "Ana" && p.Line == 3
	})
}

// The room outlives its occupants: when the last client leaves, eviction
// persists the notes; the next first-joiner gets them seeded back — across
// what is effectively a server restart of the room.
func TestNotesEvictionPersistsAndReseeds(t *testing.T) {
	srv, notes, dir := newNotesServer(t, 50*time.Millisecond)
	ctx := context.Background()

	ana, err := deepws.Dial[presence](ctx, wsURL(srv, "inc-1", "sekrit"), "ana")
	if err != nil {
		t.Fatal(err)
	}
	ana.Edit(func(d *crdt.Document) { d.Insert(0, "surviving note") })
	if err := ana.Publish(ctx); err != nil {
		t.Fatal(err)
	}
	// Let the hub apply the update before leaving.
	waitFor(t, "hub state", func() bool { return notes.Text("inc-1") == "surviving note" })
	ana.Close(ctx)

	waitFor(t, "eviction file", func() bool {
		_, err := os.Stat(dir + "/inc-1.notes.bin")
		return err == nil
	})

	// Text reads from disk while the room is down.
	if got := notes.Text("inc-1"); got != "surviving note" {
		t.Fatalf("Text after eviction = %q", got)
	}

	// A fresh joiner finds the notes waiting.
	bruno, err := deepws.Dial[presence](ctx, wsURL(srv, "inc-1", "sekrit"), "bruno")
	if err != nil {
		t.Fatal(err)
	}
	defer bruno.Close(ctx)
	waitFor(t, "reseeded text", func() bool { return bruno.Text() == "surviving note" })
}

// Bad token or unknown incident: the join is refused before the upgrade.
func TestNotesAuth(t *testing.T) {
	srv, _, _ := newNotesServer(t, time.Hour)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if _, err := deepws.Dial[presence](ctx, wsURL(srv, "inc-1", "wrong"), "x"); err == nil {
		t.Fatal("bad token accepted")
	}
	if _, err := deepws.Dial[presence](ctx, wsURL(srv, "inc-666", "sekrit"), "x"); err == nil {
		t.Fatal("unknown incident accepted")
	}
}
