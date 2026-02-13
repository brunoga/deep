package crdt

import (
	"testing"

	"github.com/brunoga/deep/v3/crdt/hlc"
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
	if len(text) != 3 {
		t.Errorf("expected 3 runs, got %d", len(text))
	}

	// Check IDs
	if text[0].Value != "He" {
		t.Errorf("text[0] should be 'He', got %s", text[0].Value)
	}
	if text[1].Value != "!" {
		t.Errorf("text[1] should be '!', got %s", text[1].Value)
	}
	if text[2].Value != "llo" {
		t.Errorf("text[2] should be 'llo', got %s", text[2].Value)
	}

	expectedID2 := text[0].ID
	expectedID2.Logical += 2
	if text[2].ID != expectedID2 {
		t.Errorf("text[2] ID mismatch: expected %v, got %v", expectedID2, text[2].ID)
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
	if len(text) != 1 {
		t.Errorf("expected 1 run, got %d", len(text))
	}

	// Delete middle of run "Hello" -> "Heo"
	text = text.Delete(2, 2)
	if text.String() != "Heo" {
		t.Errorf("expected Heo, got %s", text.String())
	}
	if len(text) != 2 {
		t.Errorf("expected 2 runs, got %d", len(text))
	}

	// 3. Edge cases
	// Delete from empty
	empty := Text{}.Delete(0, 5)
	if len(empty) != 0 {
		t.Error("expected empty result")
	}

	// Delete length 0
	text2 := text.Delete(0, 0)
	if len(text2) != len(text) {
		t.Error("expected no change for length 0")
	}

	// Out of bounds (starts after end)
	text3 := text.Delete(10, 5)
	if text3.String() != text.String() {
		t.Error("expected no change for out of bounds delete")
	}

	// Delete across multiple runs
	multi := Text{}.Insert(0, "ABC", clock).Insert(3, "DEF", clock).Insert(6, "GHI", clock)
	// "ABCDEFGHI" -> delete "CDEFG" (index 2, length 5) -> "ABHI"
	multi = multi.Delete(2, 5)
	if multi.String() != "ABHI" {
		t.Errorf("expected ABHI, got %s", multi.String())
	}
}

func TestText_CRDT_Convergence(t *testing.T) {
	docA := NewCRDT(Text{}, "A")
	docB := NewCRDT(Text{}, "B")

	// A types "Hello"
	deltaA1 := docA.Edit(func(t *Text) {
		*t = t.Insert(0, "Hello", docA.Clock)
	})

	// B receives
	docB.ApplyDelta(deltaA1)

	// Concurrent edits
	// A: "Hello" -> "Hello World"
	deltaA2 := docA.Edit(func(t *Text) {
		*t = t.Insert(5, " World", docA.Clock)
	})

	// B: "Hello" -> "He!llo"
	deltaB1 := docB.Edit(func(t *Text) {
		*t = t.Insert(2, "!", docB.Clock)
	})

	// Sync
	docA.ApplyDelta(deltaB1)
	docB.ApplyDelta(deltaA2)

	if docA.Value.String() != docB.Value.String() {
		t.Errorf("Convergence failed!\nA: %s\nB: %s", docA.Value.String(), docB.Value.String())
	}
	t.Logf("Converged to: %s", docA.Value.String())
}

func TestText_Normalize(t *testing.T) {
	id1 := hlc.HLC{WallTime: 100, Logical: 0, NodeID: "A"}
	id2 := id1
	id2.Logical = 3

	text := Text{
		{ID: id1, Value: "abc"},
		{ID: id2, Value: "def"},
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
