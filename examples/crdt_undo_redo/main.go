package main

import (
	"fmt"

	"github.com/brunoga/deep/v6"
	"github.com/brunoga/deep/v6/crdt"
)

// Undoing an edit in a distributed setting cannot just rewind local state: the
// peers already saw the original edit. CRDT.Reverse applies the inverse locally
// and returns it as a new Delta with a fresh timestamp — causally after the
// edit it undoes — so it propagates and converges like any other change.
type Doc struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

func main() {
	author := crdt.NewCRDT(Doc{Title: "Untitled", Body: ""}, "author")
	reader := crdt.NewCRDT(Doc{Title: "Untitled", Body: ""}, "reader")

	// The author writes, and the reader receives the edit.
	edit := author.Edit(func(d *Doc) {
		d.Title = "Deep Dive"
		d.Body = "First draft."
	})
	reader.ApplyDelta(edit)

	fmt.Println("--- AFTER EDIT ---")
	show(author, reader)

	// Second thoughts: undo. The undo delta goes out to the peer too.
	undo := author.Reverse(edit)
	reader.ApplyDelta(undo)

	fmt.Println("\n--- AFTER UNDO ---")
	show(author, reader)

	// Redo is just reversing the undo.
	redo := author.Reverse(undo)
	reader.ApplyDelta(redo)

	fmt.Println("\n--- AFTER REDO ---")
	show(author, reader)

	// Deltas are idempotent: redelivering one changes nothing.
	reader.ApplyDelta(redo)
	reader.ApplyDelta(edit)

	fmt.Println("\n--- AFTER REDELIVERING OLD DELTAS ---")
	show(author, reader)
	fmt.Println("\nUndo and redo are ordinary deltas, so peers converge on them")
	fmt.Println("and duplicate delivery is harmless.")
}

func show(a, b *crdt.CRDT[Doc]) {
	va, vb := a.View(), b.View()
	fmt.Printf("  author: %+v\n", va)
	fmt.Printf("  reader: %+v\n", vb)
	fmt.Printf("  converged: %v\n", deep.Equal(va, vb))
}
