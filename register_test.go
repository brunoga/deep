package deep

import (
	"testing"
)

type CustomRegistered struct {
	ID   int
	Data string
}

func TestRegisterCustomEqual(t *testing.T) {
	// Register a custom equality function that ignores Data field
	RegisterCustomEqual(func(a, b CustomRegistered) bool {
		return a.ID == b.ID
	})

	a := CustomRegistered{ID: 1, Data: "A"}
	b := CustomRegistered{ID: 1, Data: "B"}

	if !Equal(a, b) {
		t.Error("Expected equal based on ID only")
	}

	c := CustomRegistered{ID: 2, Data: "A"}
	if Equal(a, c) {
		t.Error("Expected not equal based on ID")
	}
}

func TestRegisterCustomCopy(t *testing.T) {
	// Register a custom copy function that modifies Data
	RegisterCustomCopy(func(src CustomRegistered) (CustomRegistered, error) {
		dst := src
		dst.Data = "Copied: " + src.Data
		return dst, nil
	})

	src := CustomRegistered{ID: 1, Data: "Original"}
	dst, err := Copy(src)
	if err != nil {
		t.Fatalf("Copy failed: %v", err)
	}

	if dst.Data != "Copied: Original" {
		t.Errorf("Custom copy logic not applied, got: %s", dst.Data)
	}
}

func TestRegisterCustomDiff_Example(t *testing.T) {
	// Custom diff that always returns a specific atomic patch
	RegisterCustomDiff(func(a, b CustomRegistered) (Patch[CustomRegistered], error) {
		if a.ID == b.ID {
			return nil, nil
		}
		// Return atomic patch replacing entire struct
		bld := NewPatchBuilder[CustomRegistered]()
		bld.Set(a, b)
		return bld.Build()
	})

	a := CustomRegistered{ID: 1, Data: "A"}
	b := CustomRegistered{ID: 2, Data: "B"}

	patch := MustDiff(a, b)
	if patch == nil {
		t.Fatal("Expected patch")
	}
	
	// Verify it's atomic (1 op)
	ops := 0
	patch.Walk(func(path string, op OpKind, old, new any) error {
		ops++
		return nil
	})
	if ops != 1 {
		t.Errorf("Expected 1 atomic op, got %d", ops)
	}
}
