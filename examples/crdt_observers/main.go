package main

import (
	"fmt"

	"github.com/brunoga/deep/v6/crdt"
)

// A user interface needs to know what changed, not just that something did.
// OnChange hands you the patch that was actually applied, so you can redraw the
// affected fields instead of diffing snapshots or rebuilding the whole view.
type Doc struct {
	Title  string         `json:"title"`
	Author string         `json:"author"`
	Views  map[string]int `json:"views"`
}

func main() {
	server := crdt.NewCRDT(Doc{Views: map[string]int{}}, "server")
	client := crdt.NewCRDT(Doc{Views: map[string]int{}}, "client")

	// A peer that goes offline early: its edit is already outdated by the time
	// it is delivered.
	offline := crdt.NewCRDT(Doc{Views: map[string]int{}}, "offline")
	staleDelta := offline.Edit(func(d *Doc) { d.Title = "Written While Offline" })

	// The client redraws only what changed, and notes where it came from.
	cancel := client.OnChange(func(c crdt.Change[Doc]) {
		fmt.Printf("  [%s] %d operation(s)\n", c.Source, len(c.Patch.Operations))
		for _, op := range c.Patch.Operations {
			fmt.Printf("    redraw %s = %v\n", op.Path, op.New)
		}
	})

	fmt.Println("--- LOCAL EDIT ---")
	client.Edit(func(d *Doc) { d.Title = "Draft" })

	fmt.Println("\n--- REMOTE DELTA ---")
	delta := server.Edit(func(d *Doc) {
		d.Author = "alice"
		d.Views["home"] = 1
	})
	client.ApplyDelta(delta)

	// Only operations that survive conflict resolution are announced. The
	// offline peer's title is older than the client's, so it is neither applied
	// nor reported — an interface driven by these notifications never draws a
	// change that did not happen.
	fmt.Println("\n--- STALE DELTA FROM THE OFFLINE PEER ---")
	client.ApplyDelta(staleDelta)
	fmt.Printf("  nothing announced; title is still %q\n", client.View().Title)

	fmt.Println("\n--- FULL MERGE ---")
	server.Edit(func(d *Doc) { d.Views["about"] = 5 })
	client.Merge(server)

	fmt.Println("\n--- AFTER UNSUBSCRIBING ---")
	cancel()
	client.Edit(func(d *Doc) { d.Title = "Final" })
	fmt.Println("  (nothing printed)")

	fmt.Printf("\nFinal state: %+v\n", client.View())
}
