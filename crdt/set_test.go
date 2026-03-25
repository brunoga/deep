package crdt

import (
	"sort"
	"testing"
)

func TestSet_AddContains(t *testing.T) {
	s := NewSet[string]("node-a")
	s.Add("a")
	s.Add("b")
	s.Add("c")

	for _, elem := range []string{"a", "b", "c"} {
		if !s.Contains(elem) {
			t.Errorf("expected Contains(%q) == true", elem)
		}
	}
	if s.Contains("d") {
		t.Error("expected Contains(\"d\") == false")
	}
}

func TestSet_Remove(t *testing.T) {
	s := NewSet[string]("node-a")
	s.Add("x")
	s.Remove("x")
	if s.Contains("x") {
		t.Error("expected Contains(\"x\") == false after Remove")
	}
}

func TestSet_ReAddAfterRemove(t *testing.T) {
	s := NewSet[string]("node-a")
	s.Add("x")
	s.Remove("x")
	s.Add("x")
	if !s.Contains("x") {
		t.Error("expected Contains(\"x\") == true after re-Add")
	}
}

func TestSet_Items(t *testing.T) {
	s := NewSet[string]("node-a")
	s.Add("alpha")
	s.Add("beta")
	s.Add("gamma")
	if len(s.Items()) != 3 {
		t.Errorf("expected 3 items, got %d", len(s.Items()))
	}
}

func TestSet_DuplicateAdd(t *testing.T) {
	s := NewSet[string]("node-a")
	s.Add("dup")
	s.Add("dup")

	items := s.Items()
	if len(items) != 1 {
		t.Errorf("expected 1 distinct item after duplicate adds, got %d", len(items))
	}
	if !s.Contains("dup") {
		t.Error("expected Contains(\"dup\") == true")
	}
}

func TestSet_MergeConvergence(t *testing.T) {
	nodeA := NewSet[string]("node-a")
	nodeB := NewSet[string]("node-b")

	nodeA.Add("a")
	nodeB.Add("b")

	nodeA.Merge(nodeB)
	nodeB.Merge(nodeA)

	for _, s := range []*Set[string]{nodeA, nodeB} {
		if !s.Contains("a") {
			t.Errorf("node %s: expected Contains(\"a\")", s.NodeID())
		}
		if !s.Contains("b") {
			t.Errorf("node %s: expected Contains(\"b\")", s.NodeID())
		}
	}
}

func TestSet_AddWins(t *testing.T) {
	// Step 1: build shared initial state on node-a, then sync to node-b.
	nodeA := NewSet[string]("node-a")
	nodeA.Add("x")

	nodeB := NewSet[string]("node-b")
	nodeB.Merge(nodeA) // nodeB now has "x" too

	// Step 2: node-a removes "x" (tombstones the shared entry).
	nodeA.Remove("x")

	// Step 3: node-b independently adds "x" again (new entry, different HLC tag).
	nodeB.Add("x")

	// Step 4: bidirectional merge.
	nodeA.Merge(nodeB)
	nodeB.Merge(nodeA)

	// Step 5: add-wins — both nodes must still contain "x".
	if !nodeA.Contains("x") {
		t.Error("add-wins violated: node-a does not contain \"x\" after merge")
	}
	if !nodeB.Contains("x") {
		t.Error("add-wins violated: node-b does not contain \"x\" after merge")
	}
}

func TestSet_MergeIdempotent(t *testing.T) {
	nodeA := NewSet[string]("node-a")
	nodeB := NewSet[string]("node-b")

	nodeA.Add("hello")
	nodeB.Add("world")

	nodeA.Merge(nodeB)
	nodeA.Merge(nodeB) // second merge should be a no-op

	items := nodeA.Items()
	sort.Strings(items)
	if len(items) != 2 || items[0] != "hello" || items[1] != "world" {
		t.Errorf("idempotent merge produced unexpected items: %v", items)
	}
}

func TestSet_RemoveNonExistent(t *testing.T) {
	s := NewSet[string]("node-a")
	// Should not panic.
	s.Remove("ghost")
	if s.Contains("ghost") {
		t.Error("expected Contains(\"ghost\") == false")
	}
}

func TestSet_Commutativity(t *testing.T) {
	// Build two independent nodes with different elements.
	nodeA1 := NewSet[string]("node-a")
	nodeB1 := NewSet[string]("node-b")
	nodeA1.Add("a")
	nodeB1.Add("b")

	nodeA2 := NewSet[string]("node-a")
	nodeB2 := NewSet[string]("node-b")
	nodeA2.Add("a")
	nodeB2.Add("b")

	// Order 1: A→B then B→A on copies 1.
	nodeA1.Merge(nodeB1)
	nodeB1.Merge(nodeA1)

	// Order 2: B→A then A→B on copies 2.
	nodeB2.Merge(nodeA2)
	nodeA2.Merge(nodeB2)

	items1 := nodeA1.Items()
	items2 := nodeA2.Items()
	sort.Strings(items1)
	sort.Strings(items2)

	if len(items1) != len(items2) {
		t.Fatalf("commutativity violated: len %d vs %d", len(items1), len(items2))
	}
	for i := range items1 {
		if items1[i] != items2[i] {
			t.Errorf("commutativity violated at index %d: %q vs %q", i, items1[i], items2[i])
		}
	}
}
