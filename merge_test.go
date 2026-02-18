package deep

import (
	"testing"
)

func TestMerge_Basic(t *testing.T) {
	type Data struct {
		A int
		B int
		C int
	}
	base := Data{A: 1, B: 1, C: 1}
	
	// Patch 1: A -> 2
	p1 := MustDiff(base, Data{A: 2, B: 1, C: 1})
	
	// Patch 2: B -> 3
	p2 := MustDiff(base, Data{A: 1, B: 3, C: 1})
	
	// Merge
	merged, conflicts, err := Merge(p1, p2)
	if err != nil {
		t.Fatalf("Merge failed: %v", err)
	}
	if len(conflicts) > 0 {
		t.Errorf("Expected no conflicts, got %d", len(conflicts))
	}
	
	final := base
	err = merged.ApplyChecked(&final)
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}
	
	expected := Data{A: 2, B: 3, C: 1}
	if final != expected {
		t.Errorf("Merge incorrect: got %+v, want %+v", final, expected)
	}
}

func TestMerge_Conflict(t *testing.T) {
	type Data struct {
		A int
	}
	base := Data{A: 1}
	
	// Patch 1: A -> 2
	p1 := MustDiff(base, Data{A: 2})
	
	// Patch 2: A -> 3
	p2 := MustDiff(base, Data{A: 3})
	
	merged, conflicts, err := Merge(p1, p2)
	if err != nil {
		t.Fatalf("Merge failed: %v", err)
	}
	
	if len(conflicts) != 1 {
		t.Fatalf("Expected 1 conflict, got %d", len(conflicts))
	}
	
	// By default, last one wins in current implementation for the patch part
	final := base
	merged.ApplyChecked(&final)
	if final.A != 3 {
		t.Errorf("Expected last patch to win value, got %d", final.A)
	}
}

func TestMerge_TreeConflict(t *testing.T) {
	type Inner struct {
		A int
	}
	type Data struct {
		I *Inner
	}
	
	base := Data{I: &Inner{A: 1}}
	
	// Patch 1: I.A -> 2
	p1 := MustDiff(base, Data{I: &Inner{A: 2}})
	
	// Patch 2: I -> nil (delete I)
	p2 := MustDiff(base, Data{I: nil})
	
	// Merge
	_, conflicts, err := Merge(p1, p2)
	if err != nil {
		t.Fatalf("Merge failed: %v", err)
	}
	
	// Should detect conflict on /I/A (modified) vs /I (deleted)
	if len(conflicts) == 0 {
		t.Error("Expected tree conflict")
	}
}
