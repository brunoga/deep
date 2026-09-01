package main

import (
	"fmt"

	"github.com/brunoga/deep/v5/crdt"
)

// An ordinary slice inside a CRDT is synchronized as one value: two replicas
// editing it concurrently resolve by last-write-wins, and one of the edits is
// simply lost. crdt.List is a sequence that merges instead — every insertion
// and deletion survives, and every replica agrees on the resulting order.
//
// Elements are placed relative to their neighbours rather than by index, so
// concurrent edits do not fight over positions.
type Board struct {
	Name  string            `json:"name"`
	Tasks crdt.List[string] `json:"tasks"`
}

func main() {
	alice := crdt.NewCRDT(Board{Name: "Sprint"}, "alice")
	bob := crdt.NewCRDT(Board{Name: "Sprint"}, "bob")

	// Alice seeds the board and Bob receives it.
	seed := alice.Edit(func(b *Board) {
		b.Tasks = b.Tasks.Insert(0, "design", alice.Clock())
		b.Tasks = b.Tasks.Insert(1, "build", alice.Clock())
		b.Tasks = b.Tasks.Insert(2, "ship", alice.Clock())
	})
	bob.ApplyDelta(seed)

	fmt.Println("--- SHARED BOARD ---")
	fmt.Printf("  %v\n", alice.View().Tasks.Items())

	// They go offline and edit the same region concurrently: Alice inserts
	// after "design", Bob inserts at the same position and removes "ship".
	fromAlice := alice.Edit(func(b *Board) {
		b.Tasks = b.Tasks.Insert(1, "review design", alice.Clock())
	})
	fromBob := bob.Edit(func(b *Board) {
		b.Tasks = b.Tasks.Insert(1, "write spec", bob.Clock())
		b.Tasks = b.Tasks.Delete(3, 1) // "ship"
	})

	fmt.Println("\n--- DIVERGED (offline) ---")
	fmt.Printf("  alice: %v\n", alice.View().Tasks.Items())
	fmt.Printf("  bob:   %v\n", bob.View().Tasks.Items())

	// The partition heals and they exchange deltas.
	alice.ApplyDelta(fromBob)
	bob.ApplyDelta(fromAlice)

	fmt.Println("\n--- AFTER SYNC ---")
	fmt.Printf("  alice: %v\n", alice.View().Tasks.Items())
	fmt.Printf("  bob:   %v\n", bob.View().Tasks.Items())

	same := fmt.Sprint(alice.View().Tasks.Items()) == fmt.Sprint(bob.View().Tasks.Items())
	fmt.Printf("\nConverged: %v\n", same)
	fmt.Println("Both insertions survived and the deletion held — an ordinary")
	fmt.Println("slice would have kept just one writer's version of the list.")

	// Redelivering a delta changes nothing.
	alice.ApplyDelta(fromBob)
	fmt.Printf("Unchanged after redelivery: %v\n",
		fmt.Sprint(alice.View().Tasks.Items()) == fmt.Sprint(bob.View().Tasks.Items()))
}
