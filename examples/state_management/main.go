package main

import (
	"fmt"
	"log"

	v5 "github.com/brunoga/deep/v5"
)

type DocState struct {
	Title    string            `json:"title"`
	Content  string            `json:"content"`
	Metadata map[string]string `json:"metadata"`
}

func main() {
	current := DocState{
		Title:    "Draft 1",
		Content:  "Hello World",
		Metadata: map[string]string{"author": "Alice"},
	}

	// Each edit records a reverse patch for undo.
	var undoStack []v5.Patch[DocState]

	edit := func(fn func(*DocState)) {
		next := v5.Copy(current)
		fn(&next)
		patch, err := v5.Diff(current, next)
		if err != nil {
			log.Fatal(err)
		}
		undoStack = append(undoStack, patch.Reverse())
		current = next
	}

	edit(func(d *DocState) {
		d.Title = "Final Version"
		d.Content = "Goodbye World"
	})

	edit(func(d *DocState) {
		d.Metadata["tags"] = "go,library"
	})

	fmt.Println("--- CURRENT STATE ---")
	fmt.Printf("%+v\n", current)

	// Undo edit 2.
	v5.Apply(&current, undoStack[len(undoStack)-1])
	undoStack = undoStack[:len(undoStack)-1]
	fmt.Println("\n--- AFTER UNDO (edit 2) ---")
	fmt.Printf("%+v\n", current)

	// Undo edit 1.
	v5.Apply(&current, undoStack[len(undoStack)-1])
	undoStack = undoStack[:len(undoStack)-1]
	fmt.Println("\n--- AFTER UNDO (edit 1) ---")
	fmt.Printf("%+v\n", current)
}
