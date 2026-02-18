package deep

import (
	"fmt"
	"reflect"
	"testing"
)

func TestDiff_Basic(t *testing.T) {
	tests := []struct {
		name string
		a, b int
	}{
		{"Same", 1, 1},
		{"Different", 1, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patch := MustDiff(tt.a, tt.b)
			if tt.a == tt.b {
				if patch != nil {
					t.Errorf("Expected nil patch, got %v", patch)
				}
			} else {
				if patch == nil {
					t.Errorf("Expected non-nil patch")
				}
				val := tt.a
				patch.Apply(&val)
				if val != tt.b {
					t.Errorf("Apply failed: expected %v, got %v", tt.b, val)
				}
			}
		})
	}
}

func TestDiff_Struct(t *testing.T) {
	type S struct {
		A int
		B string
		c int // unexported
	}
	a := S{A: 1, B: "one", c: 10}
	b := S{A: 2, B: "one", c: 20}
	patch := MustDiff(a, b)
	if patch == nil {
		t.Fatal("Expected patch")
	}
	patch.Apply(&a)
	if a != b {
		t.Errorf("Apply failed: expected %+v, got %+v", b, a)
	}
}

func TestDiff_Ptr(t *testing.T) {
	v1 := 10
	v2 := 20
	tests := []struct {
		name string
		a, b *int
	}{
		{"BothNil", nil, nil},
		{"NilToVal", nil, &v1},
		{"ValToNil", &v1, nil},
		{"ValToValSame", &v1, &v1},
		{"ValToValDiffAddr", &v1, &v2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var input *int
			if tt.a != nil {
				val := *tt.a
				input = &val
			}
			target := tt.b
			patch := MustDiff(input, target)
			isEqual := false
			if input == nil && target == nil {
				isEqual = true
			} else if input != nil && target != nil && *input == *target {
				isEqual = true
			}
			if isEqual {
				if patch != nil {
					t.Errorf("Expected nil patch")
				}
			} else {
				if patch == nil {
					t.Fatal("Expected patch")
				}
				patch.Apply(&input)
				if target == nil {
					if input != nil {
						t.Errorf("Expected nil, got %v", input)
					}
				} else {
					if input == nil {
						t.Errorf("Expected %v, got nil", *target)
					} else if *input != *target {
						t.Errorf("Expected %v, got %v", *target, *input)
					}
				}
			}
		})
	}
}

func TestDiff_Map(t *testing.T) {
	tests := []struct {
		name string
		a, b map[string]int
	}{
		{
			"Add",
			map[string]int{"a": 1},
			map[string]int{"a": 1, "b": 2},
		},
		{
			"Remove",
			map[string]int{"a": 1, "b": 2},
			map[string]int{"a": 1},
		},
		{
			"Modify",
			map[string]int{"a": 1},
			map[string]int{"a": 2},
		},
		{
			"Mixed",
			map[string]int{"a": 1, "b": 2},
			map[string]int{"a": 2, "c": 3},
		},
		{
			"NilToMap",
			nil,
			map[string]int{"a": 1},
		},
		{
			"MapToNil",
			map[string]int{"a": 1},
			nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var a map[string]int
			if tt.a != nil {
				a = make(map[string]int)
				for k, v := range tt.a {
					a[k] = v
				}
			}
			patch := MustDiff(a, tt.b)
			if patch == nil {
				t.Fatal("Expected patch")
			}
			patch.Apply(&a)
			if !reflect.DeepEqual(a, tt.b) {
				t.Errorf("Apply failed: expected %v, got %v", tt.b, a)
			}
		})
	}
}

func TestDiff_Slice(t *testing.T) {
	tests := []struct {
		name string
		a, b []string
	}{
		{
			"Append",
			[]string{"a"},
			[]string{"a", "b"},
		},
		{
			"DeleteEnd",
			[]string{"a", "b"},
			[]string{"a"},
		},
		{
			"DeleteStart",
			[]string{"a", "b"},
			[]string{"b"},
		},
		{
			"InsertStart",
			[]string{"b"},
			[]string{"a", "b"},
		},
		{
			"InsertMiddle",
			[]string{"a", "c"},
			[]string{"a", "b", "c"},
		},
		{
			"Modify",
			[]string{"a", "b", "c"},
			[]string{"a", "X", "c"},
		},
		{
			"Complex",
			[]string{"a", "b", "c", "d"},
			[]string{"a", "c", "E", "d", "f"},
		},
		{
			"NilToSlice",
			nil,
			[]string{"a"},
		},
		{
			"SliceToNil",
			[]string{"a"},
			nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var a []string
			if tt.a != nil {
				a = make([]string, len(tt.a))
				copy(a, tt.a)
			}
			patch := MustDiff(a, tt.b)
			if patch == nil {
				t.Fatal("Expected patch")
			}
			patch.Apply(&a)
			if !reflect.DeepEqual(a, tt.b) {
				t.Errorf("Apply failed: expected %v, got %v", tt.b, a)
			}
		})
	}
}

func TestDiff_Array(t *testing.T) {
	a := [3]int{1, 2, 3}
	b := [3]int{1, 4, 3}
	patch := MustDiff(a, b)
	patch.Apply(&a)
	if a != b {
		t.Errorf("Apply failed: expected %v, got %v", b, a)
	}
}

func TestDiff_Interface(t *testing.T) {
	var a any = 1
	var b any = 2
	patch := MustDiff(a, b)
	patch.Apply(&a)
	if a != b {
		t.Errorf("Apply failed: expected %v, got %v", b, a)
	}
	b = "string"
	patch = MustDiff(a, b)
	patch.Apply(&a)
	if a != b {
		t.Errorf("Apply failed: expected %v, got %v", b, a)
	}
}

func TestDiff_Nested(t *testing.T) {
	type Child struct {
		Name string
	}
	type Parent struct {
		C *Child
		L []int
	}
	a := Parent{
		C: &Child{Name: "old"},
		L: []int{1, 2},
	}
	b := Parent{
		C: &Child{Name: "new"},
		L: []int{1, 2, 3},
	}
	patch := MustDiff(a, b)
	patch.Apply(&a)
	if !reflect.DeepEqual(a, b) {
		t.Errorf("Apply failed")
	}
}

func TestDiff_SliceStruct(t *testing.T) {
	type S struct {
		ID int
		V  string
	}
	a := []S{{1, "v1"}, {2, "v2"}}
	b := []S{{1, "v1"}, {2, "v2-mod"}}
	patch := MustDiff(a, b)
	patch.Apply(&a)
	if !reflect.DeepEqual(a, b) {
		t.Errorf("Apply failed")
	}
}

func TestDiff_InterfaceExhaustive(t *testing.T) {
	tests := []struct {
		name string
		a, b any
	}{
		{"NilToNil", nil, nil},
		{"NilToVal", nil, 1},
		{"ValToNil", 1, nil},
		{"SameTypeDiffVal", 1, 2},
		{"DiffType", 1, "string"},
		{"SameTypeNestedDiff", map[string]int{"a": 1}, map[string]int{"a": 2}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := tt.a
			patch := MustDiff(a, tt.b)
			if tt.a == nil && tt.b == nil {
				if patch != nil {
					t.Errorf("Expected nil patch")
				}
				return
			}
			if patch == nil {
				t.Fatal("Expected patch")
			}
			patch.Apply(&a)
			if !reflect.DeepEqual(a, tt.b) {
				t.Errorf("Apply failed: expected %v, got %v", tt.b, a)
			}
		})
	}
}

func TestDiff_MapExhaustive(t *testing.T) {
	type S struct{ A int }
	tests := []struct {
		name string
		a, b map[string]S
	}{
		{
			"ModifiedValue",
			map[string]S{"a": {1}},
			map[string]S{"a": {2}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := tt.a
			patch := MustDiff(a, tt.b)
			patch.Apply(&a)
			if !reflect.DeepEqual(a, tt.b) {
				t.Errorf("Apply failed: expected %v, got %v", tt.b, a)
			}
		})
	}
}

type CustomTypeForDiffer struct {
	Value int
}

func (c CustomTypeForDiffer) Diff(other CustomTypeForDiffer) (Patch[CustomTypeForDiffer], error) {
	if c.Value == other.Value {
		return nil, nil
	}
	type internal CustomTypeForDiffer
	p := MustDiff(internal(c), internal{Value: other.Value + 1000})
	return &typedPatch[CustomTypeForDiffer]{
		inner:  p.(*typedPatch[internal]).inner,
		strict: true,
	}, nil
}

func TestDiff_CustomDiffer_ValueReceiver(t *testing.T) {
	a := CustomTypeForDiffer{Value: 10}
	b := CustomTypeForDiffer{Value: 20}

	patch := MustDiff(a, b)
	if patch == nil {
		t.Fatal("Expected patch")
	}

	patch.Apply(&a)

	expected := 20 + 1000
	if a.Value != expected {
		t.Errorf("Custom Diff method was not called correctly: expected %d, got %d", expected, a.Value)
	}
}

type CustomPtrTypeForDiffer struct {
	Value int
}

func (c *CustomPtrTypeForDiffer) Diff(other *CustomPtrTypeForDiffer) (Patch[*CustomPtrTypeForDiffer], error) {
	if (c == nil && other == nil) || (c != nil && other != nil && c.Value == other.Value) {
		return nil, nil
	}

	type internal CustomPtrTypeForDiffer
	p := MustDiff((*internal)(c), &internal{Value: other.Value + 5000})
	return &typedPatch[*CustomPtrTypeForDiffer]{
		inner:  p.(*typedPatch[*internal]).inner,
		strict: true,
	}, nil
}

func TestDiff_CustomDiffer_PointerReceiver(t *testing.T) {
	a := &CustomPtrTypeForDiffer{Value: 10}
	b := &CustomPtrTypeForDiffer{Value: 20}

	patch := MustDiff(a, b)
	if patch == nil {
		t.Fatal("Expected patch")
	}

	patch.Apply(&a)

	expected := 20 + 5000
	if a.Value != expected {
		t.Errorf("Custom Diff method (ptr receiver) was not called correctly: expected %d, got %d", expected, a.Value)
	}
}

type CustomErrorDiffer struct {
	Value int
}

func (c CustomErrorDiffer) Diff(other CustomErrorDiffer) (Patch[CustomErrorDiffer], error) {
	return nil, fmt.Errorf("custom error")
}

func TestDiff_CustomDiffer_ErrorCase(t *testing.T) {
	a := CustomErrorDiffer{Value: 1}
	b := CustomErrorDiffer{Value: 2}

	defer func() {
		if r := recover(); r == nil {
			t.Errorf("Expected panic due to custom error in Diff")
		} else {
			if fmt.Sprintf("%v", r) != "custom error" {
				t.Errorf("Expected panic 'custom error', got '%v'", r)
			}
		}
	}()

	MustDiff(a, b)
}

func TestDiff_CustomDiffer_ToJSONPatch(t *testing.T) {
	a := CustomTypeForDiffer{Value: 10}
	b := CustomTypeForDiffer{Value: 20}

	patch := MustDiff(a, b)
	if patch == nil {
		t.Fatal("Expected patch")
	}

	jsonPatch, err := patch.ToJSONPatch()
	if err != nil {
		t.Fatalf("ToJSONPatch failed: %v", err)
	}

	expected := `[{"op":"replace","path":"/Value","value":1020}]`
	if string(jsonPatch) != expected {
		t.Errorf("Expected JSON patch %s, got %s", expected, string(jsonPatch))
	}
}

type CustomNestedForJSON struct {
	Inner CustomTypeForDiffer
}

func TestDiff_CustomDiffer_ToJSONPatch_Nested(t *testing.T) {
	a := CustomNestedForJSON{Inner: CustomTypeForDiffer{Value: 10}}
	b := CustomNestedForJSON{Inner: CustomTypeForDiffer{Value: 20}}

	patch := MustDiff(a, b)
	if patch == nil {
		t.Fatal("Expected patch")
	}

	jsonPatch, err := patch.ToJSONPatch()
	if err != nil {
		t.Fatalf("ToJSONPatch failed: %v", err)
	}

	expected := `[{"op":"replace","path":"/Inner/Value","value":1020}]`
	if string(jsonPatch) != expected {
		t.Errorf("Expected JSON patch %s, got %s", expected, string(jsonPatch))
	}
}

func TestRegisterCustomDiff(t *testing.T) {
	type Custom struct {
		Val string
	}

	d := NewDiffer()
	RegisterCustomDiff(d, func(a, b Custom) (Patch[Custom], error) {
		if a.Val == b.Val {
			return nil, nil
		}
		builder := NewPatchBuilder[Custom]()
		node, _ := builder.Root().Field("Val")
		node.Put("CUSTOM:" + b.Val)
		return builder.Build()
	})

	c1 := Custom{Val: "old"}
	c2 := Custom{Val: "new"}

	patch := MustDiffUsing(d, c1, c2)
	if patch == nil {
		t.Fatal("Expected patch")
	}

	target := Custom{Val: "old"}
	patch.Apply(&target)

	if target.Val != "CUSTOM:new" {
		t.Errorf("Expected CUSTOM:new, got %s", target.Val)
	}
}

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
	b := []KeyedTask{
		{ID: "t2", Status: "done", Value: 2},
		{ID: "t1", Status: "todo", Value: 1},
	}

	patch := MustDiff(a, b)
	if patch == nil {
		t.Fatal("Expected patch")
	}

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

	patch := MustDiff(a, b)
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
	b := []KeyedTask{
		{ID: "t3", Status: "todo"},
		{ID: "t1", Status: "done"},
		{ID: "t4", Status: "new"},
	}

	patch := MustDiff(a, b)
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

func TestDiff_MoveDetection(t *testing.T) {
	type Document struct {
		Title   string
		Content string
	}

	type Workspace struct {
		Drafts  []Document
		Archive map[string]Document
	}

	doc := Document{
		Title:   "Move Test",
		Content: "Some content",
	}

	ws := Workspace{
		Drafts:  []Document{doc},
		Archive: make(map[string]Document),
	}

	target := Workspace{
		Drafts: []Document{},
		Archive: map[string]Document{
			"moved": doc,
		},
	}

	t.Run("Disabled", func(t *testing.T) {
		patch := MustDiff(ws, target, DiffDetectMoves(false))
		moveCount := 0
		patch.Walk(func(path string, op OpKind, old, new any) error {
			if op == OpMove {
				moveCount++
			}
			return nil
		})
		if moveCount != 0 {
			t.Errorf("Expected 0 moves when disabled, got %d", moveCount)
		}

		// Verify apply still works (via copy/delete)
		final, _ := Copy(ws)
		err := patch.ApplyChecked(&final)
		if err != nil {
			t.Fatalf("Apply failed: %v", err)
		}
		if len(final.Drafts) != 0 || len(final.Archive) != 1 || final.Archive["moved"].Title != doc.Title {
			t.Errorf("Final state incorrect: %+v", final)
		}
	})

	t.Run("Enabled", func(t *testing.T) {
		patch := MustDiff(ws, target, DiffDetectMoves(true))
		moveCount := 0
		var movePath string
		var moveFrom string
		patch.Walk(func(path string, op OpKind, old, new any) error {
			if op == OpMove {
				moveCount++
				movePath = path
				moveFrom = old.(string)
			}
			return nil
		})
		if moveCount != 1 {
			t.Errorf("Expected 1 move when enabled, got %d", moveCount)
		}
		if movePath != "/Archive/moved" || moveFrom != "/Drafts/0" {
			t.Errorf("Unexpected move: %s from %s", movePath, moveFrom)
		}

		// Verify apply works
		final, _ := Copy(ws)
		err := patch.ApplyChecked(&final)
		if err != nil {
			t.Fatalf("Apply failed: %v", err)
		}
		if len(final.Drafts) != 0 || len(final.Archive) != 1 || final.Archive["moved"].Title != doc.Title {
			t.Errorf("Final state incorrect: %+v", final)
		}
	})

	t.Run("MapToSlice", func(t *testing.T) {
		doc := &Document{
			Title:   "Move Test Ptr",
			Content: "Some content",
		}
		type WorkspacePtr struct {
			Drafts  []*Document
			Archive map[string]*Document
		}
		ws := WorkspacePtr{
			Drafts: []*Document{},
			Archive: map[string]*Document{
				"d1": doc,
			},
		}
		target := WorkspacePtr{
			Drafts:  []*Document{doc},
			Archive: map[string]*Document{},
		}

		patch := MustDiff(ws, target, DiffDetectMoves(true))
		moveCount := 0
		patch.Walk(func(path string, op OpKind, old, new any) error {
			if op == OpMove {
				moveCount++
			}
			return nil
		})
		if moveCount != 1 {
			t.Errorf("Expected 1 move, got %d", moveCount)
		}

		final, _ := Copy(ws)
		err := patch.ApplyChecked(&final)
		if err != nil {
			t.Fatalf("Apply failed: %v", err)
		}
		if len(final.Drafts) != 1 || len(final.Archive) != 0 || final.Drafts[0].Title != doc.Title {
			t.Errorf("Final state incorrect: %+v", final)
		}
	})
}

