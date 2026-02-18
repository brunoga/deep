package core

import (
	"reflect"
	"testing"
)

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
		path string
		parent string
		key string
		index int
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
