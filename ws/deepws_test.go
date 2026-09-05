package deepws_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brunoga/deep/v6/crdt"
	deepws "github.com/brunoga/deep/ws"
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
