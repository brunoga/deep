package deepws_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brunoga/deep/v6/crdt"
	deepws "github.com/brunoga/deep/ws"
	"github.com/coder/websocket"
)

type cursor struct {
	Name  string `json:"name"`
	Index int    `json:"index"`
}

func startHub(t *testing.T) (*deepws.Hub, string) {
	t.Helper()
	hub := deepws.NewHub()
	srv := httptest.NewServer(hub)
	t.Cleanup(srv.Close)
	return hub, "ws" + strings.TrimPrefix(srv.URL, "http") + "/?room=doc-1"
}

// waitFor polls until check passes or the deadline hits, so tests wait for
// convergence without racing it.
func waitFor(t *testing.T, what string, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestTwoClientsConverge(t *testing.T) {
	_, url := startHub(t)
	ctx := context.Background()

	alice, err := deepws.Dial[cursor](ctx, url, "alice")
	if err != nil {
		t.Fatal(err)
	}
	defer alice.Close(ctx)
	bob, err := deepws.Dial[cursor](ctx, url, "bob")
	if err != nil {
		t.Fatal(err)
	}
	defer bob.Close(ctx)

	alice.Edit(func(d *crdt.Document) { d.Insert(0, "hello ") })
	if err := alice.Publish(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "bob to see alice's text", func() bool {
		return bob.Text() == "hello "
	})

	bob.Edit(func(d *crdt.Document) { d.Insert(d.Len(), "world") })
	if err := bob.Publish(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "alice to see bob's text", func() bool {
		return alice.Text() == "hello world"
	})

	if a, b := alice.Text(), bob.Text(); a != b {
		t.Fatalf("documents diverged: %q vs %q", a, b)
	}
}

func TestUnpublishedEditsSurviveIncomingUpdates(t *testing.T) {
	// The interleaving that loses data if the client marks everything as
	// published when a remote update lands: alice edits locally, bob's update
	// arrives before she publishes, then she publishes.
	_, url := startHub(t)
	ctx := context.Background()

	alice, _ := deepws.Dial[cursor](ctx, url, "alice")
	defer alice.Close(ctx)
	bob, _ := deepws.Dial[cursor](ctx, url, "bob")
	defer bob.Close(ctx)

	alice.Edit(func(d *crdt.Document) { d.Insert(0, "AAA") })
	// Not published yet.

	bob.Edit(func(d *crdt.Document) { d.Insert(0, "BBB") })
	if err := bob.Publish(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "alice to receive bob's edit", func() bool {
		return strings.Contains(alice.Text(), "BBB")
	})

	// Now alice publishes; her AAA must reach bob.
	if err := alice.Publish(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "bob to receive alice's pre-existing edit", func() bool {
		return strings.Contains(bob.Text(), "AAA")
	})
	waitFor(t, "convergence", func() bool {
		return alice.Text() == bob.Text()
	})
}

func TestLateJoinerCatchesUp(t *testing.T) {
	_, url := startHub(t)
	ctx := context.Background()

	alice, _ := deepws.Dial[cursor](ctx, url, "alice")
	alice.Edit(func(d *crdt.Document) { d.Insert(0, "early history") })
	if err := alice.Publish(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "hub to hold the text", func() bool { return true })

	// The joiner receives everything during Dial, before it returns.
	carol, err := deepws.Dial[cursor](ctx, url, "carol")
	if err != nil {
		t.Fatal(err)
	}
	defer carol.Close(ctx)
	if got := carol.Text(); got != "early history" {
		t.Fatalf("late joiner sees %q", got)
	}
	alice.Close(ctx)
}

func TestOfflineEditsUploadOnReconnect(t *testing.T) {
	hub, url := startHub(t)
	ctx := context.Background()

	alice, _ := deepws.Dial[cursor](ctx, url, "alice")
	alice.Edit(func(d *crdt.Document) { d.Insert(0, "kept ") })
	if err := alice.Publish(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "hub to have the first edit", func() bool {
		var s string
		hub.Room("doc-1", func(d *crdt.Document) { s = d.String() })
		return s == "kept "
	})
	alice.Close(ctx)

	// "alice" edits offline: a fresh client with the same node id whose local
	// doc has diverged. Dial must upload the offline edit during handshake.
	offline, _ := deepws.Dial[cursor](ctx, url, "alice2")
	offline.Edit(func(d *crdt.Document) { d.Insert(d.Len(), "offline") })
	if err := offline.Publish(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "hub to hold both edits", func() bool {
		var s string
		hub.Room("doc-1", func(d *crdt.Document) { s = d.String() })
		return strings.Contains(s, "kept ") && strings.Contains(s, "offline")
	})
	offline.Close(ctx)
}

func TestPresencePropagatesAndLeaves(t *testing.T) {
	_, url := startHub(t)
	ctx := context.Background()

	alice, _ := deepws.Dial[cursor](ctx, url, "alice")
	defer alice.Close(ctx)
	bob, _ := deepws.Dial[cursor](ctx, url, "bob")

	if err := bob.Announce(ctx, cursor{Name: "Bob", Index: 3}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "alice to see bob", func() bool {
		s, ok := alice.Awareness().States()["bob"]
		return ok && s.Index == 3
	})

	// A joiner sees cached presence without waiting for a heartbeat.
	carol, _ := deepws.Dial[cursor](ctx, url, "carol")
	defer carol.Close(ctx)
	waitFor(t, "carol to see bob's cached presence", func() bool {
		_, ok := carol.Awareness().States()["bob"]
		return ok
	})

	// A clean close says goodbye, so peers drop bob at once.
	bob.Close(ctx)
	waitFor(t, "alice to see bob leave", func() bool {
		_, ok := alice.Awareness().States()["bob"]
		return !ok
	})
}

func TestConcurrentEditingConverges(t *testing.T) {
	_, url := startHub(t)
	ctx := context.Background()

	const writers = 4
	clients := make([]*deepws.Client[cursor], writers)
	for i := range clients {
		c, err := deepws.Dial[cursor](ctx, url, string(rune('a'+i)))
		if err != nil {
			t.Fatal(err)
		}
		clients[i] = c
		defer c.Close(ctx)
	}

	var wg sync.WaitGroup
	for i, c := range clients {
		wg.Add(1)
		go func(i int, c *deepws.Client[cursor]) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				c.Edit(func(d *crdt.Document) { d.Insert(0, string(rune('A'+i))) })
				if err := c.Publish(ctx); err != nil {
					t.Error(err)
					return
				}
			}
		}(i, c)
	}
	wg.Wait()

	waitFor(t, "all clients to converge on 80 characters", func() bool {
		for _, c := range clients {
			if c.Len() != writers*20 {
				return false
			}
		}
		first := clients[0].Text()
		for _, c := range clients[1:] {
			if c.Text() != first {
				return false
			}
		}
		return true
	})
}

func TestAuthRejectsAndAdmits(t *testing.T) {
	hub := deepws.NewHub(deepws.WithAuth(func(r *http.Request, room string) error {
		if r.Header.Get("X-Token") != "sesame" {
			return fmt.Errorf("no")
		}
		return nil
	}))
	srv := httptest.NewServer(hub)
	defer srv.Close()
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/?room=locked"
	ctx := context.Background()

	if _, err := deepws.Dial[cursor](ctx, url, "intruder"); err == nil {
		t.Fatal("dial without the token should fail")
	}

	c, err := deepws.Dial[cursor](ctx, url, "bearer", deepws.WithDialOptions(&websocket.DialOptions{
		HTTPHeader: http.Header{"X-Token": []string{"sesame"}},
	}))
	if err != nil {
		t.Fatalf("dial with the token: %v", err)
	}
	c.Close(ctx)
}

func TestRoomEvictionPersistsAndFrees(t *testing.T) {
	var mu sync.Mutex
	evicted := map[string]string{}
	hub := deepws.NewHub(deepws.WithRoomEviction(50*time.Millisecond, func(name string, d *crdt.Document) {
		mu.Lock()
		defer mu.Unlock()
		evicted[name] = d.String()
	}))
	srv := httptest.NewServer(hub)
	defer srv.Close()
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/?room=temp"
	ctx := context.Background()

	c, err := deepws.Dial[cursor](ctx, url, "a")
	if err != nil {
		t.Fatal(err)
	}
	c.Edit(func(d *crdt.Document) { d.Insert(0, "save me") })
	if err := c.Publish(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "hub to hold the text", func() bool {
		var s string
		hub.Room("temp", func(d *crdt.Document) { s = d.String() })
		return s == "save me"
	})
	c.Close(ctx)

	waitFor(t, "the room to be evicted with its state", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return evicted["temp"] == "save me"
	})

	// The next joiner gets a fresh room — which the host may seed from what
	// it persisted, the way Room is documented for.
	hub.Room("temp", func(d *crdt.Document) {
		if got := d.String(); got != "" {
			t.Errorf("room was not fresh after eviction: %q", got)
		}
	})
}

func TestRejoinCancelsEviction(t *testing.T) {
	var mu sync.Mutex
	evictions := 0
	hub := deepws.NewHub(deepws.WithRoomEviction(80*time.Millisecond, func(string, *crdt.Document) {
		mu.Lock()
		defer mu.Unlock()
		evictions++
	}))
	srv := httptest.NewServer(hub)
	defer srv.Close()
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/?room=busy"
	ctx := context.Background()

	a, _ := deepws.Dial[cursor](ctx, url, "a")
	a.Close(ctx) // room empties; timer armed

	b, _ := deepws.Dial[cursor](ctx, url, "b") // rejoin before it fires
	time.Sleep(160 * time.Millisecond)         // past the idle window
	mu.Lock()
	n := evictions
	mu.Unlock()
	if n != 0 {
		t.Fatalf("room evicted %d times while occupied", n)
	}
	b.Close(ctx)
	waitFor(t, "eviction after the real departure", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return evictions == 1
	})
}

func TestHeartbeatKeepsAndDetects(t *testing.T) {
	hub := deepws.NewHub(deepws.WithPingInterval(20 * time.Millisecond))
	srv := httptest.NewServer(hub)
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/?room=hb"
	ctx := context.Background()

	c, err := deepws.Dial[cursor](ctx, url, "a", deepws.WithClientPingInterval(20*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	// Several ping intervals pass; a healthy connection stays up.
	time.Sleep(150 * time.Millisecond)
	select {
	case <-c.Done():
		t.Fatalf("healthy connection died: %v", c.Err())
	default:
	}

	c.Close(ctx)
	srv.Close()
}

func TestHeartbeatDetectsASilentPeer(t *testing.T) {
	// A hub that completes the handshake and then goes silent — never reads
	// again, so pings get no pong. That is what a half-open TCP connection
	// looks like, which httptest cannot simulate directly: its
	// CloseClientConnections does not touch hijacked connections.
	silent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sock, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		ctx := r.Context()
		if _, _, err := sock.Read(ctx); err != nil { // the client's state vector
			return
		}
		sv, _ := crdt.StateVector{}.MarshalBinary()
		_ = sock.Write(ctx, websocket.MessageBinary, append([]byte{1}, sv...)) // frame 1: state vector
		<-ctx.Done()                                                           // silence: no reads, so no pong ever comes back
	}))
	defer silent.Close()

	c, err := deepws.Dial[cursor](context.Background(),
		"ws"+strings.TrimPrefix(silent.URL, "http"), "a",
		deepws.WithClientPingInterval(30*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.Done():
		// The probe noticed; the client is free instead of waiting forever
		// for updates that cannot come.
	case <-time.After(3 * time.Second):
		t.Fatal("client never noticed the silent hub")
	}
}

func TestWithDocumentResumesOfflineEdits(t *testing.T) {
	// The real offline story: the connection dies, the responder keeps
	// typing into the same document, and the next Dial carries those edits up.
	hub, url := startHub(t)
	ctx := context.Background()

	alice, _ := deepws.Dial[cursor](ctx, url, "alice")
	alice.Edit(func(d *crdt.Document) { d.Insert(0, "online. ") })
	if err := alice.Publish(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "hub to hold the online edit", func() bool {
		var s string
		hub.Room("doc-1", func(d *crdt.Document) { s = d.String() })
		return s == "online. "
	})
	alice.Close(ctx)

	// Offline: the old client is closed, but its document is still valid
	// through Edit. Meanwhile the room moves on without her.
	alice.Edit(func(d *crdt.Document) { d.Insert(d.Len(), "offline. ") })

	bob, _ := deepws.Dial[cursor](ctx, url, "bob")
	bob.Edit(func(d *crdt.Document) { d.Insert(d.Len(), "bob was here. ") })
	if err := bob.Publish(ctx); err != nil {
		t.Fatal(err)
	}
	defer bob.Close(ctx)

	// Reconnect resuming the same document: the handshake pushes the offline
	// edit up and pulls bob's edit down. Detach is the sanctioned way to take
	// the document out of a finished client.
	kept := alice.Detach()
	if kept == nil {
		t.Fatal("Detach returned nil after Close")
	}
	alice2, err := deepws.Dial[cursor](ctx, url, "alice", deepws.WithDocument(kept))
	if err != nil {
		t.Fatal(err)
	}
	defer alice2.Close(ctx)

	waitFor(t, "everyone to hold all three edits", func() bool {
		a, b := alice2.Text(), bob.Text()
		return a == b &&
			strings.Contains(a, "online. ") &&
			strings.Contains(a, "offline. ") &&
			strings.Contains(a, "bob was here. ")
	})
}
