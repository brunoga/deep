package main

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/brunoga/deep/v6/crdt"
)

// Cursor is what each editor broadcasts about itself. It is not part of the
// document: a cursor position is not an edit, must not be merged into the text,
// and should disappear when its owner does.
type Cursor struct {
	Name   string `json:"name"`
	Colour string `json:"colour"`
	Index  int    `json:"index"`
}

// A settable clock, so this example can show a timeout without waiting for one.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func main() {
	clock := &fakeClock{t: time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)}
	opts := []crdt.AwarenessOption[Cursor]{
		crdt.WithTTL[Cursor](10 * time.Second),
		crdt.WithClock[Cursor](clock.now),
	}

	alice := crdt.NewAwareness("alice", opts...)
	bob := crdt.NewAwareness("bob", opts...)

	// Alice watches who comes and goes.
	alice.OnChange(func(c crdt.PresenceChange[Cursor]) {
		if c.Kind == crdt.PresenceLeft {
			fmt.Printf("  [alice sees] %s %s\n", c.Node, c.Kind)
			return
		}
		fmt.Printf("  [alice sees] %s %s at index %d\n", c.Node, c.Kind, c.State.Index)
	})

	fmt.Println("== announcing yourself ==")
	alice.SetLocal(Cursor{Name: "Alice", Colour: "#e11", Index: 0})
	alice.Apply(bob.SetLocal(Cursor{Name: "Bob", Colour: "#11e", Index: 12}))

	fmt.Println("\n== moving ==")
	// Broadcasting is just a value; here it goes over JSON, as it would over a
	// websocket.
	wire, _ := json.Marshal(bob.SetLocal(Cursor{Name: "Bob", Colour: "#11e", Index: 20}))
	fmt.Printf("  on the wire: %s\n", wire)
	var update crdt.AwarenessUpdate[Cursor]
	_ = json.Unmarshal(wire, &update)
	alice.Apply(update)

	fmt.Println("\n== who is here ==")
	for node, c := range alice.States() {
		fmt.Printf("  %-6s %-6s index %d\n", node, c.Name, c.Index)
	}

	// ── leaving, both ways ───────────────────────────────────────────────────
	fmt.Println("\n== a peer that says goodbye ==")
	alice.Apply(bob.Leave())
	fmt.Printf("  bob still listed: %v\n", contains(alice.States(), "bob"))

	fmt.Println("\n== a peer that just goes quiet ==")
	carol := crdt.NewAwareness("carol", opts...)
	alice.Apply(carol.SetLocal(Cursor{Name: "Carol", Index: 3}))

	clock.advance(9 * time.Second)
	fmt.Printf("  after 9s, carol listed: %v\n", contains(alice.States(), "carol"))
	clock.advance(2 * time.Second)
	fmt.Printf("  after 11s, carol listed: %v\n", contains(alice.States(), "carol"))
	fmt.Println("  no coordination was needed to drop her: presence is")
	fmt.Println("  ephemeral, so a peer wrongly dropped simply reappears with")
	fmt.Println("  its next update. That is why the timeout can be local.")

	fmt.Println("\n== and she does ==")
	alice.Apply(carol.SetLocal(Cursor{Name: "Carol", Index: 4}))
	fmt.Printf("  carol listed again: %v\n", contains(alice.States(), "carol"))
}

func contains(m map[string]Cursor, k string) bool {
	_, ok := m[k]
	return ok
}
