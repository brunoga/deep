package crdt

import (
	"testing"
	"time"
)

func TestMap_SetGet(t *testing.T) {
	m := NewMap[string, int]("node-a")
	m.Set("a", 1)

	v, ok := m.Get("a")
	if !ok || v != 1 {
		t.Errorf("expected (1, true), got (%d, %v)", v, ok)
	}

	v2, ok2 := m.Get("b")
	if ok2 || v2 != 0 {
		t.Errorf("expected (0, false) for missing key, got (%d, %v)", v2, ok2)
	}
}

func TestMap_Delete(t *testing.T) {
	m := NewMap[string, int]("node-a")
	m.Set("a", 1)
	m.Delete("a")

	if m.Contains("a") {
		t.Error("expected Contains(\"a\") == false after Delete")
	}
}

func TestMap_DeleteNonExistent(t *testing.T) {
	m := NewMap[string, int]("node-a")
	// Should not panic.
	m.Delete("missing")
}

func TestMap_Overwrite(t *testing.T) {
	m := NewMap[string, int]("node-a")
	m.Set("a", 1)
	m.Set("a", 2)

	v, ok := m.Get("a")
	if !ok || v != 2 {
		t.Errorf("expected (2, true) after overwrite, got (%d, %v)", v, ok)
	}
}

func TestMap_Keys(t *testing.T) {
	m := NewMap[string, int]("node-a")
	m.Set("x", 1)
	m.Set("y", 2)
	m.Set("z", 3)
	m.Delete("y")

	keys := m.Keys()
	if len(keys) != 2 {
		t.Errorf("expected 2 live keys, got %d: %v", len(keys), keys)
	}
	for _, k := range keys {
		if k == "y" {
			t.Error("deleted key \"y\" should not appear in Keys()")
		}
	}
}

func TestMap_MergeDisjointKeys(t *testing.T) {
	a := NewMap[string, int]("node-a")
	b := NewMap[string, int]("node-b")

	a.Set("x", 1)
	b.Set("y", 2)

	a.Merge(b)
	b.Merge(a)

	for _, node := range []*Map[string, int]{a, b} {
		v, ok := node.Get("x")
		if !ok || v != 1 {
			t.Errorf("node %s: expected x=1, got (%d, %v)", node.NodeID(), v, ok)
		}
		v2, ok2 := node.Get("y")
		if !ok2 || v2 != 2 {
			t.Errorf("node %s: expected y=2, got (%d, %v)", node.NodeID(), v2, ok2)
		}
	}
}

func TestMap_LWW_SameKey(t *testing.T) {
	a := NewMap[string, string]("node-a")
	b := NewMap[string, string]("node-b")

	a.Set("k", "a")
	time.Sleep(2 * time.Millisecond)
	b.Set("k", "b")

	a.Merge(b)

	v, ok := a.Get("k")
	if !ok || v != "b" {
		t.Errorf("LWW: expected \"b\" (newer) to win, got (%q, %v)", v, ok)
	}
}

func TestMap_DeleteWinsOverOlderSet(t *testing.T) {
	a := NewMap[string, string]("node-a")
	b := NewMap[string, string]("node-b")

	a.Set("k", "v")
	time.Sleep(2 * time.Millisecond)
	b.Set("k", "v") // b needs to know about the key to delete it with a newer ts
	b.Delete("k")

	a.Merge(b)

	if a.Contains("k") {
		t.Error("expected Contains(\"k\") == false after merging newer delete")
	}
}

func TestMap_SetWinsOverOlderDelete(t *testing.T) {
	a := NewMap[string, string]("node-a")
	b := NewMap[string, string]("node-b")

	// a deletes first, b re-sets with a newer timestamp
	a.Set("k", "old")
	a.Delete("k")
	time.Sleep(2 * time.Millisecond)
	b.Set("k", "new")

	a.Merge(b)

	v, ok := a.Get("k")
	if !ok || v != "new" {
		t.Errorf("expected newer Set to win over older Delete, got (%q, %v)", v, ok)
	}
}

func TestMap_MergeIdempotent(t *testing.T) {
	a := NewMap[string, int]("node-a")
	b := NewMap[string, int]("node-b")

	a.Set("x", 10)
	b.Set("y", 20)

	a.Merge(b)
	// Snapshot state after one merge.
	keys1 := a.Len()
	v1, _ := a.Get("x")
	v2, _ := a.Get("y")

	// Merge again — should be idempotent.
	a.Merge(b)

	if a.Len() != keys1 {
		t.Errorf("idempotent: Len changed after second merge: %d vs %d", a.Len(), keys1)
	}
	gv1, _ := a.Get("x")
	gv2, _ := a.Get("y")
	if gv1 != v1 || gv2 != v2 {
		t.Errorf("idempotent: values changed after second merge")
	}
}

func TestMap_Commutativity(t *testing.T) {
	// Scenario 1: merge A→B then B→A
	a1 := NewMap[string, int]("node-a")
	b1 := NewMap[string, int]("node-b")
	a1.Set("shared", 1)
	time.Sleep(2 * time.Millisecond)
	b1.Set("shared", 2)
	a1.Set("only-a", 10)
	b1.Set("only-b", 20)

	a1.Merge(b1)
	b1.Merge(a1)
	va1, _ := a1.Get("shared")
	vb1, _ := b1.Get("shared")

	// Scenario 2: fresh nodes, merge B→A then A→B
	a2 := NewMap[string, int]("node-a")
	b2 := NewMap[string, int]("node-b")
	a2.Set("shared", 1)
	time.Sleep(2 * time.Millisecond)
	b2.Set("shared", 2)
	a2.Set("only-a", 10)
	b2.Set("only-b", 20)

	b2.Merge(a2)
	a2.Merge(b2)
	va2, _ := a2.Get("shared")
	vb2, _ := b2.Get("shared")

	if va1 != va2 {
		t.Errorf("commutativity: a diverged: scenario1=%d scenario2=%d", va1, va2)
	}
	if vb1 != vb2 {
		t.Errorf("commutativity: b diverged: scenario1=%d scenario2=%d", vb1, vb2)
	}
	if a1.Len() != a2.Len() {
		t.Errorf("commutativity: Len diverged: scenario1=%d scenario2=%d", a1.Len(), a2.Len())
	}
}

func TestMap_StringKeys(t *testing.T) {
	a := NewMap[string, int]("node-a")
	b := NewMap[string, int]("node-b")

	a.Set("hello", 42)
	b.Set("world", 99)

	a.Merge(b)

	v1, ok1 := a.Get("hello")
	v2, ok2 := a.Get("world")
	if !ok1 || v1 != 42 {
		t.Errorf("expected hello=42, got (%d, %v)", v1, ok1)
	}
	if !ok2 || v2 != 99 {
		t.Errorf("expected world=99, got (%d, %v)", v2, ok2)
	}
}

func TestMap_IntKeys(t *testing.T) {
	a := NewMap[int, string]("node-a")
	b := NewMap[int, string]("node-b")

	a.Set(1, "one")
	b.Set(2, "two")

	a.Merge(b)

	v1, ok1 := a.Get(1)
	v2, ok2 := a.Get(2)
	if !ok1 || v1 != "one" {
		t.Errorf("expected 1=\"one\", got (%q, %v)", v1, ok1)
	}
	if !ok2 || v2 != "two" {
		t.Errorf("expected 2=\"two\", got (%q, %v)", v2, ok2)
	}
}
