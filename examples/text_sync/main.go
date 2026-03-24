package main

import (
	"fmt"

	"github.com/brunoga/deep/v5/crdt"
	"github.com/brunoga/deep/v5/crdt/hlc"
)

func main() {
	clockA := hlc.NewClock("node-a")
	clockB := hlc.NewClock("node-b")

	// Two nodes start with the same empty document.
	docA := crdt.Text{}
	docB := crdt.Text{}

	// --- Step 1: Node A inserts "Hello" ---
	docA = docA.Insert(0, "Hello", clockA)

	// Propagate A's full state to B via MergeTextRuns.
	docB = crdt.MergeTextRuns(docB, docA)

	fmt.Println("After A types 'Hello':")
	fmt.Printf("  Doc A: %q\n", docA.String())
	fmt.Printf("  Doc B: %q\n", docB.String())

	// --- Step 2: Concurrent edits (network partition) ---
	// A appends " World"
	docA = docA.Insert(5, " World", clockA)

	// B inserts "!" at position 5 (after "Hello")
	docB = docB.Insert(5, "!", clockB)

	fmt.Println("\nAfter concurrent edits (partition):")
	fmt.Printf("  Doc A: %q\n", docA.String())
	fmt.Printf("  Doc B: %q\n", docB.String())

	// --- Step 3: Merge (partition heals) ---
	mergedA := crdt.MergeTextRuns(docA, docB)
	mergedB := crdt.MergeTextRuns(docB, docA)

	fmt.Println("\nAfter merge:")
	fmt.Printf("  Doc A: %q\n", mergedA.String())
	fmt.Printf("  Doc B: %q\n", mergedB.String())

	if mergedA.String() == mergedB.String() {
		fmt.Println("\nSUCCESS: both nodes converged.")
	} else {
		fmt.Println("\nFAILURE: nodes diverged!")
	}
}
