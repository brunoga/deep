package deep

import (
	"fmt"
	"testing"
)

type CustomTypeForDiffer struct {
	Value int
}

func (c CustomTypeForDiffer) Diff(other CustomTypeForDiffer) (Patch[CustomTypeForDiffer], error) {
	if c.Value == other.Value {
		return nil, nil
	}
	// Return a patch that adds 100 to the value instead of just replacing it,
	// just to prove this method was called.

	// For testing purposes, let's just use a simple value replacement 
	// but with a recognizable change.
	type internal CustomTypeForDiffer
	p := Diff(internal(c), internal{Value: other.Value + 1000})
	return &typedPatch[CustomTypeForDiffer]{
		inner:  p.(*typedPatch[internal]).inner,
		strict: true,
	}, nil
}

func TestDiff_CustomDiffer_ValueReceiver(t *testing.T) {
	a := CustomTypeForDiffer{Value: 10}
	b := CustomTypeForDiffer{Value: 20}
	
	patch := Diff(a, b)
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
	p := Diff((*internal)(c), &internal{Value: other.Value + 5000})
	return &typedPatch[*CustomPtrTypeForDiffer]{
		inner:  p.(*typedPatch[*internal]).inner,
		strict: true,
	}, nil
}

func TestDiff_CustomDiffer_PointerReceiver(t *testing.T) {
	a := &CustomPtrTypeForDiffer{Value: 10}
	b := &CustomPtrTypeForDiffer{Value: 20}
	
	patch := Diff(a, b)
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
	
	Diff(a, b)
}

func TestDiff_CustomDiffer_ToJSONPatch(t *testing.T) {
	a := CustomTypeForDiffer{Value: 10}
	b := CustomTypeForDiffer{Value: 20}
	
	patch := Diff(a, b)
	if patch == nil {
		t.Fatal("Expected patch")
	}
	
	jsonPatch, err := patch.ToJSONPatch()
	if err != nil {
		t.Fatalf("ToJSONPatch failed: %v", err)
	}
	
	// We expect a replace operation for "/Value" because deep.Diff on a struct 
	// returns field-level diffs.
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
	
	patch := Diff(a, b)
	if patch == nil {
		t.Fatal("Expected patch")
	}
	
	jsonPatch, err := patch.ToJSONPatch()
	if err != nil {
		t.Fatalf("ToJSONPatch failed: %v", err)
	}
	
	// We expect a replace operation for "/Inner/Value"
	expected := `[{"op":"replace","path":"/Inner/Value","value":1020}]`
	if string(jsonPatch) != expected {
		t.Errorf("Expected JSON patch %s, got %s", expected, string(jsonPatch))
	}
}

type NestedCustom struct {
	C CustomTypeForDiffer
}

func TestDiff_CustomDiffer_Nested(t *testing.T) {
	a := NestedCustom{C: CustomTypeForDiffer{Value: 10}}
	b := NestedCustom{C: CustomTypeForDiffer{Value: 20}}
	
	patch := Diff(a, b)
	if patch == nil {
		t.Fatal("Expected patch")
	}
	
	patch.Apply(&a)
	
	expected := 20 + 1000
	if a.C.Value != expected {
		t.Errorf("Custom Diff method (nested) was not called correctly: expected %d, got %d", expected, a.C.Value)
	}
}
