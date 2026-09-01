package main

import (
	"fmt"
	"math/rand"
	"time"

	"github.com/brunoga/deep/v5/crdt"
	"github.com/brunoga/deep/v5/crdt/hlc"
)

// crdt.Document holds the same runs as crdt.Text, but keeps them in a tree
// ordered by position instead of a plain slice. Finding a position and editing
// there costs the same whether the document holds a hundred runs or ten
// thousand, where a Text takes proportionally longer as edits scatter runs
// around.
//
// Both describe the same document and serialize the same way, so a replica
// running one converges with a replica running the other.
func main() {
	alice := crdt.NewDocument(hlc.NewClock("alice"))
	bob := crdt.NewDocument(hlc.NewClock("bob"))

	alice.Insert(0, "The quick brown fox.")
	bob.MergeFrom(alice.Text())

	fmt.Println("--- SHARED DOCUMENT ---")
	fmt.Printf("  %q\n", alice.String())

	// They edit the same sentence while apart.
	alice.Insert(10, "very ")
	bob.Delete(4, 6) // "quick "
	bob.Insert(bob.Len(), " Then it slept")

	fmt.Println("\n--- EDITED APART ---")
	fmt.Printf("  alice: %q\n", alice.String())
	fmt.Printf("  bob:   %q\n", bob.String())

	// Each merges the other's runs; both land in the same place.
	aliceRuns, bobRuns := alice.Text(), bob.Text()
	alice.MergeFrom(bobRuns)
	bob.MergeFrom(aliceRuns)

	fmt.Println("\n--- AFTER SYNC ---")
	fmt.Printf("  alice: %q\n", alice.String())
	fmt.Printf("  bob:   %q\n", bob.String())
	fmt.Printf("  converged: %v\n", alice.String() == bob.String())

	// Why the tree: editing stays cheap as a document fragments.
	fmt.Println("\n--- COST OF AN EDIT, AS RUNS ACCUMULATE ---")
	for _, runs := range []int{500, 4000, 16000} {
		doc := crdt.NewDocument(hlc.NewClock("a"))
		doc.Insert(0, "seed")
		for i := 0; i < runs; i++ {
			doc.Insert(0, "x") // prepending leaves a run behind each time
		}
		start := time.Now()
		for i := 0; i < 100; i++ {
			doc.Insert(doc.Len(), "y")
		}
		fmt.Printf("  %5d runs: %6s per edit\n", runs, (time.Since(start) / 100).Round(time.Nanosecond))
	}

	// Scattered editing of a large document.
	rng := rand.New(rand.NewSource(1))
	doc := crdt.NewDocument(hlc.NewClock("a"))
	doc.Insert(0, "seed")
	start := time.Now()
	for i := 0; i < 20000; i++ {
		doc.Insert(rng.Intn(doc.Len()+1), "z")
	}
	fmt.Printf("\n20,000 insertions at random positions: %s (%s each)\n",
		time.Since(start).Round(time.Millisecond), (time.Since(start) / 20000).Round(time.Nanosecond))
	fmt.Printf("resulting document: %d characters, %d runs\n", doc.Len(), len(doc.Text()))
}
