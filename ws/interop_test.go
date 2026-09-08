package deepws_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brunoga/deep/v6/crdt"
	deepws "github.com/brunoga/deep/ws"
)

// The cross-language test: a real hub, a real websocket, and a client written
// in the other language.
//
// The conformance corpus proves the two implementations agree about recorded
// bytes. This proves they agree while talking to each other — handshake,
// relay, convergence and presence — which is the only way to find out that,
// say, one of them publishes before the other is listening.
func TestJavaScriptClientJoinsARoom(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed; skipping the cross-language test")
	}
	script, err := filepath.Abs(filepath.Join("..", "js", "test", "interop", "client.mjs"))
	if err != nil {
		t.Fatal(err)
	}

	hub := deepws.NewHub()
	srv := httptest.NewServer(hub)
	defer srv.Close()
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/?room=interop"

	ctx := context.Background()

	// A Go client types first, so the JavaScript one has something to receive
	// during its handshake.
	goClient, err := deepws.Dial[cursor](ctx, url, "go-client")
	if err != nil {
		t.Fatal(err)
	}
	defer goClient.Close(ctx)
	goClient.Edit(func(d *crdt.Document) { d.Insert(0, "written in Go. ") })
	if err := goClient.Publish(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the hub to hold the Go text", func() bool {
		var s string
		hub.Room("interop", func(d *crdt.Document) { s = d.String() })
		return s == "written in Go. "
	})

	// Now the JavaScript client joins, appends, and reports what it holds.
	runCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(runCtx, node, script, url, "js-client",
		"insert:15:and in JavaScript.", "announce:web")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("javascript client: %v\n%s", err, out)
	}

	var got struct {
		Text   string   `json:"text"`
		Length int      `json:"length"`
		Peers  []string `json:"peers"`
	}
	// The script prints one JSON line; anything before it is noise worth
	// showing if the parse fails.
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &got); err != nil {
		t.Fatalf("javascript client output: %v\n%s", err, out)
	}

	const want = "written in Go. and in JavaScript."
	if got.Text != want {
		t.Errorf("javascript client holds %q, want %q", got.Text, want)
	}
	if got.Length != len([]rune(want)) {
		t.Errorf("javascript client length = %d, want %d", got.Length, len([]rune(want)))
	}

	// And the Go side must converge on what JavaScript wrote.
	waitFor(t, "the Go client to receive the JavaScript text", func() bool {
		return goClient.Text() == want
	})
	waitFor(t, "the hub to agree", func() bool {
		var s string
		hub.Room("interop", func(d *crdt.Document) { s = d.String() })
		return s == want
	})
}

// Presence crosses the boundary too: the JavaScript client's announcement
// must reach a Go peer's awareness view.
func TestJavaScriptPresenceReachesGo(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed; skipping the cross-language test")
	}
	script, err := filepath.Abs(filepath.Join("..", "js", "test", "interop", "client.mjs"))
	if err != nil {
		t.Fatal(err)
	}

	hub := deepws.NewHub()
	srv := httptest.NewServer(hub)
	defer srv.Close()
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/?room=presence"
	ctx := context.Background()

	watcher, err := deepws.Dial[jsPresence](ctx, url, "watcher")
	if err != nil {
		t.Fatal(err)
	}
	defer watcher.Close(ctx)

	// Sightings are recorded as they happen rather than read at the end: the
	// JavaScript client says goodbye when it closes, and a peer that has left
	// is *supposed* to be gone from the view by then. Both halves of that are
	// worth asserting.
	var mu sync.Mutex
	var joined, left bool
	cancelWatch := watcher.Awareness().OnChange(func(c crdt.PresenceChange[jsPresence]) {
		if c.Node != "js-peer" {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		switch c.Kind {
		case crdt.PresenceLeft:
			left = true
		default:
			if c.State.Name == "Ada" {
				joined = true
			}
		}
	})
	defer cancelWatch()

	runCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(runCtx, node, script, url, "js-peer", "announce:Ada")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("javascript client: %v\n%s", err, out)
	}

	waitFor(t, "the Go peer to see the JavaScript client announce itself", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return joined
	})
	waitFor(t, "the Go peer to see it leave", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return left
	})
	if _, still := watcher.Awareness().States()["js-peer"]; still {
		t.Error("a client that said goodbye is still shown as present")
	}
}

// jsPresence is the shape the JavaScript client announces.
type jsPresence struct {
	Name string `json:"name"`
}
