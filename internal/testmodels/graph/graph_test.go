package graph

import (
	"testing"

	deep "github.com/brunoga/deep/v6"
)

// The parity tests in the parent package check that these types behave the same
// under the generated methods and the reflection engine. These check what that
// behaviour actually is, so that the guarantee is written down rather than
// implied by an agreement between two implementations.

func TestCloneRebuildsASelfCycle(t *testing.T) {
	n := &Node{Name: "n"}
	n.Next = n

	c := n.Clone()

	if c == n {
		t.Fatal("Clone returned the original")
	}
	if c.Next != c {
		t.Error("the cycle in the copy does not close on the copy")
	}
	if c.Next == n {
		t.Error("the copy still points at the original")
	}
}

func TestCloneRebuildsALongerCycle(t *testing.T) {
	a := &Node{Name: "a"}
	b := &Node{Name: "b", Next: a}
	a.Next = b

	c := a.Clone()

	if c.Next.Name != "b" || c.Next.Next != c {
		t.Errorf("two-node cycle not rebuilt: %q -> %q -> closes on start: %v",
			c.Name, c.Next.Name, c.Next.Next == c)
	}
	if c.Next == b {
		t.Error("the copy shares a node with the original")
	}
}

func TestCloneCopiesASharedNodeOnce(t *testing.T) {
	shared := &Node{Name: "shared"}
	n := &Node{
		Name:     "root",
		Next:     shared,
		Peers:    []*Node{shared, shared},
		Children: map[string]*Node{"a": shared, "b": shared},
	}

	c := n.Clone()

	// One node in, one node out — reached by four routes in both.
	for _, got := range []*Node{c.Peers[0], c.Peers[1], c.Children["a"], c.Children["b"]} {
		if got != c.Next {
			t.Error("a route to the shared node reached a different copy of it")
		}
	}
	if c.Next == shared {
		t.Error("the copy shares the node with the original")
	}
}

func TestCloneRebuildsCyclesThroughValueStructs(t *testing.T) {
	// The cycle runs through Edge, which is a value in a slice and in a map.
	n := &Node{Name: "n"}
	n.Edges = []Edge{{Weight: 1, To: n}}
	n.Links = map[string]Edge{"back": {Weight: 2, To: n}}

	c := n.Clone()

	if c.Edges[0].To != c {
		t.Error("the cycle through the slice of Edge does not close on the copy")
	}
	if c.Links["back"].To != c {
		t.Error("the cycle through the map of Edge does not close on the copy")
	}
	if c.Edges[0].To == n || c.Links["back"].To == n {
		t.Error("the copy still points at the original")
	}
}

func TestClonePreservesNilVersusEmpty(t *testing.T) {
	empty := &Node{
		Name:     "empty",
		Peers:    []*Node{},
		Children: map[string]*Node{},
		Tags:     map[string]string{},
		Scores:   []int{},
	}
	nils := &Node{Name: "nil"}

	ec := empty.Clone()
	if ec.Peers == nil || ec.Children == nil || ec.Tags == nil || ec.Scores == nil {
		t.Error("an empty collection cloned to nil")
	}
	nc := nils.Clone()
	if nc.Peers != nil || nc.Children != nil || nc.Tags != nil || nc.Scores != nil {
		t.Error("a nil collection cloned to an empty one")
	}
	// The distinction is not academic: the reflection engine compares on it.
	if deep.Equal(*empty, *nils) {
		t.Error("empty and nil collections should not compare equal")
	}
}

func TestEqualTerminatesOnCyclicValues(t *testing.T) {
	mk := func(name string) *Node {
		n := &Node{Name: name}
		n.Next = n
		n.Peers = []*Node{n}
		n.Children = map[string]*Node{"self": n}
		return n
	}

	if a, b := mk("same"), mk("same"); !a.Equal(b) {
		t.Error("two values with the same cyclic shape should be equal")
	}
	if a, b := mk("one"), mk("two"); a.Equal(b) {
		t.Error("cyclic values differing in a field should not be equal")
	}

	// A difference buried one step below the cycle still has to be found.
	a, b := mk("same"), mk("same")
	a.Next.Label.Rank = 1
	if a.Equal(b) {
		t.Error("a difference reached through the cycle was missed")
	}
}

func TestDiffTerminatesOnCyclicValues(t *testing.T) {
	a := &Node{Name: "a"}
	a.Next = a
	b := &Node{Name: "b"}
	b.Next = b

	p := a.Diff(b)
	if len(p.Operations) == 0 {
		t.Fatal("no operations for two differing cyclic values")
	}
	if err := deep.Apply(a, p); err != nil {
		t.Fatalf("applying: %v", err)
	}
	if a.Name != "b" {
		t.Errorf("Name = %q, want %q", a.Name, "b")
	}
	if a.Next != a {
		t.Error("applying the patch broke the cycle")
	}
}

func TestDiffAndApplyUpdateEveryRouteToASharedValue(t *testing.T) {
	// A pair is diffed once, at the first path that reaches it; each later
	// route becomes one alias operation. Applying the patch must land the right
	// value at every route AND rebuild the sharing — whether the target still
	// shares the node (a clone) or was rebuilt without sharing.
	oldShared := &Node{Name: "old"}
	a := &Node{Name: "root", Next: oldShared, Children: map[string]*Node{"c": oldShared}}

	newShared := &Node{Name: "new"}
	b := &Node{Name: "root", Next: newShared, Children: map[string]*Node{"c": newShared}}

	p := a.Diff(b)

	check := func(t *testing.T, got *Node) {
		t.Helper()
		if got.Next.Name != "new" {
			t.Errorf("Next.Name = %q, want %q", got.Next.Name, "new")
		}
		if got.Children["c"].Name != "new" {
			t.Errorf("Children[c].Name = %q, want %q — the second route was not updated",
				got.Children["c"].Name, "new")
		}
		if got.Next != got.Children["c"] {
			t.Error("the two routes no longer share one node")
		}
	}

	t.Run("clone target", func(t *testing.T) {
		got := a.Clone()
		if err := deep.Apply(got, p); err != nil {
			t.Fatalf("applying: %v", err)
		}
		check(t, got)
	})

	t.Run("rebuilt target", func(t *testing.T) {
		// Equal to a, but each route holds its own node — the shape a JSON
		// decode would produce.
		got := &Node{
			Name:     "root",
			Next:     &Node{Name: "old"},
			Children: map[string]*Node{"c": {Name: "old"}},
		}
		if err := deep.Apply(got, p); err != nil {
			t.Fatalf("applying: %v", err)
		}
		check(t, got)
	})
}

func TestDiffIsLinearOnSharedStructure(t *testing.T) {
	// Stacked diamonds: both pointers at every level lead to the same next
	// node, so the number of paths to the leaf is 2^depth while the number of
	// nodes is depth+1. The diff must be linear in nodes, not paths.
	chain := func(depth, leaf int) *Node {
		cur := &Node{Name: "leaf", Label: Label{Rank: leaf}}
		for i := depth; i > 0; i-- {
			cur = &Node{Name: "lvl", Peers: []*Node{cur}, Next: cur}
		}
		return cur
	}
	const depth = 18
	a, b := chain(depth, 1), chain(depth, 2)

	p := a.Diff(b)
	if len(p.Operations) > 2*depth+2 {
		t.Fatalf("got %d operations for %d nodes — route enumeration is back",
			len(p.Operations), depth+1)
	}

	got := a.Clone()
	if err := deep.Apply(got, p); err != nil {
		t.Fatalf("applying: %v", err)
	}
	if !deep.Equal(*got, *b) {
		t.Error("patch did not reach b")
	}
	if got.Next != got.Peers[0] {
		t.Error("diamond sharing lost after apply")
	}
}

func TestAliasReverseRestoresTheDestination(t *testing.T) {
	oldShared := &Node{Name: "old"}
	a := &Node{Name: "root", Next: oldShared, Children: map[string]*Node{"c": oldShared}}
	newShared := &Node{Name: "new"}
	b := &Node{Name: "root", Next: newShared, Children: map[string]*Node{"c": newShared}}

	p := a.Diff(b)
	got := b.Clone()
	if err := deep.Apply(got, p.Reverse()); err != nil {
		t.Fatalf("applying reverse: %v", err)
	}
	if got.Next.Name != "old" || got.Children["c"].Name != "old" {
		t.Errorf("reverse landed on %q / %q, want old / old",
			got.Next.Name, got.Children["c"].Name)
	}
}

func TestNonRecursiveTypeIsUnaffected(t *testing.T) {
	// Label cannot reach a cycle, so it keeps the plain generated methods. It
	// still has to work.
	l := &Label{Text: "t", Rank: 1}
	c := l.Clone()
	if c == l || !l.Equal(c) {
		t.Error("Label.Clone did not produce an equal, separate value")
	}
	if p := l.Diff(&Label{Text: "t", Rank: 2}); len(p.Operations) != 1 {
		t.Errorf("got %d operations, want 1", len(p.Operations))
	}
}
