package deep

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

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
		t.Errorf("Reverse() application failed.\nExpected: %+v\nGot:      %+v", a, bCopy)
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
	root := builder.Root()
	root.WithCondition(Greater[Parent]("Age", 18))
	childNode, _ := root.Field("Child")
	nameNode, _ := childNode.Field("Name")
	nameNode.Set("Old", "New")

	p, _ := builder.Build()

	p1 := Parent{Age: 10, Child: Child{Name: "Old"}}
	if err := p.ApplyChecked(&p1); err == nil {
		t.Error("Expected condition to fail due to Age")
	}

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
		}
	})
}

func TestPatch_ToJSONPatch(t *testing.T) {
	type User struct {
		Name string
		Age  int
		Tags []string
	}
	u1 := User{Name: "Alice", Age: 30, Tags: []string{"A", "B"}}
	u2 := User{Name: "Alice", Age: 31, Tags: []string{"A", "C", "B"}}

	patch := Diff(u1, u2)
	jsonPatchBytes, err := patch.ToJSONPatch()
	if err != nil {
		t.Fatalf("ToJSONPatch failed: %v", err)
	}

	var ops []map[string]any
	if err := json.Unmarshal(jsonPatchBytes, &ops); err != nil {
		t.Fatalf("Failed to unmarshal JSON Patch: %v", err)
	}

	foundAge := false
	foundTags := false
	for _, op := range ops {
		switch op["path"] {
		case "/Age":
			if op["op"] == "replace" && op["value"] == float64(31) {
				foundAge = true
			}
		case "/Tags/1":
			if op["op"] == "add" && op["value"] == "C" {
				foundTags = true
			}
		}
	}

	if !foundAge {
		t.Errorf("Expected replace /Age op, got: %s", string(jsonPatchBytes))
	}
	if !foundTags {
		t.Errorf("Expected add /Tags/1 op, got: %s", string(jsonPatchBytes))
	}
}

func TestPatch_ToJSONPatch_WithConditions(t *testing.T) {
	type User struct {
		Name string
		Age  int
	}
	builder := NewBuilder[User]()
	c := Equal[User]("Name", "Alice")
	nodeAge, _ := builder.Root().Field("Age")
	nodeAge.If(c).Set(30, 31)

	patch, _ := builder.Build()
	data, err := patch.ToJSONPatch()
	if err != nil {
		t.Fatalf("ToJSONPatch failed: %v", err)
	}

	var ops []map[string]any
	json.Unmarshal(data, &ops)

	if len(ops) != 1 {
		t.Fatalf("Expected 1 op, got %d", len(ops))
	}

	op := ops[0]
	if op["if"] == nil {
		t.Fatal("Expected 'if' predicate in JSON Patch export")
	}

	pred := op["if"].(map[string]any)
	if pred["op"] != "test" || pred["path"] != "Name" || pred["value"] != "Alice" {
		t.Errorf("Unexpected predicate: %+v", pred)
	}
}

type dummyPatch struct{ basePatch }

func (p *dummyPatch) apply(root, v reflect.Value)                           {}
func (p *dummyPatch) applyChecked(root, v reflect.Value, strict bool) error { return nil }
func (p *dummyPatch) applyResolved(root, v reflect.Value, path string, resolver ConflictResolver) error {
	return nil
}
func (p *dummyPatch) reverse() diffPatch       { return p }
func (p *dummyPatch) format(indent int) string { return "" }
func (p *dummyPatch) walk(path string, fn func(path string, op OpKind, old, new any) error) error {
	return nil
}
func (p *dummyPatch) toJSONPatch(path string) []map[string]any { return nil }

func TestPatch_MarshalUnknown(t *testing.T) {
	_, err := marshalDiffPatch(&dummyPatch{})
	if err == nil {
		t.Error("Expected error for unknown patch type")
	}
}

func TestPatch_ApplySimple(t *testing.T) {
	val := 1
	patch := Diff(1, 2)
	patch.Apply(&val)
	if val != 2 {
		t.Errorf("Apply failed: expected 2, got %d", val)
	}
}

func TestPatch_ConditionsExhaustive(t *testing.T) {
	type InnerC struct{ V int }
	type DataC struct {
		A   int
		P   *InnerC
		I   any
		M   map[string]InnerC
		S   []InnerC
		Arr [1]InnerC
	}
	builder := NewBuilder[DataC]()
	root := builder.Root()

	c := Equal[DataC]("A", 1)

	root.If(c).Unless(c).Test(DataC{A: 1})

	nodeP, _ := root.Field("P")
	nodeP.If(c).Unless(c)

	nodeI, _ := root.Field("I")
	nodeI.If(c).Unless(c)

	nodeM, _ := root.Field("M")
	nodeM.If(c).Unless(c)

	nodeS, _ := root.Field("S")
	nodeS.If(c).Unless(c)

	nodeArr, _ := root.Field("Arr")
	nodeArr.If(c).Unless(c)

	patch, _ := builder.Build()
	if patch == nil {
		t.Fatal("Build failed")
	}
}

func TestPatch_MoreApplyChecked(t *testing.T) {
	// ptrPatch
	t.Run("ptrPatch", func(t *testing.T) {
		val1 := 1
		p1 := &val1
		val2 := 2
		p2 := &val2
		patch := Diff(p1, p2)
		if err := patch.ApplyChecked(&p1); err != nil {
			t.Errorf("ptrPatch ApplyChecked failed: %v", err)
		}
	})
	// interfacePatch
	t.Run("interfacePatch", func(t *testing.T) {
		var i1 any = 1
		var i2 any = 2
		patch := Diff(i1, i2)
		if err := patch.ApplyChecked(&i1); err != nil {
			t.Errorf("interfacePatch ApplyChecked failed: %v", err)
		}
	})
	// structPatch
	t.Run("structPatch", func(t *testing.T) {
		type SLocal struct{ A int }
		s1 := SLocal{1}
		s2 := SLocal{2}
		patch := Diff(s1, s2)
		if err := patch.ApplyChecked(&s1); err != nil {
			t.Errorf("structPatch ApplyChecked failed: %v", err)
		}
	})
	// arrayPatch
	t.Run("arrayPatch", func(t *testing.T) {
		a1 := [1]int{1}
		a2 := [1]int{2}
		patch := Diff(a1, a2)
		if err := patch.ApplyChecked(&a1); err != nil {
			t.Errorf("arrayPatch ApplyChecked failed: %v", err)
		}
	})
	// mapPatch
	t.Run("mapPatch", func(t *testing.T) {
		m1 := map[string]int{"a": 1}
		m2 := map[string]int{"a": 2}
		patch := Diff(m1, m2)
		if err := patch.ApplyChecked(&m1); err != nil {
			t.Errorf("mapPatch ApplyChecked failed: %v", err)
		}
	})
	// testPatch
	t.Run("testPatch", func(t *testing.T) {
		val := 1
		builder := NewBuilder[int]()
		builder.Root().Test(1)
		patch, _ := builder.Build()
		if err := patch.ApplyChecked(&val); err != nil {
			t.Errorf("testPatch ApplyChecked failed: %v", err)
		}
	})
}

func TestPatch_Apply_DocumentWide(t *testing.T) {
	type Data struct {
		A int
		B int
	}
	d := Data{A: 1, B: 0}
	rv := reflect.ValueOf(&d)

	// testPatch apply
	tp := &testPatch{expected: reflect.ValueOf(1)}
	tp.apply(rv, rv.Elem().Field(0))

	// copyPatch apply
	cp := &copyPatch{from: "/A"}
	cp.apply(rv, rv.Elem().Field(1))
	if d.B != 1 {
		t.Errorf("copyPatch apply failed: expected B=1, got %d", d.B)
	}

	// movePatch apply
	d.B = 0
	mp := &movePatch{from: "/A", path: "/B"}
	mp.apply(rv, rv.Elem().Field(1))
	if d.B != 1 || d.A != 0 {
		t.Errorf("movePatch apply failed: %+v", d)
	}
}

func TestPatch_ConditionToPredicate_Exhaustive(t *testing.T) {
	type Data struct{ V int }

	ops := []string{"==", "!=", "<", ">", "<=", ">="}
	for _, op := range ops {
		expr := fmt.Sprintf("V %s 10", op)
		cond, _ := ParseCondition[Data](expr)

		pred := conditionToPredicate(cond)
		if pred == nil {
			t.Errorf("conditionToPredicate returned nil for %s", op)
		}
	}

	c1, _ := ParseCondition[Data]("V > 0 AND V < 10")
	conditionToPredicate(c1)

	c2, _ := ParseCondition[Data]("V == 0 OR V == 10")
	conditionToPredicate(c2)

	c3, _ := ParseCondition[Data]("NOT (V == 0)")
	conditionToPredicate(c3)
}

func TestPatch_ApplyCheckedRecursive(t *testing.T) {
	type InnerR struct{ V int }
	type DataR struct {
		P *InnerR
		I any
		M map[string]InnerR
		S []InnerR
		A [1]InnerR
	}

	d1 := DataR{
		P: &InnerR{1},
		I: InnerR{2},
		M: map[string]InnerR{"a": {3}},
		S: []InnerR{{4}},
		A: [1]InnerR{{5}},
	}
	d2 := DataR{
		P: &InnerR{10},
		I: InnerR{20},
		M: map[string]InnerR{"a": {30}},
		S: []InnerR{{40}},
		A: [1]InnerR{{50}},
	}

	patch := Diff(d1, d2)
	if err := patch.ApplyChecked(&d1); err != nil {
		t.Fatalf("ApplyChecked failed: %v", err)
	}
	if !reflect.DeepEqual(d1, d2) {
		t.Errorf("ApplyChecked mismatch: %+v", d1)
	}
}

func TestPatch_ToJSONPatch_Complex(t *testing.T) {
	type Inner struct{ V int }
	type Data struct {
		P *Inner
		I any
		A [1]Inner
		M map[string]Inner
	}

	builder := NewBuilder[Data]()
	root := builder.Root()

	nodeP, _ := root.Field("P")
	nodePV, _ := nodeP.Elem().Field("V")
	nodePV.Set(1, 2)

	nodeI, _ := root.Field("I")
	nodeI.Elem().Set(1, 2)

	nodeA, _ := root.Field("A")
	nodeAI, _ := nodeA.Index(0)
	nodeAIV, _ := nodeAI.Field("V")
	nodeAIV.Set(1, 2)

	nodeM, _ := root.Field("M")
	nodeMK, _ := nodeM.MapKey("k")
	nodeMKV, _ := nodeMK.Field("V")
	nodeMKV.Set(1, 2)

	patch, _ := builder.Build()
	patch.ToJSONPatch()
}

func TestPatch_LogExhaustive(t *testing.T) {
	lp := &logPatch{message: "test"}

	lp.apply(reflect.Value{}, reflect.ValueOf(1))

	if err := lp.applyChecked(reflect.ValueOf(1), reflect.ValueOf(1), false); err != nil {
		t.Errorf("logPatch applyChecked failed: %v", err)
	}

	if lp.reverse() != lp {
		t.Error("logPatch reverse should return itself")
	}

	if lp.format(0) == "" {
		t.Error("logPatch format returned empty string")
	}

	ops := lp.toJSONPatch("/path")
	if len(ops) != 1 || ops[0]["op"] != "log" {
		t.Errorf("Unexpected toJSONPatch output: %+v", ops)
	}
}
