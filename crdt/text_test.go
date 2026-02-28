package crdt

import (
	"sync"
	"testing"

	"github.com/brunoga/deep/v5/crdt/hlc"
)

func TestText_Insert(t *testing.T) {
	clock := hlc.NewClock("node1")
	var text Text

	// 1. Initial insert
	text = text.Insert(0, "Hello", clock)
	if text.String() != "Hello" {
		t.Errorf("expected Hello, got %s", text.String())
	}
	if len(text) != 1 {
		t.Errorf("expected 1 run, got %d", len(text))
	}

	// 2. Insert in middle (triggers split)
	text = text.Insert(2, "!", clock)
	if text.String() != "He!llo" {
		t.Errorf("expected He!llo, got %s", text.String())
	}
	// Runs should be: "He", "!", "llo"
	// After normalize: "He", "!", "llo" (they are not contiguous in ID sequence
	// because "!" has a new WallTime)
	if len(text) != 3 {
		t.Errorf("expected 3 runs, got %d", len(text))
	}

	// Check IDs
	ordered := text.getOrdered()
	if ordered[0].Value != "He" {
		t.Errorf("ordered[0] should be 'He', got %s", ordered[0].Value)
	}
	if ordered[1].Value != "!" {
		t.Errorf("ordered[1] should be '!', got %s", ordered[1].Value)
	}
	if ordered[2].Value != "llo" {
		t.Errorf("ordered[2] should be 'llo', got %s", ordered[2].Value)
	}

	expectedID2 := ordered[0].ID
	expectedID2.Logical += 2
	if ordered[2].ID != expectedID2 {
		t.Errorf("ordered[2] ID mismatch: expected %v, got %v", expectedID2, ordered[2].ID)
	}
}

func TestText_Delete(t *testing.T) {
	clock := hlc.NewClock("node1")
	text := Text{}.Insert(0, "Hello World", clock)

	// Delete " World"
	text = text.Delete(5, 6)
	if text.String() != "Hello" {
		t.Errorf("expected Hello, got %s", text.String())
	}
	// In RGA with tombstones, the run remains but is marked Deleted.
	// normalize() might merge it if contiguous.
	// "Hello" (not deleted), " World" (deleted)
	if len(text) != 2 {
		t.Errorf("expected 2 runs (1 tombstone), got %d", len(text))
	}

	// Delete middle of run "Hello" -> "Heo"
	text = text.Delete(2, 2)
	if text.String() != "Heo" {
		t.Errorf("expected Heo, got %s", text.String())
	}

	// "He" (active), "ll" (tombstone), "o" (active), " World" (tombstone)
	// normalize() might merge adjacent tombstones.
	if text[1].Value != "ll" || !text[1].Deleted {
		t.Errorf("expected tombstone 'll', got %v", text[1])
	}
}

func TestText_CRDT_Convergence(t *testing.T) {
	docA := NewCRDT(Text{}, "A")
	docB := NewCRDT(Text{}, "B")

	// A types "Hello"
	deltaA1 := docA.Edit(func(t *Text) {
		*t = t.Insert(0, "Hello", docA.Clock())
	})

	// B receives
	docB.ApplyDelta(deltaA1)

	// Concurrent edits
	// A: "Hello" -> "Hello World"
	deltaA2 := docA.Edit(func(t *Text) {
		*t = t.Insert(5, " World", docA.Clock())
	})

	// B: "Hello" -> "He!llo"
	deltaB1 := docB.Edit(func(t *Text) {
		*t = t.Insert(2, "!", docB.Clock())
	})

	// Sync
	docA.ApplyDelta(deltaB1)
	docB.ApplyDelta(deltaA2)

	if docA.View().String() != docB.View().String() {
		t.Errorf("Convergence failed!\nA: %s\nB: %s", docA.View().String(), docB.View().String())
	}
	// Expected: "He!llo World"
	t.Logf("Converged to: %s", docA.View().String())
}

func TestText_Normalize(t *testing.T) {
	id1 := hlc.HLC{WallTime: 100, Logical: 0, NodeID: "A"}
	id2 := id1
	id2.Logical = 3

	// To merge, id2.Prev must follow the last char of id1 (which is ID 100:2:A)
	prev2 := id1
	prev2.Logical = 2

	text := Text{
		{ID: id1, Value: "abc", Prev: hlc.HLC{}},
		{ID: id2, Value: "def", Prev: prev2},
	}

	normalized := text.normalize()
	if len(normalized) != 1 {
		t.Errorf("expected 1 run, got %d", len(normalized))
	}
	if normalized[0].Value != "abcdef" {
		t.Errorf("expected abcdef, got %s", normalized[0].Value)
	}
	if normalized[0].ID != id1 {
		t.Errorf("expected ID %v, got %v", id1, normalized[0].ID)
	}
}

func TestText_Concurrent_Fuzz(t *testing.T) {
	// 5 nodes, each doing many operations
	numNodes := 5
	opsPerNode := 20
	nodes := make([]*CRDT[Text], numNodes)
	for i := 0; i < numNodes; i++ {
		nodes[i] = NewCRDT(Text{}, string(rune('A'+i)))
	}

	// Initial shared state
	initial := nodes[0].Edit(func(text *Text) {
		*text = text.Insert(0, "START-END", nodes[0].Clock())
	})
	for i := 1; i < numNodes; i++ {
		nodes[i].ApplyDelta(initial)
	}

	// Channel to collect deltas
	deltas := make(chan Delta[Text], numNodes*opsPerNode)

	// Run concurrent edits
	var wg sync.WaitGroup
	for i := 0; i < numNodes; i++ {
		wg.Add(1)
		go func(nodeIdx int) {
			defer wg.Done()
			node := nodes[nodeIdx]
			for j := 0; j < opsPerNode; j++ {
				// Use the pointer passed to Edit, NOT node.View()
				delta := node.Edit(func(text *Text) {
					str := text.String() // <--- FIX: Use *text instead of node.View()
					if len(str) == 0 {
						*text = text.Insert(0, node.NodeID(), node.Clock())
						return
					}
					// Random-ish position
					pos := (j * (nodeIdx + 1)) % len(str)
					if j%3 == 0 && len(str) > 0 {
						// Delete
						*text = text.Delete(pos, 1)
					} else {
						// Insert
						*text = text.Insert(pos, node.NodeID(), node.Clock())
					}
				})
				deltas <- delta
			}
		}(i)
	}

	wg.Wait()
	close(deltas)

	// Everyone applies everyone else's deltas
	for delta := range deltas {
		for i := 0; i < numNodes; i++ {
			nodes[i].ApplyDelta(delta)
		}
	}

	// Verify all nodes converged to exactly the same state
	expected := nodes[0].View().String()
	for i := 1; i < numNodes; i++ {
		got := nodes[i].View().String()
		if got != expected {
			t.Errorf("Node %d did not converge. \nExpected: %q\nGot:      %q", i, expected, got)
		}
	}
	t.Logf("All nodes converged to: %s", expected)
}

func TestText_Concurrent_SamePositionInsertion(t *testing.T) {
	// Multiple nodes inserting at the EXACT SAME position concurrently.
	// Convergence should be deterministic based on HLC.
	docA := NewCRDT(Text{}, "A")
	docB := NewCRDT(Text{}, "B")
	docC := NewCRDT(Text{}, "C")

	initDelta := docA.Edit(func(text *Text) {
		*text = text.Insert(0, "[]", docA.Clock())
	})
	docB.ApplyDelta(initDelta)
	docC.ApplyDelta(initDelta)

	// All insert at position 1 (between '[' and ']')
	deltaA := docA.Edit(func(text *Text) { *text = text.Insert(1, "A", docA.Clock()) })
	deltaB := docB.Edit(func(text *Text) { *text = text.Insert(1, "B", docB.Clock()) })
	deltaC := docC.Edit(func(text *Text) { *text = text.Insert(1, "C", docC.Clock()) })

	// Full sync
	docs := []*CRDT[Text]{docA, docB, docC}
	allDeltas := []Delta[Text]{deltaA, deltaB, deltaC}
	for _, doc := range docs {
		for _, delta := range allDeltas {
			doc.ApplyDelta(delta)
		}
	}

	finalA := docA.View().String()
	finalB := docB.View().String()
	finalC := docC.View().String()

	if finalA != finalB || finalB != finalC {
		t.Fatalf("Non-deterministic convergence: A=%q, B=%q, C=%q", finalA, finalB, finalC)
	}

	// Based on ID descending order (HLC), the characters should appear in a specific order.
	// Since we can't be 100% sure of wall clock, we just check they are all the same.
	t.Logf("Converged same-pos inserts to: %s", finalA)
}

func TestText_Concurrent_OverlappingDeletions(t *testing.T) {
	// Two nodes deleting the same and overlapping characters concurrently.
	docA := NewCRDT(Text{}, "A")
	docB := NewCRDT(Text{}, "B")

	initDelta := docA.Edit(func(text *Text) {
		*text = text.Insert(0, "ABCDEFG", docA.Clock())
	})
	docB.ApplyDelta(initDelta)

	// Node A deletes "BCD" (index 1, length 3)
	deltaA := docA.Edit(func(text *Text) { *text = text.Delete(1, 3) })
	// Node B deletes "DEF" (index 3, length 3)
	deltaB := docB.Edit(func(text *Text) { *text = text.Delete(3, 3) })

	// Sync
	docA.ApplyDelta(deltaB)
	docB.ApplyDelta(deltaA)

	// Expected: "A" + "G" = "AG"
	// "BCD" and "DEF" are both deleted. Overlap "DE" is deleted by both.
	expected := "AG"
	if docA.View().String() != expected || docB.View().String() != expected {
		t.Errorf("Overlapping deletion failed. A=%q, B=%q, expected %q", docA.View().String(), docB.View().String(), expected)
	}
}
