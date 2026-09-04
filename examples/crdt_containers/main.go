package main

import (
	"fmt"
	"sort"

	"github.com/brunoga/deep/v6/crdt"
)

// Counter, Set and Map are standalone convergent containers. Each replica edits
// locally and merges whenever it can reach a peer; Merge is commutative and
// idempotent, so replicas converge regardless of message order or duplicates.
func main() {
	// --- Counter: concurrent increments all survive ---
	cartA := crdt.NewCounter("node-a")
	cartB := crdt.NewCounter("node-b")

	cartA.Increment(3)
	cartB.Increment(5)
	cartB.Decrement(1)

	cartA.Merge(cartB)
	cartB.Merge(cartA)

	fmt.Println("--- COUNTER ---")
	fmt.Printf("A added 3; B added 5 then removed 1\n")
	fmt.Printf("A=%d B=%d (no increment lost)\n", cartA.Value(), cartB.Value())

	// --- Set: add-wins semantics ---
	tagsA := crdt.NewSet[string]("node-a")
	tagsB := crdt.NewSet[string]("node-b")

	tagsA.Add("urgent")
	tagsA.Add("bug")
	tagsB.Merge(tagsA) // B learns about both tags

	// Now they diverge: A removes "bug" while B concurrently re-adds it.
	tagsA.Remove("bug")
	tagsB.Add("bug")

	tagsA.Merge(tagsB)
	tagsB.Merge(tagsA)

	fmt.Println("\n--- SET (add-wins) ---")
	fmt.Printf("A removed \"bug\" while B concurrently re-added it\n")
	fmt.Printf("A=%v B=%v (the concurrent add wins)\n", sorted(tagsA.Items()), sorted(tagsB.Items()))

	// --- Map: last-write-wins per key ---
	prefsA := crdt.NewMap[string, string]("node-a")
	prefsB := crdt.NewMap[string, string]("node-b")

	prefsA.Set("theme", "dark")
	prefsB.Set("lang", "pt-BR")
	prefsB.Set("theme", "light") // contested key, written later

	prefsA.Merge(prefsB)
	prefsB.Merge(prefsA)

	theme, _ := prefsA.Get("theme")
	lang, _ := prefsA.Get("lang")

	fmt.Println("\n--- MAP (last-write-wins per key) ---")
	fmt.Printf("A set theme=dark; B set lang=pt-BR and theme=light\n")
	fmt.Printf("theme=%q lang=%q keys=%d (both replicas agree: %v)\n",
		theme, lang, prefsA.Len(), prefsA.Len() == prefsB.Len())
}

func sorted(s []string) []string {
	sort.Strings(s)
	return s
}
