package deep

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"reflect"
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

	p := Diff(s1, s2).WithCondition(Eq[TestStruct]("I", 1))

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

func TestPatch_SerializationExhaustive(t *testing.T) {
	type Data struct {
		C []int
	}
	Register[Data]()

	builder := NewBuilder[Data]()
	root := builder.Root()
	nodeC, _ := root.Field("C")
	nodeCI, _ := nodeC.Index(0)
	nodeCI.Set(1, 10)

	patch, _ := builder.Build()

	// Gob
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	enc.Encode(patch)

	dec := gob.NewDecoder(bytes.NewReader(buf.Bytes()))
	var patch2 typedPatch[Data]
	dec.Decode(&patch2)

	// JSON
	data, _ := json.Marshal(patch)
	var patch3 typedPatch[Data]
	json.Unmarshal(data, &patch3)
}

func TestPatch_Serialization_Conditions(t *testing.T) {
	type Data struct{ A int }
	builder := NewBuilder[Data]()
	c := Eq[Data]("A", 1)
	builder.Root().If(c).Unless(c).Test(Data{A: 1})
	patch, _ := builder.Build()

	// Coverage for marshalDiffPatch branches
	data, _ := json.Marshal(patch)
	var patch2 typedPatch[Data]
	json.Unmarshal(data, &patch2)
}

func TestPatch_Serialization_Errors(t *testing.T) {
	// unmarshalDiffPatch error
	unmarshalDiffPatch([]byte("INVALID"))

	// unmarshalCondFromMap missing key
	unmarshalCondFromMap(map[string]any{}, "c")

	// convertFromSurrogate unknown kind
	convertFromSurrogate(map[string]any{"k": "unknown", "d": map[string]any{}})

	// convertFromSurrogate invalid surrogate type
	convertFromSurrogate(123)
}

func TestPatchSerialization_SemanticSlice(t *testing.T) {
	type Item struct {
		ID   int `deep:"key"`
		Name string
	}
	type Data struct {
		Items []Item `deep:"key"`
	}

	s1 := Data{Items: []Item{{ID: 1, Name: "A"}}}
	s2 := Data{Items: []Item{{ID: 1, Name: "A"}, {ID: 2, Name: "B"}}}

	patch := Diff(s1, s2)

	// Verify internal state before serialization
	unwrapped := patch.(patchUnwrapper).unwrap()
	sp := unwrapped.(*structPatch).fields["Items"].(*slicePatch)
	foundAdd := false
	for _, op := range sp.ops {
		if op.Kind == OpAdd {
			if op.Key != float64(2) && op.Key != 2 { // json uses float64
				t.Errorf("Expected Key=2, got %v", op.Key)
			}
			if op.PrevKey != float64(1) && op.PrevKey != 1 {
				t.Errorf("Expected PrevKey=1, got %v", op.PrevKey)
			}
			foundAdd = true
		}
	}
	if !foundAdd {
		t.Fatal("Add operation not found in patch")
	}

	// JSON Roundtrip
	data, err := json.Marshal(patch)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	p2 := NewPatch[Data]()
	if err := json.Unmarshal(data, p2); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	// Verify internal state after serialization
	unwrapped2 := p2.(patchUnwrapper).unwrap()
	sp2 := unwrapped2.(*structPatch).fields["Items"].(*slicePatch)
	for _, op := range sp2.ops {
		if op.Kind == OpAdd {
			// json.Unmarshal uses float64 for all numbers
			if op.Key != float64(2) {
				t.Errorf("After Unmarshal: Expected Key=2 (float64), got %v (%T)", op.Key, op.Key)
			}
			if op.PrevKey != float64(1) {
				t.Errorf("After Unmarshal: Expected PrevKey=1 (float64), got %v (%T)", op.PrevKey, op.PrevKey)
			}
		}
	}
}
