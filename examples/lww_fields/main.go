package main

import (
	"fmt"

	"github.com/brunoga/deep/v6/crdt"
	"github.com/brunoga/deep/v6/crdt/hlc"
)

// Profile uses per-field LWW[T] registers. Each register carries the timestamp
// of its last write, so two replicas can merge field by field without a central
// coordinator: for every field, the write with the newer timestamp wins.
type Profile struct {
	Name  crdt.LWW[string] `json:"name"`
	Score crdt.LWW[int]    `json:"score"`
}

// merge resolves two replicas field by field. LWW.Set only accepts a value
// whose timestamp is strictly newer, so merging is commutative — the result
// does not depend on which replica is merged into which.
func merge(dst, src Profile) Profile {
	dst.Name.Set(src.Name.Value, src.Name.Timestamp)
	dst.Score.Set(src.Score.Value, src.Score.Timestamp)
	return dst
}

func main() {
	clock := hlc.NewClock("server")
	base := Profile{
		Name:  crdt.LWW[string]{Value: "Alice", Timestamp: clock.Now()},
		Score: crdt.LWW[int]{Value: 0, Timestamp: clock.Now()},
	}

	// Replica A and replica B both start from the same base and go offline.
	replicaA, replicaB := base, base

	// A writes the score first...
	replicaA.Score.Set(10, clock.Now())

	// ...then B writes the SAME field with a later timestamp — a genuine
	// conflict — and also renames the profile, which A never touched.
	replicaB.Score.Set(99, clock.Now())
	replicaB.Name.Set("Alice Smith", clock.Now())

	fmt.Println("--- DIVERGED REPLICAS ---")
	fmt.Printf("A: name=%q score=%d\n", replicaA.Name.Value, replicaA.Score.Value)
	fmt.Printf("B: name=%q score=%d\n", replicaB.Name.Value, replicaB.Score.Value)

	// Merge in both directions; both replicas must land on the same value.
	mergedA := merge(replicaA, replicaB)
	mergedB := merge(replicaB, replicaA)

	fmt.Println("\n--- AFTER MERGE ---")
	fmt.Printf("A: name=%q score=%d\n", mergedA.Name.Value, mergedA.Score.Value)
	fmt.Printf("B: name=%q score=%d\n", mergedB.Name.Value, mergedB.Score.Value)

	converged := mergedA.Name.Value == mergedB.Name.Value &&
		mergedA.Score.Value == mergedB.Score.Value
	fmt.Printf("\nConverged: %v\n", converged)
	fmt.Println("The contested score resolves to B's write (later timestamp);")
	fmt.Println("the uncontested rename survives regardless of merge order.")
}
