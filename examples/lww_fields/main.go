package main

import (
	"fmt"

	"github.com/brunoga/deep/v5/crdt"
	"github.com/brunoga/deep/v5/crdt/hlc"
)

// Profile uses per-field LWW[T] registers so that concurrent updates to
// independent fields can always be merged: the later timestamp wins.
type Profile struct {
	Name  crdt.LWW[string] `json:"name"`
	Score crdt.LWW[int]    `json:"score"`
}

func main() {
	clock := hlc.NewClock("server")
	ts0 := clock.Now()

	base := Profile{
		Name:  crdt.LWW[string]{Value: "Alice", Timestamp: ts0},
		Score: crdt.LWW[int]{Value: 0, Timestamp: ts0},
	}

	// Client A renames the profile; apply via LWW.Set which only accepts
	// the update if the incoming timestamp is strictly newer.
	tsA := clock.Now()
	profileA := base
	profileA.Name.Set("Alice Smith", tsA)

	// Client B increments the score concurrently.
	tsB := clock.Now()
	profileB := base
	profileB.Score.Set(42, tsB)

	fmt.Println("--- CONCURRENT EDITS ---")
	fmt.Printf("Client A: name  → %q\n", profileA.Name.Value)
	fmt.Printf("Client B: score → %d\n", profileB.Score.Value)

	// Manual merge: take the newer value for each field.
	merged := base
	merged.Name.Set(profileA.Name.Value, profileA.Name.Timestamp)
	merged.Score.Set(profileB.Score.Value, profileB.Score.Timestamp)

	fmt.Println("\n--- CONVERGED RESULT ---")
	fmt.Printf("Name:  %s\n", merged.Name.Value)
	fmt.Printf("Score: %d\n", merged.Score.Value)
}
