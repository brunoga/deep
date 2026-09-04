package main

import (
	"encoding/json"
	"fmt"

	"github.com/brunoga/deep/v6/crdt"
)

// A replica remembers when every path was last written and when every path was
// deleted, so that it can recognize a stale update. Deleted text is likewise
// kept as a tombstone, because a replica that has not heard about the deletion
// may still insert next to what was deleted. Neither record shrinks on its own,
// so a long-lived replica ends up carrying more history than data.
//
// Compact discards that history — but only as far as a watermark you supply,
// because what it protects against is an old update arriving late. The
// watermark has to be older than anything still in flight anywhere in the
// system. Working it out is the application's job, since only it knows who the
// peers are; the usual answer is the oldest timestamp any peer has acknowledged.
type Doc struct {
	Sessions map[string]int `json:"sessions"`
	Notes    crdt.Text      `json:"notes"`
}

func main() {
	server := crdt.NewCRDT(Doc{Sessions: map[string]int{}}, "server")
	replica := crdt.NewCRDT(Doc{Sessions: map[string]int{}}, "replica")

	// A day's churn: sessions open and close, notes are typed and revised.
	var deltas []crdt.Delta[Doc]
	for i := 0; i < 200; i++ {
		key := fmt.Sprintf("session-%d", i)
		deltas = append(deltas, server.Edit(func(d *Doc) { d.Sessions[key] = i }))
		deltas = append(deltas, server.Edit(func(d *Doc) { delete(d.Sessions, key) }))
	}
	for i := 0; i < 50; i++ {
		deltas = append(deltas, server.Edit(func(d *Doc) {
			d.Notes = d.Notes.Insert(0, "draft text ", server.Clock())
			d.Notes = d.Notes.Delete(0, len("draft text "))
		}))
	}
	deltas = append(deltas, server.Edit(func(d *Doc) {
		d.Notes = d.Notes.Insert(d.Notes.Len(), "final wording", server.Clock())
	}))

	for _, d := range deltas {
		replica.ApplyDelta(d)
	}

	before, _ := json.Marshal(server)
	fmt.Println("--- AFTER A DAY OF CHURN ---")
	fmt.Printf("  visible: %d sessions, notes %q\n",
		len(server.View().Sessions), server.View().Notes.String())
	fmt.Printf("  stored:  %d bytes, %d text runs\n", len(before), len(server.View().Notes))

	// Every delta above has reached the replica, so everything up to now is
	// safe to forget. In a real system this watermark comes from the peers:
	// the oldest timestamp all of them have acknowledged.
	watermark := server.Clock().Now()

	// One call: it drops the bookkeeping and the text tombstones together, on
	// this replica only — nothing is sent to peers, because what the replica
	// represents has not changed.
	dropped := server.Compact(watermark)

	after, _ := json.Marshal(server)
	fmt.Println("\n--- AFTER COMPACTION ---")
	fmt.Printf("  visible: %d sessions, notes %q\n",
		len(server.View().Sessions), server.View().Notes.String())
	fmt.Printf("  stored:  %d bytes, %d text runs (%d metadata entries dropped)\n",
		len(after), len(server.View().Notes), dropped)
	fmt.Printf("  reclaimed %.0f%% of the stored state, with the value unchanged\n",
		100*(1-float64(len(after))/float64(len(before))))

	// A replica that compacted still converges with one that did not.
	fmt.Println("\n--- STILL CONVERGES WITH AN UNCOMPACTED PEER ---")
	post := server.Edit(func(d *Doc) { d.Sessions["new"] = 1 })
	replica.ApplyDelta(post)
	fmt.Printf("  server:  %v, notes %q\n", server.View().Sessions, server.View().Notes.String())
	fmt.Printf("  replica: %v, notes %q\n", replica.View().Sessions, replica.View().Notes.String())
}
