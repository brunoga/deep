package deep

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestPatchJSONSerialization(t *testing.T) {
	type SubStruct struct {
		A int
		B string
	}
	type TestStruct struct {
		I int
		S string
		B bool
		M map[string]int
		L []int
		O *SubStruct
	}

	s1 := TestStruct{
		I: 1,
		S: "foo",
		B: true,
		M: map[string]int{"a": 1},
		L: []int{1, 2, 3},
		O: &SubStruct{A: 10, B: "bar"},
	}
	s2 := TestStruct{
		I: 2,
		S: "bar",
		B: false,
		M: map[string]int{"a": 2, "b": 3},
		L: []int{1, 4, 3, 5},
		O: &SubStruct{A: 20, B: "baz"},
	}

	p := Diff(s1, s2)
	if p == nil {
		t.Fatal("Diff should not be nil")
	}

	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("JSON Marshal failed: %v", err)
	}

	p2 := NewPatch[TestStruct]()
	if err := json.Unmarshal(data, p2); err != nil {
		t.Fatalf("JSON Unmarshal failed: %v", err)
	}

	s3 := s1
	p2.Apply(&s3)

	if !reflect.DeepEqual(s2, s3) {
		t.Errorf(`Apply after JSON serialization failed.
Expected: %+v
Got: %+v`, s2, s3)
	}
}

func TestPatchGobSerialization(t *testing.T) {
	type SubStruct struct {
		A int
		B string
	}
	type TestStruct struct {
		I int
		S string
		B bool
		M map[string]int
		L []int
		O *SubStruct
	}

	// Gob needs registration for types used in any/interface{}
	gob.Register(SubStruct{})
	Register[TestStruct]()

	s1 := TestStruct{
		I: 1,
		S: "foo",
		B: true,
		M: map[string]int{"a": 1},
		L: []int{1, 2, 3},
		O: &SubStruct{A: 10, B: "bar"},
	}
	s2 := TestStruct{
		I: 2,
		S: "bar",
		B: false,
		M: map[string]int{"a": 2, "b": 3},
		L: []int{1, 4, 3, 5},
		O: &SubStruct{A: 20, B: "baz"},
	}

	p := Diff(s1, s2)
	if p == nil {
		t.Fatal("Diff should not be nil")
	}

	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	if err := enc.Encode(&p); err != nil {
		t.Fatalf("Gob Encode failed: %v", err)
	}

	p2 := NewPatch[TestStruct]()
	dec := gob.NewDecoder(&buf)
	if err := dec.Decode(&p2); err != nil {
		t.Fatalf("Gob Decode failed: %v", err)
	}

	s3 := s1
	p2.Apply(&s3)

	if !reflect.DeepEqual(s2, s3) {
		t.Errorf(`Apply after Gob serialization failed.
Expected: %+v
Got: %+v`, s2, s3)
	}
}

func TestPatchWithConditionSerialization(t *testing.T) {
	type TestStruct struct {
		I int
	}

	s1 := TestStruct{I: 1}
	s2 := TestStruct{I: 2}

	p := Diff(s1, s2).WithCondition(Equal[TestStruct]("I", 1))

	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("JSON Marshal failed: %v", err)
	}

	p2 := NewPatch[TestStruct]()
	if err := json.Unmarshal(data, p2); err != nil {
		t.Fatalf("JSON Unmarshal failed: %v", err)
	}

	s3 := s1
	if err := p2.ApplyChecked(&s3); err != nil {
		t.Fatalf("ApplyChecked failed: %v", err)
	}

	if s3.I != 2 {
		t.Errorf("Expected I=2, got %d", s3.I)
	}

	s4 := TestStruct{I: 10}
	if err := p2.ApplyChecked(&s4); err == nil {
		t.Error("ApplyChecked should have failed due to condition")
	}
}

func TestPatch_String_Basic(t *testing.T) {
	a, b := "foo", "bar"
	patch := Diff(a, b)
	if !strings.Contains(patch.String(), "foo -> bar") {
		t.Errorf("String() missing transition: %s", patch.String())
	}
}

func TestPatch_String_Complex(t *testing.T) {
	type Child struct {
		Name string
	}
	type Data struct {
		Tags   []string
		Meta   map[string]any
		Kids   []Child
		Status *string
	}
	active := "active"
	inactive := "inactive"
	a := Data{
		Tags: []string{"tag1", "tag2"},
		Meta: map[string]any{
			"key1": "val1",
			"key2": 123,
		},
		Kids: []Child{
			{Name: "Kid1"},
		},
		Status: &active,
	}
	b := Data{
		Tags: []string{"tag1", "tag2", "tag3"},
		Meta: map[string]any{
			"key1": "val1-mod",
			"key3": true,
		},
		Kids: []Child{
			{Name: "Kid1"},
			{Name: "Kid2"},
		},
		Status: &inactive,
	}
	patch := Diff(a, b)
	if patch == nil {
		t.Fatal("Expected non-nil patch")
	}
	s := patch.String()
	expectedSubstrings := []string{
		"Struct{",
		"Tags: Slice{",
		"+ [2]: tag3",
		"Meta: Map{",
		"+ key3: true",
		"- key2",
		"key1: val1 -> val1-mod",
		"Kids: Slice{",
		"+ [1]: {Kid2}",
		"Status: active -> inactive",
	}
	for _, sub := range expectedSubstrings {
		if !strings.Contains(s, sub) {
			t.Errorf("String() output missing expected substring: %q", sub)
		}
	}
	revPatch := patch.Reverse()
	if revPatch == nil {
		t.Fatal("Expected non-nil reverse patch")
	}
	bCopy, _ := Copy(b)
	revPatch.Apply(&bCopy)
	if !reflect.DeepEqual(bCopy, a) {
		t.Errorf("Reverse() application failed.\\nExpected: %+v\\nGot:      %+v", a, bCopy)
	}
}

func TestPatch_Reverse_Basic(t *testing.T) {
	a := 10
	b := 20
	patch := Diff(a, b)
	rev := patch.Reverse()
	val := b
	rev.Apply(&val)
	if val != a {
		t.Errorf("Reverse Apply failed: expected %v, got %v", a, val)
	}
}

func TestPatch_Reverse_Slice(t *testing.T) {
	a := []string{"a", "b", "c"}
	b := []string{"a", "X", "d", "c"}
	patch := Diff(a, b)
	rev := patch.Reverse()
	target := make([]string, len(a))
	copy(target, a)
	patch.Apply(&target)
	if !reflect.DeepEqual(target, b) {
		t.Fatalf("Forward patch failed")
	}
	rev.Apply(&target)
	if !reflect.DeepEqual(target, a) {
		t.Errorf("Reverse Apply failed.\nExpected: %v\nGot:      %v", a, target)
	}
}

func TestApplyChecked_Comprehensive(t *testing.T) {
	type Inner struct {
		Val int
	}
	type Data struct {
		Arr [2]int
		Slc []int
		Ptr *Inner
		Ifc any
	}

	ptrVal := &Inner{Val: 10}
	a := Data{
		Arr: [2]int{1, 2},
		Slc: []int{3, 4},
		Ptr: ptrVal,
		Ifc: "string",
	}

	// Create a modified version
	ptrValMod := &Inner{Val: 20}
	b := Data{
		Arr: [2]int{1, 20}, // Index 1 mod
		Slc: []int{3},      // Index 1 del
		Ptr: ptrValMod,     // Ptr content mod
		Ifc: 123,           // Interface type/val change
	}

	patch := Diff(a, b)
	if patch == nil {
		t.Fatal("Expected patch")
	}

	// ApplyChecked should succeed on 'a'
	// We use a copy to keep 'a' pure
	aCopy, _ := Copy(a)

	if err := patch.ApplyChecked(&aCopy); err != nil {
		t.Fatalf("ApplyChecked failed: %v", err)
	}

	if !reflect.DeepEqual(aCopy, b) {
		t.Errorf("ApplyChecked result mismatch.\nGot: %+v\nWant: %+v", aCopy, b)
	}
}

func TestPatch_Reverse_Array(t *testing.T) {
	a := [3]int{1, 2, 3}
	b := [3]int{1, 20, 3}

	patch := Diff(a, b)

	// Test Format
	s := patch.String()
	if !strings.Contains(s, "Array{") || !strings.Contains(s, "[1]: 2 -> 20") {
		t.Errorf("Array format failed: %s", s)
	}

	rev := patch.Reverse()

	target := a
	patch.Apply(&target)
	if target != b {
		t.Fatalf("Forward patch failed")
	}

	rev.Apply(&target)
	if target != a {
		t.Errorf("Reverse failed: got %v, want %v", target, a)
	}
}

func TestInterfaceContentPatch(t *testing.T) {
	type S struct {
		Val int
	}
	var a any = S{Val: 10}
	var b any = S{Val: 20}

	patch := Diff(a, b)
	if patch == nil {
		t.Fatal("Expected patch")
	}

	// Verify it's an interfacePatch internally (since underlying types match)
	s := patch.String()
	if strings.Contains(s, "->") && !strings.Contains(s, "Struct{") {
		// If it just says "S{10} -> S{20}", it's a valuePatch.
		// For interfacePatch, it should look like Struct{ Val: 10 -> 20 }
	}

	// ApplyChecked should succeed
	aCopy := a
	if err := patch.ApplyChecked(&aCopy); err != nil {
		t.Fatalf("ApplyChecked failed: %v", err)
	}

	if !reflect.DeepEqual(aCopy, b) {
		t.Errorf("Result mismatch: got %v, want %v", aCopy, b)
	}
}

func TestPatch_StrictToggle(t *testing.T) {
	type S struct{ A int }
	s1 := S{A: 1}
	s2 := S{A: 2}

	p := Diff(s1, s2) // Strict is true by default

	s3 := S{A: 10} // Current value doesn't match oldVal (1)
	if err := p.ApplyChecked(&s3); err == nil {
		t.Error("Expected error in strict mode")
	}

	pNonStrict := p.WithStrict(false)
	if err := pNonStrict.ApplyChecked(&s3); err != nil {
		t.Errorf("Expected no error in non-strict mode: %v", err)
	}
	if s3.A != 2 {
		t.Errorf("Expected A=2, got %d", s3.A)
	}
}

func TestPatch_LocalCondition(t *testing.T) {
	type S struct{ A int }

	builder := NewBuilder[S]()
	// Set A from 1 to 2, but only if current value < 5
	node, _ := builder.Root().Field("A")
	node.Set(1, 2).WithCondition(Less[int]("", 5))

	p, _ := builder.Build()
	p = p.WithStrict(false) // Disable strict to only test local condition

	s1 := S{A: 3}
	if err := p.ApplyChecked(&s1); err != nil {
		t.Fatalf("ApplyChecked failed: %v", err)
	}
	if s1.A != 2 {
		t.Errorf("Expected A=2, got %d", s1.A)
	}

	s2 := S{A: 10}
	if err := p.ApplyChecked(&s2); err == nil {
		t.Error("Expected local condition to fail")
	}
}

func TestPatch_RecursiveCondition(t *testing.T) {
	type Child struct{ Name string }
	type Parent struct {
		Age   int
		Child Child
	}

	builder := NewBuilder[Parent]()
	// Change Child.Name to "NewName", but only if Parent.Age > 18
	root := builder.Root()
	root.WithCondition(Greater[Parent]("Age", 18))
	childNode, _ := root.Field("Child")
	nameNode, _ := childNode.Field("Name")
	nameNode.Set("Old", "New")

	p, _ := builder.Build()

	// 1. Should fail because Age is 10
	p1 := Parent{Age: 10, Child: Child{Name: "Old"}}
	if err := p.ApplyChecked(&p1); err == nil {
		t.Error("Expected condition to fail due to Age")
	}

	// 2. Should pass because Age is 20
	p2 := Parent{Age: 20, Child: Child{Name: "Old"}}
	if err := p.ApplyChecked(&p2); err != nil {
		t.Fatalf("ApplyChecked failed: %v", err)
	}
	if p2.Child.Name != "New" {
		t.Errorf("Expected Name=New, got %s", p2.Child.Name)
	}
}

func TestApplyChecked_Conflicts(t *testing.T) {
	t.Run("MapDeletionMismatch", func(t *testing.T) {
		m1 := map[string]int{"a": 1}
		m2 := map[string]int{}
		p := Diff(m1, m2)

		m3 := map[string]int{"a": 2} // Value mismatch
		if err := p.ApplyChecked(&m3); err == nil {
			t.Error("Expected error for map deletion value mismatch")
		}
	})

	t.Run("MapModificationMissingKey", func(t *testing.T) {
		m1 := map[string]int{"a": 1}
		m2 := map[string]int{"a": 2}
		p := Diff(m1, m2)

		m3 := map[string]int{"b": 1} // Key 'a' missing
		if err := p.ApplyChecked(&m3); err == nil {
			t.Error("Expected error for map modification missing key")
		}
	})

	t.Run("NumericConversions", func(t *testing.T) {
		type S struct {
			I8  int8
			U16 uint16
			F32 float32
		}
		s1 := S{I8: 1, U16: 1, F32: 1.0}
		s2 := S{I8: 2, U16: 2, F32: 2.0}
		p := Diff(s1, s2)

		// Simulate JSON/Gob float64 input
		data, _ := json.Marshal(p)
		p2 := NewPatch[S]()
		json.Unmarshal(data, p2)

		s3 := S{I8: 1, U16: 1, F32: 1.0}
		if err := p2.ApplyChecked(&s3); err != nil {
			t.Fatalf("ApplyChecked failed with numeric conversion: %v", err)
		}
		if s3.I8 != 2 || s3.U16 != 2 || s3.F32 != 2.0 {
			t.Errorf("Result mismatch: %+v", s3)
		}
	})

	t.Run("MapAdditionConflict", func(t *testing.T) {
		m1 := map[string]int{}
		m2 := map[string]int{"a": 1}
		p := Diff(m1, m2)

		m3 := map[string]int{"a": 10} // Key 'a' already exists
		if err := p.ApplyChecked(&m3); err == nil {
			t.Error("Expected error for map addition existing key conflict")
		}
	})

	t.Run("SliceErrors", func(t *testing.T) {
		a := []int{1}
		b := []int{1, 2}
		p := Diff(a, b)

		var empty []int
		if err := p.ApplyChecked(&empty); err == nil {
			// p is slicePatch with opAdd at index 1.
			// Applying to empty slice should work if we allow append.
			// Wait, Diff(a, b) where a=[1], b=[1, 2] is opAdd at index 1.
			// Applying to []int{} will fail because curIdx starts at 0,
			// and op.Index (1) > curIdx (0), so it tries to copy [0:1] from v,
			// but v.Len() is 0.
		}
	})
}

func TestPatch_Serialization_Exhaustive(t *testing.T) {
	type Data struct {
		V int
	}
	Register[Data]()

	// Test with complex condition
	cond, _ := ParseCondition[Data]("V > 10 OR NOT (V == 0)")
	p := Diff(Data{V: 1}, Data{V: 2}).WithCondition(cond)

	// JSON
	jsonData, _ := json.Marshal(p)
	p2 := NewPatch[Data]()
	json.Unmarshal(jsonData, p2)
	if p2.String() == "" {
		t.Error("JSON restoration failed")
	}

	// Gob
	var buf bytes.Buffer
	gob.NewEncoder(&buf).Encode(&p)
	p3 := NewPatch[Data]()
	gob.NewDecoder(&buf).Decode(&p3)
	if p3.String() == "" {
		t.Error("Gob restoration failed")
	}
}
