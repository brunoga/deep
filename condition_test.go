package deep

import (
	"reflect"
	"testing"
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
		val, err := Path(tt.pointer).resolve(rv)
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
		val, err := Path(tt.pointer).resolve(rv)
		if err != nil {
			t.Fatalf("Resolve(%s) failed: %v", tt.pointer, err)
		}
		if val.Int() != int64(tt.want) {
			t.Errorf("Resolve(%s) = %v, want %v", tt.pointer, val.Int(), tt.want)
		}
	}
}

func TestJSONPointer_InConditions(t *testing.T) {
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

func TestPath_SetDelete(t *testing.T) {
	type Data struct {
		A int
		M map[string]int
		S []int
	}
	d := Data{A: 1, M: map[string]int{"a": 1}, S: []int{1}}
	rv := reflect.ValueOf(&d).Elem()

	// Set
	Path("A").set(rv, reflect.ValueOf(2))
	if d.A != 2 {
		t.Errorf("Set A failed: %d", d.A)
	}
	Path("M.b").set(rv, reflect.ValueOf(2))
	if d.M["b"] != 2 {
		t.Errorf("Set M.b failed: %v", d.M)
	}
	Path("S[1]").set(rv, reflect.ValueOf(2)) // Append
	if len(d.S) != 2 || d.S[1] != 2 {
		t.Errorf("Set S[1] failed: %v", d.S)
	}

	// Delete
	Path("M.a").delete(rv)
	if _, ok := d.M["a"]; ok {
		t.Error("Delete M.a failed")
	}
	Path("S[0]").delete(rv)
	if len(d.S) != 1 || d.S[0] != 2 {
		t.Errorf("Delete S[0] failed: %v", d.S)
	}
}

func TestPath_Errors_Exhaustive(t *testing.T) {
	type S struct{ A int }
	var s *S
	rv := reflect.ValueOf(s)

	// Resolve nil pointer
	_, err := Path("A").resolve(rv)
	if err == nil {
		t.Error("Expected error resolving through nil pointer")
	}

	// Resolve empty path parent
	_, _, err = Path("").resolveParent(reflect.ValueOf(1))
	if err == nil {
		t.Error("Expected error resolveParent empty")
	}

	// Navigate invalid index
	_, _, err = Path("").navigate(reflect.ValueOf([]int{1}), []pathPart{{index: 5, isIndex: true}})
	if err == nil {
		t.Error("Expected error index out of bounds")
	}

	// Navigate invalid map key type
	m := map[float64]int{1.0: 1}
	_, _, err = Path("").navigate(reflect.ValueOf(m), []pathPart{{key: "1"}})
	if err == nil {
		t.Error("Expected error unsupported map key")
	}

	// Navigate non-struct field
	_, _, err = Path("").navigate(reflect.ValueOf(1), []pathPart{{key: "A"}})
	if err == nil {
		t.Error("Expected error non-struct field")
	}

	// Set/Delete errors
	Path("A").delete(reflect.ValueOf(1))
}

func TestCondition_Errors(t *testing.T) {
	type Data struct{ A int }

	t.Run("PathResolveErrors", func(t *testing.T) {
		cond := DefinedCondition[Data]{Path: "Missing.Field"}
		ok, _ := cond.Evaluate(&Data{})
		if ok {
			t.Error("DefinedCondition should be false for missing path")
		}

		condU := UndefinedCondition[Data]{Path: "Missing.Field"}
		ok, _ = condU.Evaluate(&Data{})
		if !ok {
			t.Error("UndefinedCondition should be true for missing path")
		}

		condT := TypeCondition[Data]{Path: "Missing.Field", TypeName: "undefined"}
		ok, _ = condT.Evaluate(&Data{})
		if !ok {
			t.Error("TypeCondition should be true for missing path if looking for undefined")
		}
	})

	t.Run("CompareValuesErrors", func(t *testing.T) {
		_, err := compareValues(reflect.ValueOf(1), reflect.ValueOf("a"), ">", false)
		if err == nil {
			t.Error("Expected error comparing int and string with >")
		}

		_, err = compareValues(reflect.ValueOf(struct{}{}), reflect.ValueOf(struct{}{}), ">", false)
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
		_, err := marshalConditionAny(123)
		if err == nil {
			t.Error("Expected error marshalling unknown type")
		}
		_, err = convertFromCondSurrogate[any](123)
		if err == nil {
			t.Error("Expected error converting from invalid surrogate type")
		}
		_, err = convertFromCondSurrogate[any](map[string]any{"k": "unknown", "d": nil})
		if err == nil {
			t.Error("Expected error converting from unknown kind")
		}
	})

	t.Run("RawConditionExhaustive", func(t *testing.T) {
		rc := &rawTypeCondition{Path: "A", TypeName: "unknown"}
		_, err := rc.evaluateAny(Data{A: 1})
		if err == nil {
			t.Error("Expected error for unknown type in rawTypeCondition")
		}

		rs := &rawStringCondition{Path: "A", Val: "v", Op: "unknown"}
		_, err = rs.evaluateAny(struct{ A string }{"v"})
		if err == nil {
			t.Error("Expected error for unknown op in rawStringCondition")
		}
	})
}
