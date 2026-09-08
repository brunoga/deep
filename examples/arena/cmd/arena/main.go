// arena is the player's terminal client: a grid you walk around with the
// arrow keys, rendered from a replica that the server's tick patches keep
// current.
//
// Press r for instant replay: the client rewinds its own world through the
// Reverse of the patches it has already applied — no server round trip, no
// second data structure; the patch stream it synced from is also its
// recording of the recent past.
//
//	arena -server ws://localhost:7777 -name ana
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	deep "github.com/brunoga/deep/v6"

	"github.com/brunoga/deep/examples/arena/client"
	"github.com/brunoga/deep/examples/arena/world"
)

func main() {
	server := flag.String("server", "ws://localhost:7777", "arena server")
	name := flag.String("name", os.Getenv("USER"), "player name")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	c, err := client.Dial(ctx, *server+"/?name="+*name, *name)
	cancel()
	if err != nil {
		log.Fatalf("joining: %v", err)
	}
	defer c.Close()

	m := &model{c: c, status: "connected — arrows move, r replays, q quits"}
	p := tea.NewProgram(m, tea.WithAltScreen())
	c.OnTick(func() { p.Send(tickMsg{}) })
	go func() {
		<-c.Done()
		p.Send(deadMsg{})
	}()
	if _, err := p.Run(); err != nil {
		log.Fatal(err)
	}
}

type (
	tickMsg   struct{}
	deadMsg   struct{}
	replayFrame struct{}
)

type model struct {
	c      *client.Client
	status string

	// Instant replay: a strip of past worlds, walked backward on the screen
	// while the live replica keeps ticking underneath.
	replaying bool
	frames    []world.World
	frame     int
}

func (m *model) Init() tea.Cmd { return nil }

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tickMsg:
		return m, nil
	case deadMsg:
		m.status = "connection lost"
		return m, tea.Quit
	case replayFrame:
		m.frame++
		if m.frame >= len(m.frames) {
			m.replaying = false
			m.status = "back to live"
			return m, nil
		}
		return m, tea.Tick(40*time.Millisecond, func(time.Time) tea.Msg { return replayFrame{} })
	case tea.KeyMsg:
		if m.replaying {
			return m, nil // watch it out
		}
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "up", "w":
			return m, m.move(0, -1)
		case "down", "s":
			return m, m.move(0, 1)
		case "left", "a":
			return m, m.move(-1, 0)
		case "right", "d":
			return m, m.move(1, 0)
		case "r":
			return m, m.startReplay()
		}
	}
	return m, nil
}

func (m *model) move(dx, dy int) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if _, err := m.c.Move(ctx, dx, dy); err != nil {
			return deadMsg{}
		}
		return nil
	}
}

// startReplay builds the strip of past worlds by walking the ring of applied
// patches backward with Reverse, then plays it oldest-first.
func (m *model) startReplay() tea.Cmd {
	ring := m.c.Ring()
	if len(ring) == 0 {
		m.status = "nothing to replay yet"
		return nil
	}
	w := m.c.World()
	frames := []world.World{w}
	for i := len(ring) - 1; i >= 0; i-- {
		if err := deep.Apply(&w, ring[i].Reverse()); err != nil {
			m.status = "replay unavailable: " + err.Error()
			return nil
		}
		frames = append(frames, deep.Clone(w))
	}
	// frames is now newest-to-oldest; flip it so the replay runs forward
	// from the past to the present.
	for i, j := 0, len(frames)-1; i < j; i, j = i+1, j-1 {
		frames[i], frames[j] = frames[j], frames[i]
	}
	m.frames = frames
	m.frame = 0
	m.replaying = true
	m.status = fmt.Sprintf("replaying the last %d ticks", len(ring))
	return tea.Tick(40*time.Millisecond, func(time.Time) tea.Msg { return replayFrame{} })
}

var (
	hues = []lipgloss.Style{
		lipgloss.NewStyle().Foreground(lipgloss.Color("1")),
		lipgloss.NewStyle().Foreground(lipgloss.Color("2")),
		lipgloss.NewStyle().Foreground(lipgloss.Color("3")),
		lipgloss.NewStyle().Foreground(lipgloss.Color("4")),
		lipgloss.NewStyle().Foreground(lipgloss.Color("5")),
		lipgloss.NewStyle().Foreground(lipgloss.Color("6")),
	}
	selfStyle   = lipgloss.NewStyle().Bold(true).Reverse(true)
	gemStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	floorStyle  = lipgloss.NewStyle().Faint(true)
	banner      = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("11"))
	statusStyle = lipgloss.NewStyle().Faint(true)
)

func (m *model) View() string {
	w := m.c.World()
	header := ""
	if m.replaying && m.frame < len(m.frames) {
		w = m.frames[m.frame]
		header = banner.Render("◀◀ REPLAY") + "\n"
	}
	return header + renderWorld(w, m.c.You()) + "\n" + statusStyle.Render(" "+m.status)
}

func renderWorld(w world.World, you string) string {
	// The grid, row by row.
	grid := make([][]string, w.Height)
	for y := range grid {
		grid[y] = make([]string, w.Width)
		for x := range grid[y] {
			grid[y][x] = floorStyle.Render("·")
		}
	}
	for _, g := range w.Gems {
		if w.InBounds(g.X, g.Y) {
			grid[g.Y][g.X] = gemStyle.Render(string(rune('0' + g.Value)))
		}
	}
	for id, p := range w.Players {
		if !w.InBounds(p.X, p.Y) {
			continue
		}
		letter := strings.ToUpper(id[:1])
		cell := hues[p.Hue%len(hues)].Render(letter)
		if id == you {
			cell = selfStyle.Render(letter)
		}
		grid[p.Y][p.X] = cell
	}
	var rows []string
	for _, row := range grid {
		rows = append(rows, " "+strings.Join(row, " "))
	}
	board := strings.Join(rows, "\n")

	// The scoreboard, best first.
	type entry struct {
		id    string
		p     world.Player
	}
	entries := make([]entry, 0, len(w.Players))
	for id, p := range w.Players {
		entries = append(entries, entry{id, p})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].p.Score != entries[j].p.Score {
			return entries[i].p.Score > entries[j].p.Score
		}
		return entries[i].id < entries[j].id
	})
	var side []string
	side = append(side, fmt.Sprintf("tick %d", w.Tick), "")
	for _, e := range entries {
		label := fmt.Sprintf("%-10s %4d", e.id, e.p.Score)
		if e.id == you {
			label = selfStyle.Render(label)
		} else {
			label = hues[e.p.Hue%len(hues)].Render(label)
		}
		side = append(side, label)
	}

	return lipgloss.JoinHorizontal(lipgloss.Top, board, "   ", strings.Join(side, "\n"))
}
