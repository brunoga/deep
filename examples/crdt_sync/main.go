package main

import (
	"encoding/json"
	"fmt"
	"github.com/brunoga/deep/v5"
	"github.com/brunoga/deep/v5/crdt/hlc"
)

type SharedState struct {
	Title   string            `json:"title"`
	Options map[string]string `json:"options"`
}

func main() {
	clockA := hlc.NewClock("node-a")
	clockB := hlc.NewClock("node-b")

	initial := SharedState{
		Title:   "Initial",
		Options: map[string]string{"theme": "light"},
	}

	// 1. Node A Edit
	tsA := clockA.Now()
	patchA := v5.NewPatch[SharedState]()
	patchA.Operations = append(patchA.Operations, v5.Operation{
		Kind: v5.OpReplace, Path: "/title", New: "Title by A", Timestamp: tsA,
	})

	// 2. Node B Edit (Concurrent)
	tsB := clockB.Now()
	patchB := v5.NewPatch[SharedState]()
	patchB.Operations = append(patchB.Operations, v5.Operation{
		Kind: v5.OpReplace, Path: "/title", New: "Title by B", Timestamp: tsB,
	})
	patchB.Operations = append(patchB.Operations, v5.Operation{
		Kind: v5.OpReplace, Path: "/options/font", New: "mono", Timestamp: tsB,
	})

	// 3. Convergent Merge
	merged := v5.Merge(patchA, patchB, nil)

	fmt.Println("--- Synchronizing Node A and Node B ---")

	stateA := initial
	v5.Apply(&stateA, merged)

	stateB := initial
	v5.Apply(&stateB, merged)

	out, _ := json.MarshalIndent(stateA, "", "  ")
	fmt.Println(string(out))

	if stateA.Title == stateB.Title {
		fmt.Println("SUCCESS: Both nodes converged!")
	}
}
