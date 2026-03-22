package main

import (
	"fmt"

	v5 "github.com/brunoga/deep/v5"
	"github.com/brunoga/deep/v5/crdt"
)

type SharedDoc struct {
	Title   string `json:"title"`
	Content string `json:"content"`
}

func main() {
	initial := SharedDoc{Title: "Untitled", Content: ""}

	nodeA := crdt.NewCRDT(initial, "node-a")
	nodeB := crdt.NewCRDT(initial, "node-b")

	// Concurrent edits: A edits the title, B edits the content.
	deltaA := nodeA.Edit(func(d *SharedDoc) {
		d.Title = "My Document"
	})
	deltaB := nodeB.Edit(func(d *SharedDoc) {
		d.Content = "Hello, World!"
	})

	fmt.Println("--- CONCURRENT EDITS ---")
	fmt.Println("Node A: title   → \"My Document\"")
	fmt.Println("Node B: content → \"Hello, World!\"")

	// Exchange deltas (simulate network delivery).
	nodeA.ApplyDelta(deltaB)
	nodeB.ApplyDelta(deltaA)

	viewA := nodeA.View()
	viewB := nodeB.View()

	fmt.Println("\n--- AFTER SYNC ---")
	fmt.Printf("Node A: %+v\n", viewA)
	fmt.Printf("Node B: %+v\n", viewB)

	if v5.Equal(viewA, viewB) {
		fmt.Println("\nSUCCESS: Both nodes converged!")
	}
}
