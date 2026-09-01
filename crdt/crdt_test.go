package crdt

import (
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"
	"time"

	deep "github.com/brunoga/deep/v5"
)

type TestUser struct {
	ID      int `deep:"key"`
	Name    string
	Friends []TestFriend `deep:"key"`
}

type TestFriend struct {
	ID   int `deep:"key"`
	Name string
}

func TestCRDT_EditDelta(t *testing.T) {
	nodeA := NewCRDT(TestUser{ID: 1, Name: "Alice"}, "node-a")
	nodeB := NewCRDT(TestUser{ID: 1, Name: "Alice"}, "node-b")

	delta := nodeA.Edit(func(u *TestUser) {
		u.Name = "Alice Mod"
	})

	if nodeA.View().Name != "Alice Mod" {
		t.Error("Edit failed to update local value")
	}

	nodeB.ApplyDelta(delta)
	if nodeB.View().Name != "Alice Mod" {
		t.Error("ApplyDelta failed to update remote value")
	}
}

func TestCRDT_EditApplied(t *testing.T) {
	node := NewCRDT(TestUser{ID: 1, Name: "Old"}, "node1")

	delta := node.Edit(func(u *TestUser) { u.Name = "New" })

	if node.View().Name != "New" {
		t.Errorf("expected New, got %s", node.View().Name)
	}

	node2 := NewCRDT(TestUser{ID: 1, Name: "Old"}, "node2")
	node2.ApplyDelta(delta)
	if node2.View().Name != "New" {
		t.Error("delta from Edit did not propagate correctly")
	}
}

func TestCRDT_Merge(t *testing.T) {
	initial := TestUser{
		ID:      1,
		Friends: []TestFriend{{ID: 100, Name: "Bob"}},
	}
	nodeA := NewCRDT(initial, "node-a")
	nodeB := NewCRDT(initial, "node-b")

	// Concurrent edits
	nodeA.Edit(func(u *TestUser) {
		u.Friends[0].Name = "Bobby"
	})
	nodeB.Edit(func(u *TestUser) {
		u.Friends = append(u.Friends, TestFriend{ID: 101, Name: "Charlie"})
	})

	nodeA.Merge(nodeB)
	nodeB.Merge(nodeA)

	viewA := nodeA.View()
	viewB := nodeB.View()

	if !reflect.DeepEqual(viewA, viewB) {
		t.Errorf("Nodes diverged after merge! A: %+v B: %+v", viewA, viewB)
	}

	if len(viewA.Friends) != 2 {
		t.Errorf("Expected 2 friends, got %d", len(viewA.Friends))
	}
}

func TestCRDT_Conflict(t *testing.T) {
	nodeA := NewCRDT(TestUser{ID: 1, Name: "Original"}, "node-a")
	nodeB := NewCRDT(TestUser{ID: 1, Name: "Original"}, "node-b")

	// nodeA edits first
	nodeA.Edit(func(u *TestUser) { u.Name = "Alice" })
	// nodeB edits later (physically)
	time.Sleep(2 * time.Millisecond)
	nodeB.Edit(func(u *TestUser) { u.Name = "Bob" })

	nodeA.Merge(nodeB)
	if nodeA.View().Name != "Bob" {
		t.Errorf("LWW failed: expected 'Bob', got '%s'", nodeA.View().Name)
	}
}

// --- Fix: Text fields in structs are merged convergently, not via LWW ---

type docWithText struct {
	Title string
	Body  Text
}

// Both nodes concurrently insert into the same Text field. After a bidirectional
// Merge both must hold the same text containing both insertions.
func TestMerge_TextFieldConvergent(t *testing.T) {
	nodeA := NewCRDT(docWithText{Title: "doc"}, "node-a")
	nodeB := NewCRDT(docWithText{Title: "doc"}, "node-b")

	nodeA.Edit(func(d *docWithText) {
		d.Body = d.Body.Insert(0, "hello", nodeA.Clock())
	})
	nodeB.Edit(func(d *docWithText) {
		d.Body = d.Body.Insert(0, "world", nodeB.Clock())
	})

	nodeA.Merge(nodeB)
	nodeB.Merge(nodeA)

	viewA := nodeA.View()
	viewB := nodeB.View()

	if viewA.Body.String() != viewB.Body.String() {
		t.Errorf("Text diverged after merge: A=%q B=%q", viewA.Body.String(), viewB.Body.String())
	}
	combined := viewA.Body.String()
	if !strings.Contains(combined, "hello") || !strings.Contains(combined, "world") {
		t.Errorf("Text merge dropped content: got %q", combined)
	}
}

// Even when nodeA's edit has an older HLC timestamp, its text must still
// appear in the merged result — Text bypasses LWW entirely.
func TestMerge_TextNotLWWFiltered(t *testing.T) {
	nodeA := NewCRDT(docWithText{}, "node-a")
	nodeB := NewCRDT(docWithText{}, "node-b")

	// nodeA edits first (older wall-clock time)
	nodeA.Edit(func(d *docWithText) {
		d.Body = d.Body.Insert(0, "older", nodeA.Clock())
	})
	time.Sleep(2 * time.Millisecond)
	// nodeB edits later (newer wall-clock time)
	nodeB.Edit(func(d *docWithText) {
		d.Body = d.Body.Insert(0, "newer", nodeB.Clock())
	})

	nodeA.Merge(nodeB)

	combined := nodeA.View().Body.String()
	if !strings.Contains(combined, "older") || !strings.Contains(combined, "newer") {
		t.Errorf("LWW incorrectly filtered Text: got %q, want both 'older' and 'newer'", combined)
	}
}

// Fix: TextRun sub-field ops (e.g. /Body/<id>/Deleted) must still be
// detected as Text-field ops and handled via MergeTextRuns, not LWW.
func TestMerge_TextSubFieldOp(t *testing.T) {
	nodeA := NewCRDT(docWithText{}, "node-a")
	nodeB := NewCRDT(docWithText{}, "node-b")

	// nodeA inserts a run; nodeB gets the same initial state then deletes it.
	nodeA.Edit(func(d *docWithText) {
		d.Body = d.Body.Insert(0, "shared", nodeA.Clock())
	})
	// Sync nodeB to the same starting state.
	nodeB.Merge(nodeA)

	// nodeB soft-deletes the run (produces a /Body/<id>/Deleted sub-field op on next diff).
	nodeB.Edit(func(d *docWithText) {
		d.Body = d.Body.Delete(0, len("shared"))
	})
	// nodeA inserts more text concurrently.
	nodeA.Edit(func(d *docWithText) {
		d.Body = d.Body.Insert(len("shared"), " appended", nodeA.Clock())
	})

	nodeA.Merge(nodeB)
	nodeB.Merge(nodeA)

	viewA := nodeA.View()
	viewB := nodeB.View()

	if viewA.Body.String() != viewB.Body.String() {
		t.Errorf("Sub-field op: Text diverged: A=%q B=%q", viewA.Body.String(), viewB.Body.String())
	}
}

// Fix: Text field nested inside a nested struct must be detected and merged.
func TestMerge_TextInNestedStruct(t *testing.T) {
	type inner struct{ Notes Text }
	type outer struct{ Meta inner }

	nodeA := NewCRDT(outer{}, "node-a")
	nodeB := NewCRDT(outer{}, "node-b")

	nodeA.Edit(func(o *outer) {
		o.Meta.Notes = o.Meta.Notes.Insert(0, "note-a", nodeA.Clock())
	})
	nodeB.Edit(func(o *outer) {
		o.Meta.Notes = o.Meta.Notes.Insert(0, "note-b", nodeB.Clock())
	})

	nodeA.Merge(nodeB)
	nodeB.Merge(nodeA)

	viewA := nodeA.View()
	viewB := nodeB.View()

	if viewA.Meta.Notes.String() != viewB.Meta.Notes.String() {
		t.Errorf("Nested Text diverged: A=%q B=%q",
			viewA.Meta.Notes.String(), viewB.Meta.Notes.String())
	}
	combined := viewA.Meta.Notes.String()
	if !strings.Contains(combined, "note-a") || !strings.Contains(combined, "note-b") {
		t.Errorf("Nested Text merge dropped content: got %q", combined)
	}
}

// Fix: Text field inside a keyed-slice element must be detected by key, not
// by positional index. The old tree-walk would misalign elements if the two
// nodes had different slice orderings.
func TestMerge_TextInKeyedSliceElement(t *testing.T) {
	type section struct {
		ID      int `deep:"key"`
		Content Text
	}
	type article struct{ Sections []section }

	initial := article{Sections: []section{
		{ID: 1},
		{ID: 2},
	}}

	nodeA := NewCRDT(initial, "node-a")
	nodeB := NewCRDT(initial, "node-b")

	// Both nodes write to section 2's Content concurrently.
	nodeA.Edit(func(a *article) {
		a.Sections[1].Content = a.Sections[1].Content.Insert(0, "from-a", nodeA.Clock())
	})
	nodeB.Edit(func(a *article) {
		a.Sections[1].Content = a.Sections[1].Content.Insert(0, "from-b", nodeB.Clock())
	})
	// NodeA also prepends a new section, shifting positional indices.
	nodeA.Edit(func(a *article) {
		a.Sections = append([]section{{ID: 0}}, a.Sections...)
	})

	nodeA.Merge(nodeB)
	nodeB.Merge(nodeA)

	findSection := func(secs []section, id int) *section {
		for i := range secs {
			if secs[i].ID == id {
				return &secs[i]
			}
		}
		return nil
	}

	viewA := nodeA.View()
	viewB := nodeB.View()

	s2A := findSection(viewA.Sections, 2)
	s2B := findSection(viewB.Sections, 2)
	if s2A == nil || s2B == nil {
		t.Fatal("section 2 missing after merge")
	}
	if s2A.Content.String() != s2B.Content.String() {
		t.Errorf("Section 2 Text diverged: A=%q B=%q",
			s2A.Content.String(), s2B.Content.String())
	}
	combined := s2A.Content.String()
	if !strings.Contains(combined, "from-a") || !strings.Contains(combined, "from-b") {
		t.Errorf("Section 2 Text missing content: got %q", combined)
	}
}

func TestCRDT_Reverse(t *testing.T) {
	nodeA := NewCRDT(TestUser{ID: 1, Name: "Alice"}, "node-a")
	nodeB := NewCRDT(TestUser{ID: 1, Name: "Alice"}, "node-b")

	delta := nodeA.Edit(func(u *TestUser) { u.Name = "Bob" })

	// Undo on nodeA
	undoDelta := nodeA.Reverse(delta)
	if nodeA.View().Name != "Alice" {
		t.Errorf("Reverse did not undo: expected Alice, got %s", nodeA.View().Name)
	}

	// Undo delta propagates to peers
	nodeB.ApplyDelta(delta)
	nodeB.ApplyDelta(undoDelta)
	if nodeB.View().Name != "Alice" {
		t.Errorf("Reverse delta did not propagate: expected Alice, got %s", nodeB.View().Name)
	}

	// Redo: reverse the undo
	nodeA.Reverse(undoDelta)
	if nodeA.View().Name != "Bob" {
		t.Errorf("Redo failed: expected Bob, got %s", nodeA.View().Name)
	}
}

func TestCRDT_JSON(t *testing.T) {
	node := NewCRDT(TestUser{ID: 1, Name: "Initial"}, "node1")
	node.Edit(func(u *TestUser) {
		u.Name = "Modified"
	})

	data, err := node.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON failed: %v", err)
	}

	newNode := &CRDT[TestUser]{}
	if err := newNode.UnmarshalJSON(data); err != nil {
		t.Fatalf("UnmarshalJSON failed: %v", err)
	}

	if newNode.NodeID() != node.NodeID() {
		t.Errorf("expected NodeID %s, got %s", node.NodeID(), newNode.NodeID())
	}

	if newNode.View().Name != "Modified" {
		t.Errorf("expected Name Modified, got %s", newNode.View().Name)
	}

	// Ensure clocks are restored by doing a merge
	// If clocks are missing, merge might fail to converge correctly or assume old data
	node2 := NewCRDT(TestUser{ID: 1, Name: "Initial"}, "node2")
	node2.Merge(newNode)
	if node2.View().Name != "Modified" {
		t.Error("Merge with unmarshalled node failed")
	}
}

// TestNewCRDTClonesInitialValue asserts that a replica does not alias the
// value it was seeded with. Sharing it let a caller mutate replica state
// behind the mutex, and two replicas seeded from one value shared it.
func TestNewCRDTClonesInitialValue(t *testing.T) {
	type doc struct {
		Tags []string
		Meta map[string]string
	}
	initial := doc{Tags: []string{"one"}, Meta: map[string]string{"k": "v"}}

	a := NewCRDT(initial, "a")
	b := NewCRDT(initial, "b")

	// The caller mutating its own value must not reach inside the replica.
	initial.Tags[0] = "mutated"
	initial.Meta["k"] = "mutated"

	if got := a.View(); got.Tags[0] != "one" || got.Meta["k"] != "v" {
		t.Errorf("replica aliases the caller's value: %+v", got)
	}

	// Two replicas seeded from the same value must be independent.
	a.Edit(func(d *doc) { d.Tags[0] = "from-a" })
	if got := b.View(); got.Tags[0] == "from-a" {
		t.Error("an edit on one replica leaked into another seeded from the same value")
	}
}

type keyedItem struct {
	ID    string `deep:"key" json:"id"`
	Label string `json:"label"`
	Done  bool   `json:"done"`
}

type keyedDoc struct {
	Items []keyedItem       `json:"items"`
	Plain []string          `json:"plain"`
	Meta  map[string]string `json:"meta"`
}

// TestKeyedSliceConvergence asserts that replicas agree on the contents *and
// the order* of a keyed slice after concurrent additions. Apply appends a new
// element at the end, so the order used to depend on which delta happened to
// arrive first: two replicas holding the same elements disagreed.
func TestKeyedSliceConvergence(t *testing.T) {
	initial := keyedDoc{Items: []keyedItem{{ID: "t1", Label: "one"}}}
	a := NewCRDT(initial, "a")
	b := NewCRDT(initial, "b")

	// Deliberately chosen so insertion order and key order disagree.
	da := a.Edit(func(d *keyedDoc) { d.Items = append(d.Items, keyedItem{ID: "z", Label: "from-a"}) })
	db := b.Edit(func(d *keyedDoc) { d.Items = append(d.Items, keyedItem{ID: "m", Label: "from-b"}) })

	a.ApplyDelta(db)
	b.ApplyDelta(da)

	va, vb := a.View(), b.View()
	if !deep.Equal(va, vb) {
		t.Fatalf("replicas diverged:\n  a=%+v\n  b=%+v", va.Items, vb.Items)
	}
	if len(va.Items) != 3 {
		t.Fatalf("expected both additions to survive, got %+v", va.Items)
	}
}

// TestKeyedSliceRandomizedConvergence delivers a batch of concurrent edits to
// several replicas in a different order each, with duplicates, and requires
// every replica to end up identical.
func TestKeyedSliceRandomizedConvergence(t *testing.T) {
	for seed := int64(0); seed < 20; seed++ {
		rng := rand.New(rand.NewSource(seed))

		initial := keyedDoc{Items: []keyedItem{{ID: "seed"}}}
		nodes := make([]*CRDT[keyedDoc], 4)
		for i := range nodes {
			nodes[i] = NewCRDT(initial, fmt.Sprintf("n%d", i))
		}

		var deltas []Delta[keyedDoc]
		for i, n := range nodes {
			id := fmt.Sprintf("item-%d", i)
			switch rng.Intn(3) {
			case 0:
				deltas = append(deltas, n.Edit(func(d *keyedDoc) {
					d.Items = append(d.Items, keyedItem{ID: id, Label: id})
				}))
			case 1:
				deltas = append(deltas, n.Edit(func(d *keyedDoc) {
					d.Items[0].Label = id
				}))
			default:
				deltas = append(deltas, n.Edit(func(d *keyedDoc) {
					d.Items[0].Done = i%2 == 0
				}))
			}
		}

		for _, n := range nodes {
			for _, idx := range rng.Perm(len(deltas)) {
				n.ApplyDelta(deltas[idx])
			}
			// Redelivery must be a no-op.
			n.ApplyDelta(deltas[rng.Intn(len(deltas))])
		}

		want := nodes[0].View()
		for i, n := range nodes[1:] {
			if got := n.View(); !deep.Equal(got, want) {
				t.Fatalf("seed %d: replica %d diverged\n  want %+v\n  got  %+v",
					seed, i+1, want.Items, got.Items)
			}
		}
	}
}

// TestUnkeyedSliceIsLastWriteWins documents the other half of the contract: a
// slice with no deep:"key" tag is synchronized as a single value, so
// concurrent edits resolve by last-write-wins rather than merging. Replicas
// still agree — they just agree on one writer's slice.
func TestUnkeyedSliceIsLastWriteWins(t *testing.T) {
	initial := keyedDoc{Plain: []string{"one", "two"}}
	a := NewCRDT(initial, "a")
	b := NewCRDT(initial, "b")

	da := a.Edit(func(d *keyedDoc) { d.Plain[0] = "ONE" })
	db := b.Edit(func(d *keyedDoc) { d.Plain[1] = "TWO" })

	a.ApplyDelta(db)
	b.ApplyDelta(da)

	va, vb := a.View(), b.View()
	if !deep.Equal(va, vb) {
		t.Errorf("replicas diverged: %v vs %v", va.Plain, vb.Plain)
	}
	if len(va.Plain) != 2 {
		t.Errorf("unexpected result: %v", va.Plain)
	}
}

// TestMapKeysMergeIndependently is the recommended alternative to an unkeyed
// slice: distinct map keys are distinct paths, so both writes survive.
func TestMapKeysMergeIndependently(t *testing.T) {
	initial := keyedDoc{Meta: map[string]string{}}
	a := NewCRDT(initial, "a")
	b := NewCRDT(initial, "b")

	da := a.Edit(func(d *keyedDoc) { d.Meta["author"] = "alice" })
	db := b.Edit(func(d *keyedDoc) { d.Meta["lang"] = "en" })

	a.ApplyDelta(db)
	b.ApplyDelta(da)

	va, vb := a.View(), b.View()
	if !deep.Equal(va, vb) {
		t.Fatalf("diverged: %v vs %v", va.Meta, vb.Meta)
	}
	if len(va.Meta) != 2 {
		t.Errorf("expected both keys to survive, got %v", va.Meta)
	}
}
