package main

import (
	"fmt"

	"github.com/brunoga/deep/v3/crdt"
)

// Document represents a text document using the specialized CRDT Text type.
type Document struct {
	Content crdt.Text
}

func main() {
	// Initialize two documents.
	docA := crdt.NewCRDT(Document{Content: crdt.Text{}}, "A")
	docB := crdt.NewCRDT(Document{Content: crdt.Text{}}, "B")

	fmt.Println("--- Initial State: Empty ---")

	// User A types "Hello"
	fmt.Println("\n--- A types 'Hello' ---")
	deltaA1 := docA.Edit(func(d *Document) {
		// Insert "Hello" at position 0
		d.Content = d.Content.Insert(0, "Hello", docA.Clock())
	})
	fmt.Printf("Doc A: %s\n", docA.View().Content)

	// B receives "Hello"
	docB.ApplyDelta(deltaA1)
	fmt.Printf("Doc B: %s\n", docB.View().Content)

	// Concurrent Editing!
	fmt.Println("\n--- Concurrent Edits ---")
	fmt.Println("A appends ' World'")
	fmt.Println("B inserts '!' at index 5")

	// A appends " World" at index 5 (after "Hello")
	deltaA2 := docA.Edit(func(d *Document) {
		d.Content = d.Content.Insert(5, " World", docA.Clock())
	})

	// B inserts "!" at index 5 (after "Hello")
	deltaB1 := docB.Edit(func(d *Document) {
		d.Content = d.Content.Insert(5, "!", docB.Clock())
	})

	fmt.Printf("Doc A (local): %s\n", docA.View().Content)
	fmt.Printf("Doc B (local): %s\n", docB.View().Content)

	// Sync
	fmt.Println("\n--- Syncing ---")

	// A receives B's insertion
	docA.ApplyDelta(deltaB1)
	fmt.Printf("Doc A (after B): %s\n", docA.View().Content)

	// B receives A's appending
	docB.ApplyDelta(deltaA2)
	fmt.Printf("Doc B (after A): %s\n", docB.View().Content)

	if docA.View().Content.String() == docB.View().Content.String() {
		fmt.Println("SUCCESS: Documents converged!")
	} else {
		fmt.Println("FAILURE: Divergence!")
	}

	// More complex: Interleaved insertion at the same position
	fmt.Println("\n--- Concurrent Insertion at Same Position ---")

	// Both insert at the end
	pos := len(docA.View().Content.String())

	// A inserts "X"
	deltaA3 := docA.Edit(func(d *Document) {
		d.Content = d.Content.Insert(pos, "X", docA.Clock())
	})

	// B inserts "Y"
	deltaB2 := docB.Edit(func(d *Document) {
		d.Content = d.Content.Insert(pos, "Y", docB.Clock())
	})

	docA.ApplyDelta(deltaB2)
	docB.ApplyDelta(deltaA3)

	fmt.Printf("Doc A: %s\n", docA.View().Content)
	fmt.Printf("Doc B: %s\n", docB.View().Content)

	if docA.View().Content.String() == docB.View().Content.String() {
		fmt.Println("SUCCESS: Converged (deterministic order)! ")
	} else {
		fmt.Println("FAILURE: Divergence!")
	}
}
