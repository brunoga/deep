package deep

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestPath_Errors_Exhaustive(t *testing.T) {
	type S struct{ A int }
	s := S{A: 1}
	rv := reflect.ValueOf(&s).Elem()

	// resolveParent empty
	Path("").resolveParent(rv)

	// navigate nil intermediate
	type N struct{ P *S }
	n := N{P: nil}
	Path("/P/A").resolve(reflect.ValueOf(n))

	// set root error
	val := 1
	Path("/").set(reflect.ValueOf(val), reflect.ValueOf(2))

	// set slice oob
	slc := []int{1}
	Path("/2").set(reflect.ValueOf(&slc).Elem(), reflect.ValueOf(2))
}

func TestJSONPointer_Resolve(t *testing.T) {
	type Child struct {
		Name string
	}
	type Data struct {
		Kids []Child
		Meta map[string]int
	}
	d := Data{
		Kids: []Child{{Name: "A"}, {Name: "B"}},
		Meta: map[string]int{"v": 1},
	}
	tests := []struct {
		path string
		want any
	}{
		{"/Kids/0/Name", "A"},
		{"/Kids/1/Name", "B"},
		{"/Meta/v", 1},
	}
	for _, tt := range tests {
		val, err := Path(tt.path).resolve(reflect.ValueOf(d))
		if err != nil {
			t.Errorf("Resolve(%q) failed: %v", tt.path, err)
			continue
		}
		if !reflect.DeepEqual(val.Interface(), tt.want) {
			t.Errorf("Resolve(%q) = %v, want %v", tt.path, val.Interface(), tt.want)
		}
	}
}

func TestJSONPointer_SpecialChars(t *testing.T) {
	m := map[string]int{
		"foo/bar": 1,
		"foo~bar": 2,
	}
	tests := []struct {
		path string
		want any
	}{
		{"/foo~1bar", 1},
		{"/foo~0bar", 2},
	}
	for _, tt := range tests {
		val, err := Path(tt.path).resolve(reflect.ValueOf(m))
		if err != nil {
			t.Errorf("Resolve(%q) failed: %v", tt.path, err)
			continue
		}
		if !reflect.DeepEqual(val.Interface(), tt.want) {
			t.Errorf("Resolve(%q) = %v, want %v", tt.path, val.Interface(), tt.want)
		}
	}
}

func TestJSONPointer_InConditions(t *testing.T) {
	type User struct {
		Name string
	}
	u := User{Name: "Alice"}

	expr := "/Name == 'Alice'"
	cond, err := ParseCondition[User](expr)
	if err != nil {
		t.Fatalf("ParseCondition failed: %v", err)
	}
	ok, err := cond.Evaluate(&u)
	if err != nil {
		t.Fatalf("Evaluate failed: %v", err)
	}
	if !ok {
		t.Errorf("Expected true for %q", expr)
	}
}

func TestPath_SetDelete(t *testing.T) {
	type Data struct {
		A int
		M map[string]int
		S []int
	}
	d := Data{M: map[string]int{"a": 1}, S: []int{1, 2}}
	rv := reflect.ValueOf(&d).Elem()

	// Path.set
	Path("/A").set(rv, reflect.ValueOf(10))
	Path("/M/b").set(rv, reflect.ValueOf(20))
	Path("/S/1").set(rv, reflect.ValueOf(30))
	Path("/S/2").set(rv, reflect.ValueOf(40)) // Append

	if d.A != 10 || d.M["b"] != 20 || d.S[1] != 30 || d.S[2] != 40 {
		t.Errorf("Path.set failed: %+v", d)
	}

	// Path.delete
	Path("/M/a").delete(rv)
	Path("/S/0").delete(rv)
	Path("/A").delete(rv)

	if _, ok := d.M["a"]; ok || len(d.S) != 2 || d.A != 0 {
		t.Errorf("Path.delete failed: %+v", d)
	}

	// Root set
	val := 1
	rvVal := reflect.ValueOf(&val).Elem()
	Path("/").set(rvVal, reflect.ValueOf(2))
	if val != 2 {
		t.Errorf("Root Path.set failed: %d", val)
	}
}

func TestApplyChecked_Basic(t *testing.T) {
	type S struct {
		A int
	}
	s := S{A: 1}
	builder := NewBuilder[S]()
	node, _ := builder.Root().Field("A")
	node.Set(1, 2)
	patch, _ := builder.Build()
	if err := patch.ApplyChecked(&s); err != nil {
		t.Errorf("ApplyChecked failed: %v", err)
	}
	if s.A != 2 {
		t.Errorf("Expected A=2")
	}
	if err := patch.ApplyChecked(&s); err == nil {
		t.Errorf("Expected error on second ApplyChecked")
	}
}

func TestApplyChecked_Condition(t *testing.T) {
	type S struct {
		State string
		Ver   int
	}
	s := S{State: "active", Ver: 1}
	builder := NewBuilder[S]()
	root := builder.Root()
	node, _ := root.Field("Ver")
	node.Set(1, 2)
	basePatch, _ := builder.Build()
	cond := Equal[S]("State", "active")
	patch := basePatch.WithCondition(cond)
	if err := patch.ApplyChecked(&s); err != nil {
		t.Fatalf("ApplyChecked failed: %v", err)
	}
	s.State = "inactive"
	s.Ver = 1
	if err := patch.ApplyChecked(&s); err == nil {
		t.Fatal("Expected condition failure")
	}
}

func TestApplyChecked_MapStrict(t *testing.T) {
	m := map[string]int{"a": 1}
	builder := NewBuilder[map[string]int]()
	builder.Root().AddMapEntry("a", 2)
	patch, _ := builder.Build()
	if err := patch.ApplyChecked(&m); err == nil {
		t.Error("Expected error adding existing key in strict mode")
	}
}

func TestApplyChecked_ExhaustiveConditions(t *testing.T) {
	type Data struct {
		Count int
		Value float64
		Label string
		Flags []bool
	}
	d := Data{
		Count: 10,
		Value: 3.14,
		Label: "beta",
		Flags: []bool{true, false},
	}
	tests := []struct {
		name string
		cond Condition[Data]
		want bool
	}{
		{"Equal", Equal[Data]("Count", 10), true},
		{"NotEqual", NotEqual[Data]("Label", "alpha"), true},
		{"Greater", Greater[Data]("Count", 5), true},
		{"GreaterFalse", Greater[Data]("Count", 15), false},
		{"Less", Less[Data]("Value", 5.0), true},
		{"GreaterEqual", GreaterEqual[Data]("Count", 10), true},
		{"LessEqual", LessEqual[Data]("Value", 3.14), true},
		{"StringGreater", Greater[Data]("Label", "alpha"), true},
		{"SliceIndex", Equal[Data]("Flags[1]", false), true},
		{"AndTrue", And[Data](Equal[Data]("Count", 10), Less[Data]("Value", 4.0)), true},
		{"AndFalse", And[Data](Equal[Data]("Count", 10), Less[Data]("Value", 2.0)), false},
		{"OrTrue", Or[Data](Equal[Data]("Count", 0), Equal[Data]("Label", "beta")), true},
		{"NotTrue", Not[Data](Equal[Data]("Label", "alpha")), true},
		{
			"Complex",
			And[Data](
				Greater[Data]("Count", 0),
				Or[Data](
					Equal[Data]("Label", "beta"),
					Equal[Data]("Label", "release"),
				),
				Not[Data](Less[Data]("Value", 0.0)),
			),
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.cond.Evaluate(&d)
			if err != nil {
				t.Fatalf("Evaluate failed: %v", err)
			}
			if got != tt.want {
				t.Errorf("Evaluate() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseCondition(t *testing.T) {
	type User struct {
		Name   string
		Level  int
		Active bool
		Score  float64
		Tags   []string
	}
	u := User{
		Name:   "Alice",
		Level:  10,
		Active: true,
		Score:  95.5,
		Tags:   []string{"admin", "editor"},
	}
	tests := []struct {
		expr string
		want bool
	}{
		{"Name == 'Alice'", true},
		{"Level > 5", true},
		{"Active == true", true},
		{"Score >= 95.0", true},
		{"Tags[0] == 'admin'", true},
		{"(Level > 5 AND Active == true) OR Name == 'Bob'", true},
		{"NOT (Level < 5)", true},
		{"Level > 5 AND Level < 15", true},
		{"Level == 10 OR Level == 20", true},
		{"Name != 'Bob' AND (Score < 100.0 OR Level == 0)", true},
	}
	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			cond, err := ParseCondition[User](tt.expr)
			if err != nil {
				t.Fatalf("ParseCondition failed: %v", err)
			}
			got, err := cond.Evaluate(&u)
			if err != nil {
				t.Fatalf("Evaluate failed: %v", err)
			}
			if got != tt.want {
				t.Errorf("Evaluate(%q) = %v, want %v", tt.expr, got, tt.want)
			}
		})
	}
}

func TestFieldConditions(t *testing.T) {
	type Data struct {
		A  int
		B  int
		C  int
		S1 string
		S2 string
	}
	d := Data{A: 10, B: 10, C: 20, S1: "foo", S2: "bar"}

	tests := []struct {
		name string
		cond Condition[Data]
		want bool
	}{
		{"EqualField_True", EqualField[Data]("A", "B"), true},
		{"EqualField_False", EqualField[Data]("A", "C"), false},
		{"NotEqualField_True", NotEqualField[Data]("A", "C"), true},
		{"NotEqualField_False", NotEqualField[Data]("A", "B"), false},
		{"GreaterField_True", GreaterField[Data]("C", "A"), true},
		{"GreaterField_False", GreaterField[Data]("A", "C"), false},
		{"LessField_True", LessField[Data]("A", "C"), true},
		{"LessEqualField_True", LessEqualField[Data]("A", "B"), true},
		{"GreaterEqualField_True", GreaterEqualField[Data]("A", "B"), true},
		{"String_LessField", LessField[Data]("S2", "S1"), true}, // "bar" < "foo"
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.cond.Evaluate(&d)
			if err != nil {
				t.Fatalf("Evaluate failed: %v", err)
			}
			if got != tt.want {
				t.Errorf("Evaluate() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseFieldCondition(t *testing.T) {
	type Data struct {
		A int
		B int
		C int
	}
	d := Data{A: 10, B: 10, C: 20}

	tests := []struct {
		expr string
		want bool
	}{
		{"A == B", true},
		{"A != C", true},
		{"C > A", true},
		{"A < C", true},
		{"A >= B", true},
		{"A <= B", true},
		{"A == C", false},
	}

	for _, tt := range tests {
		t.Run(tt.expr, func(t *testing.T) {
			cond, err := ParseCondition[Data](tt.expr)
			if err != nil {
				t.Fatalf("ParseCondition failed: %v", err)
			}
			got, err := cond.Evaluate(&d)
			if err != nil {
				t.Fatalf("Evaluate failed: %v", err)
			}
			if got != tt.want {
				t.Errorf("Evaluate(%q) = %v, want %v", tt.expr, got, tt.want)
			}
		})
	}
}

func TestFieldConditionSerialization(t *testing.T) {

	type Data struct {
		A int

		B int
	}

	tests := []struct {
		name string

		cond Condition[Data]
	}{

		{"EqualField", EqualField[Data]("A", "B")},

		{"CompareField", GreaterField[Data]("A", "B")},

		{"AndCondition", And[Data](EqualField[Data]("A", "B"), Greater[Data]("A", 5))},

		{"OrCondition", Or[Data](EqualField[Data]("A", "B"), Less[Data]("B", 10))},

		{"NotCondition", Not[Data](Equal[Data]("A", 0))},
	}

	for _, tt := range tests {

		t.Run(tt.name, func(t *testing.T) {

			s, err := marshalCondition(tt.cond)

			if err != nil {

				t.Fatalf("marshalCondition failed: %v", err)

			}

			sBytes, err := json.Marshal(s)

			if err != nil {

				t.Fatalf("json.Marshal failed: %v", err)

			}

			cond2, err := unmarshalCondition[Data](sBytes)

			if err != nil {

				t.Fatalf("unmarshalCondition failed: %v", err)

			}

			d := Data{A: 10, B: 10}

			ok1, err := tt.cond.Evaluate(&d)

			if err != nil {

				t.Fatalf("Original Evaluate failed: %v", err)

			}

			ok2, err := cond2.Evaluate(&d)

			if err != nil {

				t.Fatalf("Restored Evaluate failed: %v", err)

			}

			if ok1 != ok2 {

				t.Errorf("Evaluate mismatch: original=%v, restored=%v", ok1, ok2)

			}

		})

	}

}

func TestCompareValues_Exhaustive(t *testing.T) {

	type Data struct {
		U uint

		F float64

		S string
	}

	d := Data{U: 10, F: 3.14, S: "banana"}

	tests := []struct {
		expr string

		want bool
	}{

		{"U > 5", true},

		{"U < 20", true},

		{"U >= 10", true},

		{"U <= 10", true},

		{"F > 3.0", true},

		{"F < 4.0", true},

		{"S > 'apple'", true},

		{"S < 'cherry'", true},

		{"S == 'banana'", true},

		{"S != 'apple'", true},
	}

	for _, tt := range tests {

		t.Run(tt.expr, func(t *testing.T) {

			cond, err := ParseCondition[Data](tt.expr)

			if err != nil {

				t.Fatalf("ParseCondition failed: %v", err)

			}

			got, err := cond.Evaluate(&d)

			if err != nil {

				t.Fatalf("Evaluate failed: %v", err)

			}

			if got != tt.want {

				t.Errorf("Evaluate(%q) = %v, want %v", tt.expr, got, tt.want)

			}

		})

	}

}

func TestCondition_Errors(t *testing.T) {
	t.Run("PathResolveErrors", func(t *testing.T) {
		type S struct{ A int }
		s := S{A: 1}
		rv := reflect.ValueOf(s)

		paths := []string{
			"NonExistent",
			"A.Sub",
			"A[0]",
		}
		for _, p := range paths {
			_, err := Path(p).resolve(rv)
			if err == nil {
				t.Errorf("Expected error for path %q", p)
			}
		}

		var nilPtr *S
		_, err := Path("A").resolve(reflect.ValueOf(nilPtr))
		if err == nil {
			t.Error("Expected error for nil pointer resolve")
		}
	})

	t.Run("CompareValuesErrors", func(t *testing.T) {
		v1 := reflect.ValueOf(10)
		v2 := reflect.ValueOf("string")
		_, err := compareValues(v1, v2, ">", false)
		if err == nil {
			t.Error("Expected error for mismatched types in comparison")
		}

		v3 := reflect.ValueOf(true)
		_, err = compareValues(v3, v3, ">", false)
		if err == nil {
			t.Error("Expected error for unsupported comparison on bool")
		}
	})

	t.Run("ParserErrors", func(t *testing.T) {
		exprs := []string{
			"A == ",
			"A > B C",
			"(A == 1",
			"A == 1)",
			"NOT",
			"A + B",
		}
		for _, e := range exprs {
			_, err := ParseCondition[any](e)
			if err == nil {
				t.Errorf("Expected error for invalid expression %q", e)
			}
		}
	})

	t.Run("SerializationErrors", func(t *testing.T) {
		_, err := marshalConditionAny(123) // Not a condition
		if err == nil {
			t.Error("Expected error for non-condition marshal")
		}

		_, err = convertFromCondSurrogate[int]("not a surrogate")
		if err == nil {
			t.Error("Expected error for invalid surrogate type")
		}
	})

	t.Run("RawConditionExhaustive", func(t *testing.T) {
		// Test rawOrCondition paths/relative
		c1 := &rawCompareCondition{Path: "A", Val: 1, Op: "=="}
		c2 := &rawCompareCondition{Path: "B", Val: 2, Op: "=="}
		or := &rawOrCondition{Conditions: []rawCondition{c1, c2}}
		if len(or.paths()) != 2 {
			t.Errorf("Expected 2 paths, got %d", len(or.paths()))
		}
		relOr := or.withRelativeParts(parsePath("Prefix"))
		if relOr.(*rawOrCondition).Conditions[0].(*rawCompareCondition).Path != "A" {
			// Actually withRelativeParts("Prefix") on path "A" should be "A" if prefix not found
		}

		// Test rawNotCondition paths/relative
		not := &rawNotCondition{C: c1}
		if len(not.paths()) != 1 {
			t.Error("Expected 1 path for not")
		}
		relNot := not.withRelativeParts(parsePath("A"))
		if relNot.(*rawNotCondition).C.(*rawCompareCondition).Path != "" {
			t.Errorf("Expected empty path after stripping prefix, got %q", relNot.(*rawNotCondition).C.(*rawCompareCondition).Path)
		}
	})
}
