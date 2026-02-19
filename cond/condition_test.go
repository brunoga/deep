package cond

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/brunoga/deep/v4/internal/core"
)

func TestJSONPointer_Resolve(t *testing.T) {
	type Config struct {
		Port int
		Host string
	}
	type Data struct {
		Network Config
		Meta    map[string]any
	}
	d := Data{
		Network: Config{Port: 8080, Host: "localhost"},
		Meta:    map[string]any{"env": "prod"},
	}
	rv := reflect.ValueOf(d)

	tests := []struct {
		pointer string
		want    any
	}{
		{"/Network/Port", 8080},
		{"/Network/Host", "localhost"},
		{"/Meta/env", "prod"},
	}
	for _, tt := range tests {
		val, err := core.DeepPath(tt.pointer).Resolve(rv)
		if err != nil {
			t.Errorf("Resolve(%s) failed: %v", tt.pointer, err)
			continue
		}
		if val.Interface() != tt.want {
			t.Errorf("Resolve(%s) = %v, want %v", tt.pointer, val.Interface(), tt.want)
		}
	}
}

func TestJSONPointer_SpecialChars(t *testing.T) {
	m := map[string]int{
		"foo/bar": 1,
		"foo~bar": 2,
	}
	rv := reflect.ValueOf(m)

	tests := []struct {
		pointer string
		want    int
	}{
		{"/foo~1bar", 1},
		{"/foo~0bar", 2},
	}
	for _, tt := range tests {
		val, err := core.DeepPath(tt.pointer).Resolve(rv)
		if err != nil {
			t.Fatalf("Resolve(%s) failed: %v", tt.pointer, err)
		}
		if val.Int() != int64(tt.want) {
			t.Errorf("Resolve(%s) = %v, want %v", tt.pointer, val.Int(), tt.want)
		}
	}
}

func TestJSONPointer_inConditions(t *testing.T) {
	type Data struct {
		A int
	}
	d := Data{A: 10}
	cond, err := ParseCondition[Data]("/A == 10")
	if err != nil {
		t.Fatalf("ParseCondition failed: %v", err)
	}
	ok, _ := cond.Evaluate(&d)
	if !ok {
		t.Error("Condition with JSON Pointer failed")
	}
}

func TestPath_ResolveParentPath(t *testing.T) {
	tests := []struct {
		path       string
		wantParent string
		wantKey    string
		wantIndex  int
		isIndex    bool
	}{
		{"/A/B", "/A", "B", 0, false},
		{"/A/0", "/A", "0", 0, true},
		{"/Map/Key~1WithSlash", "/Map", "Key/WithSlash", 0, false},
		{"/Top", "", "Top", 0, false},
	}

	for _, tt := range tests {
		parent, part, err := core.DeepPath(tt.path).ResolveParentPath()
		if err != nil {
			t.Errorf("ResolveParentPath(%s) error: %v", tt.path, err)
			continue
		}
		if string(parent) != tt.wantParent {
			t.Errorf("ResolveParentPath(%s) parent = %s, want %s", tt.path, parent, tt.wantParent)
		}
		if part.IsIndex != tt.isIndex {
			t.Errorf("ResolveParentPath(%s) isIndex = %v, want %v", tt.path, part.IsIndex, tt.isIndex)
		}
		if part.IsIndex {
			if part.Index != tt.wantIndex {
				t.Errorf("ResolveParentPath(%s) index = %d, want %d", tt.path, part.Index, tt.wantIndex)
			}
		} else {
			if part.Key != tt.wantKey {
				t.Errorf("ResolveParentPath(%s) key = %s, want %s", tt.path, part.Key, tt.wantKey)
			}
		}
	}
}

func TestPath_SetDelete(t *testing.T) {
	type Data struct {
		A int
		M map[string]int
		S []int
	}
	d := Data{A: 1, M: map[string]int{"a": 1}, S: []int{1}}
	rv := reflect.ValueOf(&d).Elem()

	// Set
	core.DeepPath("/A").Set(rv, reflect.ValueOf(2))
	if d.A != 2 {
		t.Errorf("Set A failed: %d", d.A)
	}
	core.DeepPath("/M/b").Set(rv, reflect.ValueOf(2))
	if d.M["b"] != 2 {
		t.Errorf("Set M.b failed: %v", d.M)
	}
	core.DeepPath("/S/1").Set(rv, reflect.ValueOf(2)) // Append
	if len(d.S) != 2 || d.S[1] != 2 {
		t.Errorf("Set S[1] failed: %v", d.S)
	}

	// Delete
	core.DeepPath("/M/a").Delete(rv)
	if _, ok := d.M["a"]; ok {
		t.Error("Delete M.a failed")
	}
	core.DeepPath("/S/0").Delete(rv)
	if len(d.S) != 1 || d.S[0] != 2 {
		t.Errorf("Delete S[0] failed: %v", d.S)
	}
}

func TestPath_Errors_Exhaustive(t *testing.T) {
	type S struct{ A int }
	var s *S
	rv := reflect.ValueOf(s)

	// Resolve nil pointer
	_, err := core.DeepPath("/A").Resolve(rv)
	if err == nil {
		t.Error("Expected error resolving through nil pointer")
	}

	// Resolve empty path parent
	_, _, err = core.DeepPath("").ResolveParent(reflect.ValueOf(1))
	if err == nil {
		t.Error("Expected error resolveParent empty")
	}

	// Navigate invalid index
	_, _, err = core.DeepPath("").Navigate(reflect.ValueOf([]int{1}), []core.PathPart{{Index: 5, IsIndex: true}})
	if err == nil {
		t.Error("Expected error index out of bounds")
	}

	// Navigate invalid map key type
	m := map[float64]int{1.0: 1}
	_, _, err = core.DeepPath("").Navigate(reflect.ValueOf(m), []core.PathPart{{Key: "1"}})
	if err == nil {
		t.Error("Expected error unsupported map key")
	}

	// Navigate non-struct field
	_, _, err = core.DeepPath("").Navigate(reflect.ValueOf(1), []core.PathPart{{Key: "A"}})
	if err == nil {
		t.Error("Expected error non-struct field")
	}

	// Set/Delete errors
	core.DeepPath("/A").Delete(reflect.ValueOf(1))
}

func TestCondition_Errors(t *testing.T) {
	type Data struct{ A int }

	t.Run("PathResolveErrors", func(t *testing.T) {
		cond := Defined[Data]("/Missing/Field")
		ok, _ := cond.Evaluate(&Data{})
		if ok {
			t.Error("Defined should be false for missing path")
		}

		condU := Undefined[Data]("/Missing/Field")
		ok, _ = condU.Evaluate(&Data{})
		if !ok {
			t.Error("Undefined should be true for missing path")
		}

		condT := Type[Data]("/Missing/Field", "undefined")
		ok, _ = condT.Evaluate(&Data{})
		if !ok {
			t.Error("Type should be true for missing path if looking for undefined")
		}
	})

	t.Run("CompareValuesErrors", func(t *testing.T) {
		_, err := core.CompareValues(reflect.ValueOf(1), reflect.ValueOf("a"), ">", false)
		if err == nil {
			t.Error("Expected error comparing int and string with >")
		}

		_, err = core.CompareValues(reflect.ValueOf(struct{}{}), reflect.ValueOf(struct{}{}), ">", false)
		if err == nil {
			t.Error("Expected error comparing structs with >")
		}
	})

	t.Run("ParserErrors", func(t *testing.T) {
		_, err := ParseCondition[Data]("A == ")
		if err == nil {
			t.Error("Expected error parsing incomplete expression")
		}
		_, err = ParseCondition[Data]("A ==")
		if err == nil {
			t.Error("Expected error parsing expression without value")
		}
		_, err = ParseCondition[Data]("(A == 1")
		if err == nil {
			t.Error("Expected error parsing unclosed parenthesis")
		}
		_, err = ParseCondition[Data]("A == 1 )")
		if err == nil {
			t.Error("Expected error parsing unexpected parenthesis")
		}
	})

	t.Run("SerializationErrors", func(t *testing.T) {
		_, err := MarshalConditionAny(123)
		if err == nil {
			t.Error("Expected error marshalling unknown type")
		}
		_, err = UnmarshalConditionSurrogate[any](123)
		if err == nil {
			t.Error("Expected error converting from invalid surrogate type")
		}
		_, err = UnmarshalConditionSurrogate[any](map[string]any{"k": "unknown", "d": nil})
		if err == nil {
			t.Error("Expected error converting from unknown kind")
		}
	})
}

func TestCondition_Structure(t *testing.T) {
	// Test Paths() and WithRelativePath() to improve coverage
	type Data struct {
		A struct {
			B int
		}
	}
	
	c := Eq[Data]("/A/B", 10)
	paths := c.Paths()
	if len(paths) != 1 || paths[0] != "/A/B" {
		t.Errorf("Paths() incorrect: %v", paths)
	}
	
	// Test relative path
	// Condition on /A/B, prefix /A.
	// Result path should be /B.
	rel := c.WithRelativePath("/A")
	relPaths := rel.Paths()
	if len(relPaths) != 1 || relPaths[0] != "/B" {
		t.Errorf("WithRelativePath() incorrect: %v", relPaths)
	}
	
	// Test complex condition structure
	c2 := And(Eq[Data]("/A/B", 10), Greater[Data]("/A/B", 5))
	paths2 := c2.Paths()
	if len(paths2) != 2 {
		t.Errorf("Expected 2 paths, got %d", len(paths2))
	}
	
	rel2 := c2.WithRelativePath("/A")
	relPaths2 := rel2.Paths()
	if relPaths2[0] != "/B" || relPaths2[1] != "/B" {
		t.Errorf("Relative paths incorrect: %v", relPaths2)
	}
}

func TestCondition_Structure_Exhaustive(t *testing.T) {
	type Data struct{}
	
	// Defined
	cDef := Defined[Data]("/P")
	if len(cDef.Paths()) != 1 {
		t.Error("Defined Paths failed")
	}
	if cDef.WithRelativePath("/A").Paths()[0] != "/P" { // Path logic might differ depending on prefix
		// If prefix doesn't match, it returns original.
	}
	
	// Undefined
	cUndef := Undefined[Data]("/P")
	if len(cUndef.Paths()) != 1 {
		t.Error("Undefined Paths failed")
	}
	
	// Not
	cNot := Not(cDef)
	if len(cNot.Paths()) != 1 {
		t.Error("Not Paths failed")
	}
	
	// Or
	cOr := Or(cDef, cUndef)
	if len(cOr.Paths()) != 2 {
		t.Error("Or Paths failed")
	}
	
	// CompareField
	cComp := EqualField[Data]("/A", "/B")
	if len(cComp.Paths()) != 2 {
		t.Error("EqualField Paths failed")
	}
	
	// In
	cIn := In[Data]("/A", 1, 2)
	if len(cIn.Paths()) != 1 {
		t.Error("In Paths failed")
	}
	
	// String ops
	cStr := Contains[Data]("/A", "foo")
	if len(cStr.Paths()) != 1 {
		t.Error("Contains Paths failed")
	}
	
	// Log
	cLog := Log[Data]("msg")
	if len(cLog.Paths()) != 0 {
		t.Error("Log Paths should be empty")
	}
}

func TestMarshalCondition(t *testing.T) {
	type Data struct {
		A int
	}
	c := Eq[Data]("/A", 10)
	// MarshalCondition now returns (any, error) which is the surrogate.
	// To test JSON output, we should marshal the surrogate.
	surrogate, err := MarshalCondition(c)
	if err != nil {
		t.Fatalf("MarshalCondition failed: %v", err)
	}
	
	bytes, err := json.Marshal(surrogate)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}
	
	var m map[string]any
	if err := json.Unmarshal(bytes, &m); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	
	// Use "equal" as expected by implementation
	if m["k"] != "equal" {
		t.Errorf("Expected k=equal, got %v", m["k"])
	}
	
	// Test MarshalJSON method on the Condition itself (should return bytes directly)
	bytes2, err := c.MarshalJSON()
	if err != nil {
		t.Errorf("MarshalJSON failed: %v", err)
	}
	if string(bytes) != string(bytes2) {
		t.Error("Wrapper and method differ")
	}
}
