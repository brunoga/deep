// Package tui is the responder's live view of one incident: the structured
// record on the left, edited through conditional patches; the shared notes on
// the right, a CRDT document synced over the websocket; and the presence bar
// showing who else is here.
//
// The two panes are the library's two consistency models side by side. The
// record goes through the server — patches, conditions, an audit log — because
// a severity needs one authoritative answer. The notes go peer-to-peer through
// the room — CRDT merges — because prose needs everyone typing at once.
package tui

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	deep "github.com/brunoga/deep/v6"
	"github.com/brunoga/deep/v6/crdt"
	deepws "github.com/brunoga/deep/ws"

	"github.com/brunoga/deep/examples/incident/client"
	"github.com/brunoga/deep/examples/incident/model"
)

// Presence is what each responder announces: a name and where in the notes
// their cursor sits. The hub relays it without looking; only clients with the
// same shape see each other.
type Presence struct {
	Name string `json:"name"`
	Pos  int    `json:"pos"`
}

type pane int

const (
	paneTasks pane = iota
	paneNotes
)

// Messages.
type (
	incidentMsg    model.Incident
	notesMsg       struct{}
	presenceMsg    struct{}
	statusMsg      string
	errMsg         struct{ err error }
	pollTick       struct{}
	beatTick       struct{}
	wsDeadMsg      struct{}
	reconnectedMsg struct{ c *deepws.Client[Presence] }
	retryMsg       struct{}
)

// Model is the bubbletea model.
type Model struct {
	api   *client.Client
	notes *deepws.Client[Presence]
	id    string
	me    string

	inc      model.Incident
	cursor   int    // rune position in the notes
	lastText string // the notes as last rendered, for cursor mapping
	sel      int    // selected task
	focus    pane
	status   string

	width, height int
	program       *tea.Program
}

// Run connects and blocks until the user quits.
func Run(api *client.Client, id string) error {
	inc, err := api.Get(id)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	notes, err := deepws.Dial[Presence](ctx, api.WSURL(id), api.Author())
	cancel()
	if err != nil {
		return fmt.Errorf("joining notes room: %w", err)
	}

	m := &Model{api: api, notes: notes, id: id, me: api.Author(), inc: inc,
		status: "connected", cursor: notes.Len(), lastText: notes.Text()}

	p := tea.NewProgram(m, tea.WithAltScreen())
	m.program = p
	m.watch(notes)

	_, err = p.Run()
	closeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = notes.Close(closeCtx)
	return err
}

// watch forwards a client's remote events — edits, presence, death — into
// the program loop. Remote edits and presence changes land on the read
// goroutine, so they travel as messages.
func (m *Model) watch(c *deepws.Client[Presence]) {
	// p is nil in headless tests, which pump messages by hand.
	send := func(msg tea.Msg) {}
	if p := m.program; p != nil {
		send = func(msg tea.Msg) { p.Send(msg) }
	}
	c.OnUpdate(func() { send(notesMsg{}) })
	c.Awareness().OnChange(func(crdt.PresenceChange[Presence]) { send(presenceMsg{}) })
	go func() {
		<-c.Done()
		send(wsDeadMsg{})
	}()
}

// reconnect detaches the dead client's document and dials again with it —
// nothing typed while offline is lost, which is [deepws.WithDocument]'s whole
// purpose.
func (m *Model) reconnect() tea.Cmd {
	dead := m.notes // captured here: Update may swap m.notes before the cmd runs
	wsurl, me := m.api.WSURL(m.id), m.me
	return func() tea.Msg {
		doc := dead.Detach()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var opts []deepws.ClientOption
		if doc != nil {
			opts = append(opts, deepws.WithDocument(doc))
		}
		c, err := deepws.Dial[Presence](ctx, wsurl, me, opts...)
		if err != nil {
			return retryMsg{}
		}
		return reconnectedMsg{c}
	}
}

func (m *Model) Init() tea.Cmd {
	return tea.Batch(
		m.announce(),
		tea.Tick(2*time.Second, func(time.Time) tea.Msg { return pollTick{} }),
		tea.Tick(10*time.Second, func(time.Time) tea.Msg { return beatTick{} }),
	)
}

// announce sends this client's presence — name and cursor — to the room.
func (m *Model) announce() tea.Cmd {
	notes, pos := m.notes, m.cursor
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = notes.Announce(ctx, Presence{Name: m.me, Pos: pos})
		return nil
	}
}

// publish pushes pending local notes edits to the room.
func (m *Model) publish() tea.Cmd {
	notes := m.notes
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := notes.Publish(ctx); err != nil {
			return errMsg{fmt.Errorf("publish: %w", err)}
		}
		return nil
	}
}

// refresh fetches the incident.
func (m *Model) refresh() tea.Cmd {
	return func() tea.Msg {
		inc, err := m.api.Get(m.id)
		if err != nil {
			return errMsg{err}
		}
		return incidentMsg(inc)
	}
}

// patch sends one patch and refreshes, narrating the outcome.
func (m *Model) patch(p patchReq) tea.Cmd {
	return func() tea.Msg {
		res, err := m.api.Patch(m.id, p.patch)
		if err != nil {
			return errMsg{err}
		}
		if res.Applied == 0 && res.Skipped > 0 {
			return statusMsg(p.skipped)
		}
		return statusMsg(p.done)
	}
}

type patchReq struct {
	patch   deep.Patch[model.Incident]
	done    string
	skipped string
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case incidentMsg:
		m.inc = model.Incident(msg)
		if m.sel >= len(m.inc.Tasks) {
			m.sel = max(0, len(m.inc.Tasks)-1)
		}
		return m, nil

	case notesMsg:
		// A remote edit landed; move the cursor with the text it sits in, so
		// a peer typing above does not drag this client's keystrokes into the
		// middle of their sentence.
		text := m.notes.Text()
		m.cursor = adjustCursor(m.lastText, text, m.cursor)
		m.lastText = text
		return m, nil

	case presenceMsg:
		return m, nil

	case wsDeadMsg:
		m.status = "✗ notes connection lost — reconnecting"
		return m, m.reconnect()

	case retryMsg:
		m.status = "✗ notes offline — retrying"
		return m, tea.Tick(3*time.Second, func(time.Time) tea.Msg { return wsDeadMsg{} })

	case reconnectedMsg:
		m.notes = msg.c
		m.watch(msg.c)
		text := m.notes.Text()
		m.cursor = adjustCursor(m.lastText, text, m.cursor)
		m.lastText = text
		m.status = "notes reconnected"
		return m, tea.Batch(m.publish(), m.announce())

	case statusMsg:
		m.status = string(msg)
		return m, m.refresh()

	case errMsg:
		m.status = "✗ " + msg.err.Error()
		return m, nil

	case pollTick:
		return m, tea.Batch(m.refresh(),
			tea.Tick(2*time.Second, func(time.Time) tea.Msg { return pollTick{} }))

	case beatTick:
		// The presence heartbeat: without it this client fades from the
		// others' presence views.
		return m, tea.Batch(m.announce(),
			tea.Tick(10*time.Second, func(time.Time) tea.Msg { return beatTick{} }))

	case tea.KeyMsg:
		return m.key(msg)
	}
	return m, nil
}

func (m *Model) key(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Global keys.
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "tab":
		if m.focus == paneTasks {
			m.focus = paneNotes
		} else {
			m.focus = paneTasks
		}
		return m, nil
	}
	if m.focus == paneNotes {
		return m.notesKey(msg)
	}
	return m.tasksKey(msg)
}

func (m *Model) tasksKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	taskID := ""
	if m.sel < len(m.inc.Tasks) {
		taskID = m.inc.Tasks[m.sel].ID
	}
	switch msg.String() {
	case "q":
		return m, tea.Quit
	case "j", "down":
		if m.sel < len(m.inc.Tasks)-1 {
			m.sel++
		}
	case "k", "up":
		if m.sel > 0 {
			m.sel--
		}
	case "c":
		if taskID != "" {
			return m, m.patch(patchReq{client.ClaimTask(taskID, m.me),
				"claimed " + taskID, taskID + " already claimed"})
		}
	case "d":
		if taskID != "" {
			return m, m.patch(patchReq{client.CompleteTask(taskID),
				taskID + " done", ""})
		}
	case "1", "2", "3", "4":
		sev := model.Severity(msg.String()[0] - '0')
		return m, m.patch(patchReq{client.Escalate(sev),
			"escalated to " + sev.String(), "already at " + m.inc.Severity.String() + " or worse"})
	case "M":
		return m, m.patch(patchReq{client.SetStatus(model.StatusMitigated), "mitigated", ""})
	case "R":
		return m, m.patch(patchReq{client.SetStatus(model.StatusResolved), "resolved", ""})
	case "C":
		return m, m.patch(patchReq{client.Close(), "closed", ""})
	case "U":
		return m, m.undoLast()
	}
	return m, nil
}

// undoLast reverses the newest audit entry.
func (m *Model) undoLast() tea.Cmd {
	return func() tea.Msg {
		history, err := m.api.History(m.id)
		if err != nil {
			return errMsg{err}
		}
		if len(history) == 0 {
			return statusMsg("nothing to undo")
		}
		seq := history[len(history)-1].Seq
		if _, err := m.api.Undo(m.id, seq); err != nil {
			return errMsg{err}
		}
		return statusMsg(fmt.Sprintf("undid #%d", seq))
	}
}

func (m *Model) notesKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyRunes, tea.KeySpace:
		text := string(msg.Runes)
		if msg.Type == tea.KeySpace {
			text = " "
		}
		m.notes.Edit(func(d *crdt.Document) { d.Insert(m.cursor, text) })
		m.cursor += len([]rune(text))
		m.lastText = m.notes.Text()
		return m, tea.Batch(m.publish(), m.announce())
	case tea.KeyEnter:
		m.notes.Edit(func(d *crdt.Document) { d.Insert(m.cursor, "\n") })
		m.cursor++
		m.lastText = m.notes.Text()
		return m, tea.Batch(m.publish(), m.announce())
	case tea.KeyBackspace:
		if m.cursor > 0 {
			m.notes.Edit(func(d *crdt.Document) { d.Delete(m.cursor-1, 1) })
			m.cursor--
			m.lastText = m.notes.Text()
			return m, tea.Batch(m.publish(), m.announce())
		}
	case tea.KeyLeft:
		if m.cursor > 0 {
			m.cursor--
			return m, m.announce()
		}
	case tea.KeyRight:
		if m.cursor < m.notes.Len() {
			m.cursor++
			return m, m.announce()
		}
	case tea.KeyHome:
		m.cursor = 0
		return m, m.announce()
	case tea.KeyEnd:
		m.cursor = m.notes.Len()
		return m, m.announce()
	}
	return m, nil
}

// adjustCursor maps a cursor position across a text change, by rune. The
// changed region is found as what lies between the common prefix and common
// suffix: a change entirely before the cursor shifts it by the length delta,
// a change entirely after leaves it alone, and a change spanning it pins it
// to the end of the replacement.
func adjustCursor(oldText, newText string, cursor int) int {
	if oldText == newText {
		return min(cursor, len([]rune(newText)))
	}
	oldRunes, newRunes := []rune(oldText), []rune(newText)

	prefix := 0
	for prefix < len(oldRunes) && prefix < len(newRunes) && oldRunes[prefix] == newRunes[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(oldRunes)-prefix && suffix < len(newRunes)-prefix &&
		oldRunes[len(oldRunes)-1-suffix] == newRunes[len(newRunes)-1-suffix] {
		suffix++
	}

	switch {
	case cursor <= prefix:
		return cursor
	case cursor >= len(oldRunes)-suffix:
		return cursor + len(newRunes) - len(oldRunes)
	default:
		return len(newRunes) - suffix
	}
}
