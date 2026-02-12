package deep

import (
	"testing"
)

type KeyedTask struct {
	ID     string `deep:"key"`
	Status string
	Value  int
}

func TestKeyedSlice_Basic(t *testing.T) {
	a := []KeyedTask{
		{ID: "t1", Status: "todo", Value: 1},
		{ID: "t2", Status: "todo", Value: 2},
	}
	// Swap and modify t2
	b := []KeyedTask{
		{ID: "t2", Status: "done", Value: 2},
		{ID: "t1", Status: "todo", Value: 1},
	}

	patch := Diff(a, b)
	if patch == nil {
		t.Fatal("Expected patch")
	}

	// Without keyed alignment, Myers' would see this as:
	// 0: t1 -> t2 (replace)
	// 1: t2 -> t1 (replace)
	// Or Del/Add.
	
	// With keyed alignment, it should see that t2 is the same entity even if it moved
	// (within the limits of Myers').
	
	patch.Apply(&a)

	if len(a) != 2 {
		t.Fatalf("Expected 2 tasks, got %d", len(a))
	}

	if a[0].ID != "t2" || a[0].Status != "done" {
		t.Errorf("Expected t2 done at index 0, got %+v", a[0])
	}
	if a[1].ID != "t1" || a[1].Status != "todo" {
		t.Errorf("Expected t1 todo at index 1, got %+v", a[1])
	}
}

func TestKeyedSlice_Ptr(t *testing.T) {
	a := []*KeyedTask{
		{ID: "t1", Status: "todo"},
		{ID: "t2", Status: "todo"},
	}
	b := []*KeyedTask{
		{ID: "t2", Status: "done"},
		{ID: "t1", Status: "todo"},
	}

	patch := Diff(a, b)
	if patch == nil {
		t.Fatal("Expected patch")
	}

	patch.Apply(&a)

	if a[0].ID != "t2" || a[0].Status != "done" {
		t.Errorf("Expected t2 done at index 0, got %+v", a[0])
	}
	if a[1].ID != "t1" || a[1].Status != "todo" {
		t.Errorf("Expected t1 todo at index 1, got %+v", a[1])
	}
}

func TestKeyedSlice_Complex(t *testing.T) {
	a := []KeyedTask{
		{ID: "t1", Status: "todo"},
		{ID: "t2", Status: "todo"},
		{ID: "t3", Status: "todo"},
	}
	// Move t3 to start, remove t2, add t4, modify t1
	b := []KeyedTask{
		{ID: "t3", Status: "todo"},
		{ID: "t1", Status: "done"},
		{ID: "t4", Status: "new"},
	}

	patch := Diff(a, b)
	if patch == nil {
		t.Fatal("Expected patch")
	}

	patch.Apply(&a)

	if len(a) != 3 {
		t.Fatalf("Expected 3 tasks, got %d", len(a))
	}

	if a[0].ID != "t3" || a[1].ID != "t1" || a[1].Status != "done" || a[2].ID != "t4" {
		t.Errorf("Unexpected results after apply: %+v", a)
	}
}
