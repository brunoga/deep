package main

import (
	"fmt"

	"github.com/brunoga/deep/v4"
)

// Document represents a document being edited.
type Document struct {
	Title    string
	Content  string
	Metadata map[string]string
}

func main() {
	// 1. Initial Document.
	doc := Document{
		Title:   "Draft 1",
		Content: "Hello World",
		Metadata: map[string]string{
			"author": "Alice",
		},
	}

	// 2. Keep a history of patches for "Undo" functionality.
	var history []deep.Patch[Document]

	// 3. User makes an edit: Change title and content.
	fmt.Println("Action 1: Edit title and content")
	original := deep.MustCopy(doc) // Snapshot current state
	doc.Title = "Final Version"
	doc.Content = "Goodbye World"

	// Record the change in history
	history = append(history, deep.MustDiff(original, doc))

	// 4. User makes another edit: Add metadata.
	fmt.Println("Action 2: Add metadata")
	original = deep.MustCopy(doc)
	doc.Metadata["tags"] = "go,library"

	history = append(history, deep.MustDiff(original, doc))

	fmt.Printf("\nCurrent State: %+v\n", doc)

	// 5. UNDO!
	// To undo, we take the last patch and REVERSE it.
	fmt.Println("\n--- UNDO ACTION 2 ---")
	lastPatch := history[len(history)-1]
	undoPatch := lastPatch.Reverse()
	undoPatch.Apply(&doc)

	fmt.Printf("After Undo 2: %+v\n", doc)

	// 6. UNDO again!
	fmt.Println("\n--- UNDO ACTION 1 ---")
	firstPatch := history[len(history)-2]
	undoFirstPatch := firstPatch.Reverse()
	undoFirstPatch.Apply(&doc)

	fmt.Printf("After Undo 1: %+v\n", doc)

	// Notice we are back to the initial state!
}
