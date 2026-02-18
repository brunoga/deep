package main

import (
	"fmt"
	"github.com/brunoga/deep/v4"
)

type Document struct {
	Title   string
	Content string
}

type Workspace struct {
	Drafts  []Document
	Archive map[string]Document
}

func main() {
	// 1. Initial state: A document in Drafts
	doc := Document{
		Title:   "Breaking Changes v2",
		Content: "Standardize on JSON Pointers and add Differ object...",
	}

	ws := Workspace{
		Drafts:  []Document{doc},
		Archive: make(map[string]Document),
	}

	fmt.Println("--- INITIAL WORKSPACE ---")
	fmt.Printf("Drafts: %d, Archive: %d\n\n", len(ws.Drafts), len(ws.Archive))

	// 2. Target state: Move the document from Drafts to Archive
	target := Workspace{
		Drafts: []Document{},
		Archive: map[string]Document{
			"v2-release": doc,
		},
	}

	// 3. Generate Patch
	// The Differ will index 'ws' and find 'doc' at '/Drafts/0'
	// When it sees 'doc' at '/Archive/v2-release' in 'target', it emits a Copy.
	patch := deep.MustDiff(ws, target, deep.DiffDetectMoves(true))

	fmt.Println("--- GENERATED PATCH SUMMARY ---")
	fmt.Println(patch.Summary())
	fmt.Println()

	// 4. Verify semantic efficiency
	fmt.Println("--- PATCH OPERATIONS (Walk) ---")
	err := patch.Walk(func(path string, op deep.OpKind, old, new any) error {
		if op == deep.OpCopy {
			fmt.Printf("[%s] Op: %s, From: %v\n", path, op, old)
		} else {
			fmt.Printf("[%s] Op: %s\n", path, op)
		}
		return nil
	})
	if err != nil {
		fmt.Printf("Walk failed: %v\n", err)
		return
	}
	fmt.Println()

	// 5. Apply and Verify
	final, err := deep.Copy(ws)
	if err != nil {
		fmt.Printf("Copy failed: %v\n", err)
		return
	}
	err = patch.ApplyChecked(&final)
	if err != nil {
		fmt.Printf("Apply failed: %v\n", err)
		return
	}

	fmt.Println("--- FINAL WORKSPACE ---")
	fmt.Printf("Drafts: %d, Archive: %d\n", len(final.Drafts), len(final.Archive))
	fmt.Printf("Archived Doc: %s\n", final.Archive["v2-release"].Title)
}
