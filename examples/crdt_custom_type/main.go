package main

import (
	"fmt"
	"sort"

	"github.com/brunoga/deep/v6/crdt"
	"github.com/brunoga/deep/v6/crdt/hlc"
)

// A field inside a CRDT normally merges by last-write-wins: two replicas
// writing it concurrently means one version wins and the other is dropped. A
// type that knows how to combine two of itself can say so by implementing
// crdt.Convergent, and the replica will merge it instead of choosing.
//
// Tags is a grow-only set. Merging two of them is their union, which is
// commutative, associative and idempotent — merge in any order, any number of
// times, and every replica lands on the same set. That is what Convergent asks
// for, and it is the whole contract.
type Tags []string

// MergeFrom implements crdt.Convergent.
func (t Tags) MergeFrom(other any) any {
	o, ok := other.(Tags)
	if !ok {
		return t
	}
	seen := make(map[string]bool, len(t)+len(o))
	var union Tags
	for _, s := range append(append(Tags{}, t...), o...) {
		if !seen[s] {
			seen[s] = true
			union = append(union, s)
		}
	}
	sort.Strings(union)
	return union
}

// Retired records tags that have been dropped, with when. Keeping them is what
// stops a replica that has not heard about a removal from reviving the tag; the
// entries can go once every replica has seen the removal, which is what
// crdt.Compactable is for.
type Retired map[string]hlc.HLC

// MergeFrom implements crdt.Convergent: a tag is retired if either side says so.
func (r Retired) MergeFrom(other any) any {
	o, ok := other.(Retired)
	if !ok {
		return r
	}
	union := make(Retired, len(r)+len(o))
	for k, v := range r {
		union[k] = v
	}
	for k, v := range o {
		if existing, ok := union[k]; !ok || v.After(existing) {
			union[k] = v
		}
	}
	return union
}

// CompactBefore implements crdt.Compactable.
func (r Retired) CompactBefore(before hlc.HLC) any {
	kept := make(Retired, len(r))
	for k, v := range r {
		if v.After(before) {
			kept[k] = v
		}
	}
	return kept
}

type Article struct {
	Title   string  `json:"title"`
	Tags    Tags    `json:"tags"`
	Retired Retired `json:"retired"`
}

func main() {
	alice := crdt.NewCRDT(Article{Retired: Retired{}}, "alice")
	bob := crdt.NewCRDT(Article{Retired: Retired{}}, "bob")

	seed := alice.Edit(func(a *Article) { a.Title = "Convergent types" })
	bob.ApplyDelta(seed)

	// Both add tags while apart, and both write the title.
	fromAlice := alice.Edit(func(a *Article) {
		a.Tags = Tags{"go", "crdt"}
		a.Title = "Alice's title"
	})
	fromBob := bob.Edit(func(a *Article) {
		a.Tags = Tags{"crdt", "distributed"}
		a.Title = "Bob's title"
	})

	alice.ApplyDelta(fromBob)
	bob.ApplyDelta(fromAlice)

	fmt.Println("--- AFTER SYNC ---")
	fmt.Printf("  tags:  %v\n", alice.View().Tags)
	fmt.Printf("  title: %q\n", alice.View().Title)
	fmt.Printf("  converged: %v\n", fmt.Sprint(alice.View()) == fmt.Sprint(bob.View()))
	fmt.Println("\nThe tags merged because Tags says how to combine two of itself.")
	fmt.Println("The title is an ordinary string, so one writer's version won.")

	// Retired implements Compactable as well, so Compact reaches it.
	drop := alice.Edit(func(a *Article) {
		a.Retired["obsolete"] = alice.Clock().Now()
		a.Retired["old"] = alice.Clock().Now()
	})
	bob.ApplyDelta(drop)

	fmt.Printf("\n--- BEFORE COMPACTION ---\n  retired entries: %d\n", len(alice.View().Retired))

	// Everything above has reached both replicas, so it is safe to forget.
	alice.Compact(alice.Clock().Now())

	fmt.Printf("\n--- AFTER COMPACTION ---\n  retired entries: %d, tags still %v\n",
		len(alice.View().Retired), alice.View().Tags)
	fmt.Println("\nCompact reached inside the value and asked Retired to drop what")
	fmt.Println("every replica had already seen, leaving the rest untouched.")
}
