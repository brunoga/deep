package core

import (
	"reflect"
	"testing"
)

// --- Fix: Navigate handles non-numeric keys in keyed slices ---

type keyedItem struct {
	Name  string `deep:"key"`
	Value int
}

func TestNavigate_KeyedSlice_NonNumericKey(t *testing.T) {
	items := []keyedItem{
		{Name: "todo", Value: 1},
		{Name: "in-progress", Value: 2},
		{Name: "done", Value: 3},
	}
	v := reflect.ValueOf(items)

	for _, tc := range []struct {
		key  string
		want int
	}{
		{"todo", 1},
		{"in-progress", 2},
		{"done", 3},
	} {
		val, err := DeepPath("/" + tc.key).Resolve(v)
		if err != nil {
			t.Errorf("Resolve /%s: unexpected error: %v", tc.key, err)
			continue
		}
		got := int(val.FieldByName("Value").Int())
		if got != tc.want {
			t.Errorf("Resolve /%s: got Value=%d, want %d", tc.key, got, tc.want)
		}
	}
}

// --- Fix: Set/Delete on values nested inside maps ---

type mapInner struct{ X int }
type mapOuter struct{ M map[string]mapInner }

func TestSet_MapNestedValue(t *testing.T) {
	o := mapOuter{M: map[string]mapInner{"k": {X: 1}}}
	v := reflect.ValueOf(&o).Elem()

	if err := DeepPath("/M/k/X").Set(v, reflect.ValueOf(42)); err != nil {
		t.Fatalf("Set /M/k/X: %v", err)
	}
	if got := o.M["k"].X; got != 42 {
		t.Errorf("after Set /M/k/X: got %d, want 42", got)
	}
}

func TestDelete_MapNestedValue(t *testing.T) {
	o := mapOuter{M: map[string]mapInner{"k": {X: 99}}}
	v := reflect.ValueOf(&o).Elem()

	if err := DeepPath("/M/k/X").Delete(v); err != nil {
		t.Fatalf("Delete /M/k/X: %v", err)
	}
	if got := o.M["k"].X; got != 0 {
		t.Errorf("after Delete /M/k/X: got %d, want 0 (zero value)", got)
	}
}

func TestSet_MapNestedValue_DeepPath(t *testing.T) {
	// Three levels deep: struct → map → struct → field.
	type level2 struct{ Z string }
	type level1 struct{ Inner map[string]level2 }
	type root struct{ Outer level1 }

	r := root{Outer: level1{Inner: map[string]level2{"key": {Z: "old"}}}}
	v := reflect.ValueOf(&r).Elem()

	if err := DeepPath("/Outer/Inner/key/Z").Set(v, reflect.ValueOf("new")); err != nil {
		t.Fatalf("Set deep map path: %v", err)
	}
	if got := r.Outer.Inner["key"].Z; got != "new" {
		t.Errorf("got %q, want %q", got, "new")
	}
}

// --- Fix: makeMapKey handles uint map key types ---

func TestSet_UintMapKey(t *testing.T) {
	m := map[uint]string{1: "one"}
	v := reflect.ValueOf(&m).Elem()

	if err := DeepPath("/2").Set(v, reflect.ValueOf("two")); err != nil {
		t.Fatalf("Set uint map key: %v", err)
	}
	if got := m[2]; got != "two" {
		t.Errorf("got %q, want %q", got, "two")
	}
}

func TestDelete_UintMapKey(t *testing.T) {
	m := map[uint]string{7: "seven"}
	v := reflect.ValueOf(&m).Elem()

	if err := DeepPath("/7").Delete(v); err != nil {
		t.Fatalf("Delete uint map key: %v", err)
	}
	if _, ok := m[7]; ok {
		t.Error("key 7 still present after Delete")
	}
}

func TestDeepPath_Resolve(t *testing.T) {
	type S struct {
		A int
		B []int
	}
	s := S{A: 1, B: []int{2}}
	v := reflect.ValueOf(s)

	// JSON Pointer
	val, err := DeepPath("/A").Resolve(v)
	if err != nil || val.Int() != 1 {
		t.Errorf("Resolve /A failed: %v, %v", val, err)
	}

	val, err = DeepPath("/B/0").Resolve(v)
	if err != nil || val.Int() != 2 {
		t.Errorf("Resolve /B/0 failed: %v, %v", val, err)
	}
}

func TestDeepPath_ResolveParentPath(t *testing.T) {
	tests := []struct {
		path    string
		parent  string
		key     string
		index   int
		isIndex bool
	}{
		{"/A/B", "/A", "B", 0, false},
		{"/A/0", "/A", "", 0, true},
		{"/A/B", "/A", "B", 0, false},
	}

	for _, tt := range tests {
		parent, part, err := DeepPath(tt.path).ResolveParentPath()
		if err != nil {
			t.Errorf("ResolveParentPath(%s) failed: %v", tt.path, err)
			continue
		}
		if string(parent) != tt.parent {
			t.Errorf("Parent mismatch: got %s, want %s", parent, tt.parent)
		}
		if part.IsIndex != tt.isIndex {
			t.Errorf("IsIndex mismatch: got %v, want %v", part.IsIndex, tt.isIndex)
		}
		if part.IsIndex {
			if part.Index != tt.index {
				t.Errorf("Index mismatch: got %d, want %d", part.Index, tt.index)
			}
		} else {
			if part.Key != tt.key {
				t.Errorf("Key mismatch: got %s, want %s", part.Key, tt.key)
			}
		}
	}
}
