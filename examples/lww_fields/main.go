package main

import (
	"fmt"

	v5 "github.com/brunoga/deep/v5"
	"github.com/brunoga/deep/v5/crdt/hlc"
)

// Profile uses per-field LWW[T] registers so that concurrent updates to
// independent fields can always be merged: the later timestamp wins.
type Profile struct {
	Name  v5.LWW[string] `json:"name"`
	Score v5.LWW[int]    `json:"score"`
}

func main() {
	clock := hlc.NewClock("server")
	ts0 := clock.Now()

	base := Profile{
		Name:  v5.LWW[string]{Value: "Alice", Timestamp: ts0},
		Score: v5.LWW[int]{Value: 0, Timestamp: ts0},
	}

	// Client A renames the profile (earlier timestamp).
	tsA := clock.Now()
	patchA := v5.NewPatch[Profile]()
	patchA.Operations = append(patchA.Operations, v5.Operation{
		Kind:      v5.OpReplace,
		Path:      "/name",
		New:       v5.LWW[string]{Value: "Alice Smith", Timestamp: tsA},
		Timestamp: &tsA,
	})

	// Client B increments the score concurrently (later timestamp).
	tsB := clock.Now()
	patchB := v5.NewPatch[Profile]()
	patchB.Operations = append(patchB.Operations, v5.Operation{
		Kind:      v5.OpReplace,
		Path:      "/score",
		New:       v5.LWW[int]{Value: 42, Timestamp: tsB},
		Timestamp: &tsB,
	})

	fmt.Println("--- CONCURRENT EDITS ---")
	fmt.Printf("Client A: name  → %q\n", "Alice Smith")
	fmt.Printf("Client B: score → %d\n", 42)

	// Merge both patches: non-conflicting fields are combined;
	// if both touched the same field, the later HLC timestamp would win.
	merged := v5.Merge(patchA, patchB, nil)
	result := base
	v5.Apply(&result, merged)

	fmt.Println("\n--- CONVERGED RESULT ---")
	fmt.Printf("Name:  %s\n", result.Name.Value)
	fmt.Printf("Score: %d\n", result.Score.Value)
}
