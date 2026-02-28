package main

import (
	"fmt"
	"github.com/brunoga/deep/v5"
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

	history := []DocState{v5.Copy(current)}

	// 1. Edit
	current.Title = "Final Version"
	current.Content = "Goodbye World"
	history = append(history, v5.Copy(current))

	// 2. Add metadata
	current.Metadata["tags"] = "go,library"
	history = append(history, v5.Copy(current))

	fmt.Printf("Current State: %+v\n", current)

	// Undo Action 2
	current = v5.Copy(history[1])
	fmt.Printf("After Undo 2: %+v\n", current)

	// Undo Action 1
	current = v5.Copy(history[0])
	fmt.Printf("After Undo 1: %+v\n", current)
}
