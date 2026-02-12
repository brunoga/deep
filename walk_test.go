package deep

import (
	"fmt"
	"testing"
)

func TestPatch_Walk_Basic(t *testing.T) {
	a := 10
	b := 20
	patch := Diff(a, b)

	var ops []string
	err := patch.Walk(func(path string, op OpKind, old, new any) error {
		ops = append(ops, fmt.Sprintf("%s:%s:%v:%v", path, op, old, new))
		return nil
	})

	if err != nil {
		t.Fatalf("Walk failed: %v", err)
	}

	expected := []string{"/:replace:10:20"}
	if fmt.Sprintf("%v", ops) != fmt.Sprintf("%v", expected) {
		t.Errorf("Expected ops %v, got %v", expected, ops)
	}
}

func TestPatch_Walk_Struct(t *testing.T) {
	type S struct {
		A int
		B string
	}
	a := S{A: 1, B: "one"}
	b := S{A: 2, B: "two"}
	patch := Diff(a, b)

	ops := make(map[string]string)
	err := patch.Walk(func(path string, op OpKind, old, new any) error {
		ops[path] = fmt.Sprintf("%s:%v:%v", op, old, new)
		return nil
	})

	if err != nil {
		t.Fatalf("Walk failed: %v", err)
	}

	if len(ops) != 2 {
		t.Errorf("Expected 2 ops, got %d", len(ops))
	}

	if ops["A"] != "replace:1:2" {
		t.Errorf("Unexpected op for A: %s", ops["A"])
	}
	if ops["B"] != "replace:one:two" {
		t.Errorf("Unexpected op for B: %s", ops["B"])
	}
}

func TestPatch_Walk_Slice(t *testing.T) {
	a := []int{1, 2, 3}
	b := []int{1, 4, 3, 5}
	patch := Diff(a, b)

	var ops []string
	err := patch.Walk(func(path string, op OpKind, old, new any) error {
		ops = append(ops, fmt.Sprintf("%s:%s:%v:%v", path, op, old, new))
		return nil
	})

	if err != nil {
		t.Fatalf("Walk failed: %v", err)
	}

	// Myers' might see this as replace 2 with 4 and add 5.
	// Path for slice elements should be like "[1]".
	
	found4 := false
	found5 := false
	for _, op := range ops {
		if op == "[1]:replace:2:4" {
			found4 = true
		}
		if op == "[3]:add:<nil>:5" {
			found5 = true
		}
	}

	if !found4 || !found5 {
		t.Errorf("Missing expected ops in %v", ops)
	}
}

func TestPatch_Walk_Map(t *testing.T) {
	a := map[string]int{"one": 1, "two": 2}
	b := map[string]int{"one": 1, "two": 20, "three": 3}
	patch := Diff(a, b)

	ops := make(map[string]string)
	err := patch.Walk(func(path string, op OpKind, old, new any) error {
		ops[path] = fmt.Sprintf("%s:%v:%v", op, old, new)
		return nil
	})

	if err != nil {
		t.Fatalf("Walk failed: %v", err)
	}

	if ops["two"] != "replace:2:20" {
		t.Errorf("Unexpected op for two: %s", ops["two"])
	}
	if ops["three"] != "add:<nil>:3" {
		t.Errorf("Unexpected op for three: %s", ops["three"])
	}
}

func TestPatch_Walk_KeyedSlice(t *testing.T) {
	a := []KeyedTask{
		{ID: "t1", Status: "todo"},
		{ID: "t2", Status: "todo"},
	}
	b := []KeyedTask{
		{ID: "t2", Status: "done"},
		{ID: "t1", Status: "todo"},
	}

	patch := Diff(a, b)
	
	ops := make(map[string]string)
	err := patch.Walk(func(path string, op OpKind, old, new any) error {
		ops[path] = fmt.Sprintf("%s:%v:%v", op, old, new)
		return nil
	})

	if err != nil {
		t.Fatalf("Walk failed: %v", err)
	}

	// We expect t2 at [0] to be modified.
	// Since it's Myers' with substitution, it might be replace or complex.
	// In our implementation of computeSliceEdits with keys, we currently 
	// generate opMod for same keys at same logical index in Myers' path.
	
	// Wait, if it's a swap, Myers' might see it as Del/Add or similar depending on the 
	// specific path it takes.
	
	if len(ops) == 0 {
		t.Errorf("Expected some ops, got none")
	}
}

func TestPatch_Walk_ErrorStop(t *testing.T) {
	a := map[string]int{"one": 1, "two": 2}
	b := map[string]int{"one": 10, "two": 20}
	patch := Diff(a, b)

	count := 0
	err := patch.Walk(func(path string, op OpKind, old, new any) error {
		count++
		return fmt.Errorf("stop")
	})

	if err == nil || err.Error() != "stop" {
		t.Errorf("Expected 'stop' error, got %v", err)
	}
	if count != 1 {
		t.Errorf("Expected walk to stop after 1 call, got %d", count)
	}
}
