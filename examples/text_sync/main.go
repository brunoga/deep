package main

import (
	"fmt"
	v5 "github.com/brunoga/deep/v5"
	"github.com/brunoga/deep/v5/crdt"
	"github.com/brunoga/deep/v5/crdt/hlc"
)

func main() {
	clockA := hlc.NewClock("node-a")
	clockB := hlc.NewClock("node-b")

	// Text CRDT requires HLC for operations
	// Since Text is a specialized type, we use it directly or in structs
	docA := crdt.Text{}
	docB := crdt.Text{}

	fmt.Println("--- Initial State: Empty ---")

	// 1. A types 'Hello'
	// (Using v4-like Insert but adapted for v5 concept)
	// For this prototype example, we'll manually create the Text state
	docA = crdt.Text{{ID: clockA.Now(), Value: "Hello"}}

	// Sync A -> B
	patchA := v5.Diff(crdt.Text{}, docA)
	v5.Apply(&docB, patchA)

	fmt.Printf("Doc A: %s\n", docA.String())
	fmt.Printf("Doc B: %s\n", docB.String())

	// 2. Concurrent Edits
	// A appends ' World'
	tsA := clockA.Now()
	docA = append(docA, crdt.TextRun{ID: tsA, Value: " World", Prev: docA[0].ID})

	// B inserts '!'
	tsB := clockB.Now()
	docB = append(docB, crdt.TextRun{ID: tsB, Value: "!", Prev: docB[0].ID})

	fmt.Println("\n--- Concurrent Edits ---")

	// Diff and Merge
	pA := v5.Diff(crdt.Text{}, docA)
	pB := v5.Diff(crdt.Text{}, docB)

	// In v5, we apply both patches to reach convergence
	v5.Apply(&docA, pB)
	v5.Apply(&docB, pA)

	fmt.Printf("Doc A: %s\n", docA.String())
	fmt.Printf("Doc B: %s\n", docB.String())

	if docA.String() == docB.String() {
		fmt.Println("SUCCESS: Collaborative text converged!")
	}
}
