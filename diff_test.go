package deep

import (
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
			patch := Diff(tt.a, tt.b)
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
	patch := Diff(a, b)
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
			patch := Diff(input, target)
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
			patch := Diff(a, tt.b)
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
			patch := Diff(a, tt.b)
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
	patch := Diff(a, b)
	patch.Apply(&a)
	if a != b {
		t.Errorf("Apply failed: expected %v, got %v", b, a)
	}
}

func TestDiff_Interface(t *testing.T) {
	var a any = 1
	var b any = 2
	patch := Diff(a, b)
	patch.Apply(&a)
	if a != b {
		t.Errorf("Apply failed: expected %v, got %v", b, a)
	}
	b = "string"
	patch = Diff(a, b)
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
	patch := Diff(a, b)
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
	patch := Diff(a, b)
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
			patch := Diff(a, tt.b)
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
			patch := Diff(a, tt.b)
			patch.Apply(&a)
			if !reflect.DeepEqual(a, tt.b) {
				t.Errorf("Apply failed: expected %v, got %v", tt.b, a)
			}
		})
	}
}
