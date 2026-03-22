package main

import (
	"fmt"

	"github.com/brunoga/deep/v5"
	"github.com/brunoga/deep/v5/crdt/hlc"
)

// Profile uses per-field LWW[T] registers so that concurrent updates to
// independent fields can always be merged: the later timestamp wins.
type Profile struct {
	Name  deep.LWW[string] `json:"name"`
	Score deep.LWW[int]    `json:"score"`
}

func main() {
	clock := hlc.NewClock("server")
	ts0 := clock.Now()

	base := Profile{
		Name:  deep.LWW[string]{Value: "Alice", Timestamp: ts0},
		Score: deep.LWW[int]{Value: 0, Timestamp: ts0},
	}

	// Client A renames the profile (earlier timestamp).
	tsA := clock.Now()
	patchA := deep.Patch[Profile]{}
	patchA.Operations = append(patchA.Operations, deep.Operation{
		Kind:      deep.OpReplace,
		Path:      "/name",
		New:       deep.LWW[string]{Value: "Alice Smith", Timestamp: tsA},
		Timestamp: &tsA,
	})

	// Client B increments the score concurrently (later timestamp).
	tsB := clock.Now()
	patchB := deep.Patch[Profile]{}
	patchB.Operations = append(patchB.Operations, deep.Operation{
		Kind:      deep.OpReplace,
		Path:      "/score",
		New:       deep.LWW[int]{Value: 42, Timestamp: tsB},
		Timestamp: &tsB,
	})

	fmt.Println("--- CONCURRENT EDITS ---")
	fmt.Printf("Client A: name  → %q\n", "Alice Smith")
	fmt.Printf("Client B: score → %d\n", 42)

	// Merge both patches: non-conflicting fields are combined;
	// if both touched the same field, the later HLC timestamp would win.
	merged := deep.Merge(patchA, patchB, nil)
	result := base
	deep.Apply(&result, merged)

	fmt.Println("\n--- CONVERGED RESULT ---")
	fmt.Printf("Name:  %s\n", result.Name.Value)
	fmt.Printf("Score: %d\n", result.Score.Value)
}
