package tui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/brunoga/deep/v6/crdt"
	deepws "github.com/brunoga/deep/ws"

	"github.com/brunoga/deep/examples/incident/client"
	"github.com/brunoga/deep/examples/incident/model"
	"github.com/brunoga/deep/examples/incident/server"
)

// A headless run of the model: real server, real websocket room, no
// terminal. This is the TUI's wiring — patches out of keystrokes, CRDT edits
// out of typing, rendering out of state — everything but the pixels.
func testModel(t *testing.T) (*Model, *server.Store) {
	t.Helper()
	store, err := server.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Create(model.Incident{
		ID: "inc-1", Title: "checkout errors", Severity: model.Sev2, Status: model.StatusOpen,
		Tasks: []model.Task{{ID: "t1", Text: "page db oncall"}},
	}); err != nil {
		t.Fatal(err)
	}
	api := server.NewAPI(store)
	notes, err := server.NewNotes(t.TempDir(), time.Hour,
		func(*http.Request) bool { return true },
		func(id string) bool { return id == "inc-1" })
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/ws", notes)
	mux.Handle("/", api)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	c := client.New(srv.URL, "", "ana")
	inc, err := c.Get("inc-1")
	if err != nil {
		t.Fatal(err)
	}
	ws, err := deepws.Dial[Presence](context.Background(), c.WSURL("inc-1"), "ana")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		ws.Close(ctx)
	})

	m := &Model{api: c, notes: ws, id: "inc-1", me: "ana", inc: inc, status: "connected"}
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return m, store
}

// drain runs a command and feeds its message back, like the program loop
// would, until commands stop producing messages we understand.
func drain(t *testing.T, m *Model, cmd tea.Cmd) {
	t.Helper()
	for cmd != nil {
		msg := cmd()
		switch v := msg.(type) {
		case nil:
			return
		case tea.BatchMsg:
			for _, sub := range v {
				drain(t, m, sub)
			}
			return
		case errMsg:
			t.Fatalf("command failed: %v", v.err)
		default:
			_, cmd = m.Update(msg)
		}
	}
}

func TestTypingReachesTheRoom(t *testing.T) {
	m, _ := testModel(t)
	m.focus = paneNotes

	for _, r := range "db failing over" {
		_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		drain(t, m, cmd)
	}
	if got := m.notes.Text(); got != "db failing over" {
		t.Fatalf("local text = %q", got)
	}

	// A second client joining the room receives what was typed — through the
	// hub, not through the model.
	ws2, err := deepws.Dial[Presence](context.Background(), m.api.WSURL("inc-1"), "bruno")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		ws2.Close(ctx)
	}()
	if got := ws2.Text(); got != "db failing over" {
		t.Fatalf("peer text = %q", got)
	}
}

func TestKeystrokesBecomePatches(t *testing.T) {
	m, store := testModel(t)

	// "c" claims the selected task — through the conditional patch.
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	drain(t, m, cmd)
	inc, _ := store.Get("inc-1")
	if inc.Tasks[0].Owner != "ana" {
		t.Fatalf("claim did not land: %+v", inc.Tasks[0])
	}

	// "1" escalates.
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("1")})
	drain(t, m, cmd)
	inc, _ = store.Get("inc-1")
	if inc.Severity != model.Sev1 {
		t.Fatalf("escalation did not land: %v", inc.Severity)
	}

	// "U" undoes the escalation.
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("U")})
	drain(t, m, cmd)
	inc, _ = store.Get("inc-1")
	if inc.Severity != model.Sev2 {
		t.Fatalf("undo did not land: %v", inc.Severity)
	}
}

func TestViewRenders(t *testing.T) {
	m, _ := testModel(t)
	m.notes.Edit(func(d *crdt.Document) { d.Insert(0, "hello") })

	view := m.View()
	for _, want := range []string{"SEV2", "checkout errors", "t1", "hello", "connected"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q", want)
		}
	}
}

func TestAdjustCursor(t *testing.T) {
	cases := []struct {
		name         string
		old, new     string
		cursor, want int
	}{
		{"insert before cursor shifts it", "world", "hello world", 3, 9},
		{"insert after cursor leaves it", "hello", "hello world", 3, 3},
		{"delete before cursor shifts it", "hello world", "world", 8, 2},
		{"delete containing cursor pins to change end", "abcdef", "af", 3, 1},
		{"unchanged text clamps only", "ab", "ab", 5, 2},
		{"emoji count as one rune", "🙂🙂", "x🙂🙂", 1, 2},
	}
	for _, c := range cases {
		if got := adjustCursor(c.old, c.new, c.cursor); got != c.want {
			t.Errorf("%s: adjustCursor(%q, %q, %d) = %d, want %d", c.name, c.old, c.new, c.cursor, got, c.want)
		}
	}
}

// The reconnect path: the connection dies, the responder keeps typing, and
// the TUI dials back in with the same document — nothing typed offline is
// lost, and the room's history comes back down.
func TestReconnectKeepsOfflineEdits(t *testing.T) {
	m, _ := testModel(t)
	m.focus = paneNotes

	for _, r := range "before " {
		_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		drain(t, m, cmd)
	}

	// The connection dies; typing continues into the dead client's document.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	m.notes.Close(ctx)
	cancel()
	old := m.notes
	old.Edit(func(d *crdt.Document) { d.Insert(d.Len(), "offline ") })

	// The death notice triggers the reconnect, which must resume that
	// document.
	_, cmd := m.Update(wsDeadMsg{})
	drain(t, m, cmd)
	if m.notes == old {
		t.Fatal("reconnect did not swap in a new client")
	}
	if got := m.notes.Text(); got != "before offline " {
		t.Fatalf("text after reconnect = %q", got)
	}

	// And the room has it: a fresh peer sees both halves.
	peer, err := deepws.Dial[Presence](context.Background(), m.api.WSURL("inc-1"), "peer")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		peer.Close(ctx)
	}()
	if got := peer.Text(); got != "before offline " {
		t.Fatalf("peer text = %q", got)
	}
}
