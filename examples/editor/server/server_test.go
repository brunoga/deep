package server

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brunoga/deep/v6/crdt"
	"github.com/brunoga/deep/v6/crdt/hlc"
	deepws "github.com/brunoga/deep/ws"
)

type cursor struct {
	Name      string `json:"name"`
	Selection struct {
		Anchor int `json:"anchor"`
		Head   int `json:"head"`
	} `json:"selection"`
}

func start(t *testing.T, idle time.Duration) (*Server, string, string) {
	t.Helper()
	dir := t.TempDir()
	srv, err := New(dir, idle)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/ws", srv.Hub())
	mux.Handle("/documents", srv.API())
	mux.Handle("/documents/", srv.API())
	http := httptest.NewServer(mux)
	t.Cleanup(http.Close)
	return srv, http.URL, dir
}

func wsURL(base, doc string) string {
	return "ws" + strings.TrimPrefix(base, "http") + "/ws?room=" + doc
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

// Two people editing one document converge, and each sees where the other is.
// The server does not arbitrate any of it — it relays.
func TestTwoEditorsConverge(t *testing.T) {
	_, base, _ := start(t, time.Hour)
	ctx := context.Background()

	ana, err := deepws.Dial[cursor](ctx, wsURL(base, "notes"), "ana")
	if err != nil {
		t.Fatal(err)
	}
	defer ana.Close(ctx)
	bo, err := deepws.Dial[cursor](ctx, wsURL(base, "notes"), "bo")
	if err != nil {
		t.Fatal(err)
	}
	defer bo.Close(ctx)

	ana.Edit(func(d *crdt.Document) { d.Insert(0, "first line\n") })
	if err := ana.Publish(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "bo to receive it", func() bool { return strings.Contains(bo.Text(), "first line") })

	// Bo types above Ana's text; both end up with the same document.
	bo.Edit(func(d *crdt.Document) { d.Insert(0, "title\n") })
	if err := bo.Publish(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the two to converge", func() bool {
		return ana.Text() == bo.Text() && strings.HasPrefix(ana.Text(), "title\n")
	})

	// And presence carries a selection, which is what draws a remote caret.
	var sel cursor
	sel.Name = "Ana"
	sel.Selection.Anchor, sel.Selection.Head = 6, 11
	if err := ana.Announce(ctx, sel); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "bo to see ana's selection", func() bool {
		state, ok := bo.Awareness().States()["ana"]
		return ok && state.Name == "Ana" && state.Selection.Head == 11
	})
}

// A document outlives the people editing it: when the last one leaves, the
// room is written out, and the next person to open it gets it back.
func TestDocumentSurvivesEveryoneLeaving(t *testing.T) {
	srv, base, dir := start(t, 50*time.Millisecond)
	ctx := context.Background()

	first, err := deepws.Dial[cursor](ctx, wsURL(base, "kept"), "ana")
	if err != nil {
		t.Fatal(err)
	}
	first.Edit(func(d *crdt.Document) { d.Insert(0, "worth keeping") })
	if err := first.Publish(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the room to hold it", func() bool {
		for _, doc := range srv.List() {
			if doc.Name == "kept" && doc.Chars == len("worth keeping") {
				return true
			}
		}
		return false
	})
	first.Close(ctx)

	waitFor(t, "the document to reach disk", func() bool {
		_, err := os.Stat(filepath.Join(dir, "kept.doc"))
		return err == nil
	})

	// A fresh joiner is seeded from the file during its handshake.
	second, err := deepws.Dial[cursor](ctx, wsURL(base, "kept"), "bo")
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close(ctx)
	if got := second.Text(); got != "worth keeping" {
		t.Fatalf("a rejoined document holds %q", got)
	}
}

// Names become room names and file names, so anything that is not one is
// refused before the upgrade.
func TestBadNamesAreRefused(t *testing.T) {
	_, base, _ := start(t, time.Hour)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	for _, name := range []string{"..", "../escape", "with space", ""} {
		if _, err := deepws.Dial[cursor](ctx, wsURL(base, name), "x"); err == nil {
			t.Errorf("joined a room named %q", name)
		}
	}
}

// The listing is what the client's sidebar shows: everything on disk, plus
// anything being edited right now.
func TestListing(t *testing.T) {
	srv, base, _ := start(t, time.Hour)
	ctx := context.Background()

	if err := srv.Create("plan"); err != nil {
		t.Fatal(err)
	}
	if err := srv.Create("plan"); err == nil {
		t.Error("created the same document twice")
	}
	if err := srv.Create("../escape"); err == nil {
		t.Error("created a document with a traversal name")
	}

	live, err := deepws.Dial[cursor](ctx, wsURL(base, "scratch"), "ana")
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close(ctx)
	live.Edit(func(d *crdt.Document) { d.Insert(0, "one\ntwo") })
	if err := live.Publish(ctx); err != nil {
		t.Fatal(err)
	}

	waitFor(t, "the listing to show both", func() bool {
		byName := map[string]Document{}
		for _, doc := range srv.List() {
			byName[doc.Name] = doc
		}
		scratch, ok := byName["scratch"]
		return ok && scratch.Live && scratch.Lines == 2 && len(byName) == 2
	})

	// And over HTTP, which is how the client actually asks.
	res, err := http.Get(base + "/documents")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var docs []Document
	if err := json.NewDecoder(res.Body).Decode(&docs); err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 {
		t.Fatalf("listing over HTTP: %+v", docs)
	}
}

// A file that cannot be read is set aside rather than left to refuse every
// join to that document forever.
func TestCorruptDocumentIsQuarantined(t *testing.T) {
	_, base, dir := start(t, time.Hour)
	if err := os.WriteFile(filepath.Join(dir, "broken.doc"), []byte("not a document"), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	client, err := deepws.Dial[cursor](ctx, wsURL(base, "broken"), "ana")
	if err != nil {
		t.Fatalf("a corrupt file locked the document: %v", err)
	}
	defer client.Close(ctx)

	aside, _ := filepath.Glob(filepath.Join(dir, "broken.doc.corrupt-*"))
	if len(aside) != 1 {
		t.Fatalf("the corrupt file was not set aside: %v", aside)
	}
}

// Shutdown writes out what is open, so a document typed into a moment ago
// does not depend on the eviction timer to survive.
func TestPersistWritesLiveDocuments(t *testing.T) {
	srv, base, dir := start(t, time.Hour)
	ctx := context.Background()

	client, err := deepws.Dial[cursor](ctx, wsURL(base, "open"), "ana")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close(ctx)
	client.Edit(func(d *crdt.Document) { d.Insert(0, "unsaved") })
	if err := client.Publish(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the room to hold it", func() bool {
		for _, doc := range srv.List() {
			if doc.Name == "open" && doc.Chars == len("unsaved") {
				return true
			}
		}
		return false
	})

	srv.Persist()
	if _, err := os.Stat(filepath.Join(dir, "open.doc")); err != nil {
		t.Fatalf("Persist did not write the open document: %v", err)
	}
}

// The websocket handler brings a document into being by being asked for it,
// which is fine for a client and not fine for a crawler: anything that is not
// trying to become a websocket is turned away before a room exists.
func TestPlainRequestsDoNotCreateDocuments(t *testing.T) {
	srv, base, dir := start(t, time.Hour)

	res, err := http.Get(base + "/ws?room=junk")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode == http.StatusSwitchingProtocols {
		t.Fatalf("a plain GET was upgraded")
	}

	for _, doc := range srv.List() {
		if doc.Name == "junk" {
			t.Errorf("a plain GET created a document: %+v", doc)
		}
	}
	srv.Persist()
	if _, err := os.Stat(filepath.Join(dir, "junk.doc")); err == nil {
		t.Error("a plain GET left a file behind")
	}
}

// Listing the documents reads every open room, which postpones its eviction.
// It must not postpone it forever: the client lists on every page load, and a
// document nobody is in has to reach disk anyway.
func TestListingDoesNotKeepRoomsAlive(t *testing.T) {
	srv, base, dir := start(t, 60*time.Millisecond)
	ctx := context.Background()

	client, err := deepws.Dial[cursor](ctx, wsURL(base, "watched"), "ana")
	if err != nil {
		t.Fatal(err)
	}
	client.Edit(func(d *crdt.Document) { d.Insert(0, "written despite the watching") })
	if err := client.Publish(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the room to hold it", func() bool {
		for _, doc := range srv.List() {
			if doc.Name == "watched" && doc.Live {
				return true
			}
		}
		return false
	})
	client.Close(ctx)

	// Keep listing right through the idle window, the way a browser sitting
	// on the document list would.
	done := time.After(200 * time.Millisecond)
	for listing := true; listing; {
		select {
		case <-done:
			listing = false
		default:
			srv.List()
			time.Sleep(10 * time.Millisecond)
		}
	}

	waitFor(t, "the document to reach disk anyway", func() bool {
		_, err := os.Stat(filepath.Join(dir, "watched.doc"))
		return err == nil
	})
	waitFor(t, "the room to be let go", func() bool { return srv.liveDoc("watched") == nil })
}

// An eviction and a rejoin can overlap: the room is dropped, and before the
// eviction finishes writing it out somebody opens the document again. The
// eviction is finishing with the old room, and must not report the new one
// dead — a document marked dead is one that shutdown does not write.
func TestEvictionDoesNotUnmarkTheRoomThatReplacedIt(t *testing.T) {
	srv, _, _ := start(t, time.Hour)

	replacement := crdt.NewDocument(hlc.NewClock("rejoin"))
	srv.mu.Lock()
	srv.live["raced"] = replacement
	srv.mu.Unlock()

	// The eviction hook, arriving late with the document it evicted.
	srv.persist("raced", crdt.NewDocument(hlc.NewClock("evicted")))

	if got := srv.liveDoc("raced"); got != replacement {
		t.Fatalf("the room that replaced the evicted one is %v, want it still live", got)
	}
}

// The sidebar has to hear about a document somebody else created without
// being reloaded, and about one becoming live or going quiet.
func TestListingEventsReachOtherClients(t *testing.T) {
	srv, base, _ := start(t, time.Hour)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/documents/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if got := res.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("content type %q", got)
	}

	events := make(chan string, 8)
	go func() {
		scanner := bufio.NewScanner(res.Body)
		for scanner.Scan() {
			if line := scanner.Text(); strings.HasPrefix(line, "data: ") {
				events <- strings.TrimPrefix(line, "data: ")
			}
		}
		close(events)
	}()

	// Somebody else creates a document.
	if err := srv.Create("from-elsewhere"); err != nil {
		t.Fatal(err)
	}
	select {
	case ev, ok := <-events:
		if !ok || ev != "changed" {
			t.Fatalf("stream gave %q (open: %t) when a document was created", ev, ok)
		}
	case <-ctx.Done():
		t.Fatal("no event when a document was created")
	}

	// And somebody opening one is a change to the listing too: it goes live.
	client, err := deepws.Dial[cursor](ctx, wsURL(base, "from-elsewhere"), "ana")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close(ctx)
	select {
	case ev, ok := <-events:
		if !ok || ev != "changed" {
			t.Fatalf("stream gave %q (open: %t) when a document went live", ev, ok)
		}
	case <-ctx.Done():
		t.Fatal("no event when a document went live")
	}
}
