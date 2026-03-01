package engine

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	//"github.com/brunoga/deep/v5/internal/core"
	//"github.com/brunoga/deep/v5/cond"
)

func TestPatch_String_Basic(t *testing.T) {
	a, b := "foo", "bar"
	patch := MustDiff(a, b)
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
	patch := MustDiff(a, b)
	if patch == nil {
		t.Fatal("Expected non-nil patch")
	}

	summary := patch.String()
	if !strings.Contains(summary, "+ [1]: {Kid2}") {
		t.Errorf("String() missing added kid: %s", summary)
	}
}

func TestPatch_ApplyResolved(t *testing.T) {
	type Config struct {
		Value int
	}
	c1 := Config{Value: 10}
	c2 := Config{Value: 20}

	patch := MustDiff(c1, c2)

	target := Config{Value: 10}

	// Resolver that rejects everything
	err := patch.ApplyResolved(&target, ConflictResolverFunc(func(path string, op OpKind, key, prevKey any, current, proposed reflect.Value) (reflect.Value, bool) {
		return reflect.Value{}, false
	}))
	if err != nil {
		t.Fatalf("ApplyResolved failed: %v", err)
	}

	if target.Value != 10 {
		t.Errorf("Value should not have changed, got %d", target.Value)
	}

	// Resolver that accepts everything
	err = patch.ApplyResolved(&target, ConflictResolverFunc(func(path string, op OpKind, key, prevKey any, current, proposed reflect.Value) (reflect.Value, bool) {
		return proposed, true
	}))
	if err != nil {
		t.Fatalf("ApplyResolved failed: %v", err)
	}

	if target.Value != 20 {
		t.Errorf("Value should have changed to 20, got %d", target.Value)
	}
}

type ConflictResolverFunc func(path string, op OpKind, key, prevKey any, current, proposed reflect.Value) (reflect.Value, bool)

func (f ConflictResolverFunc) Resolve(path string, op OpKind, key, prevKey any, current, proposed reflect.Value) (reflect.Value, bool) {
	return f(path, op, key, prevKey, current, proposed)
}

func TestPatch_ConditionPropagation(t *testing.T) {
	/*
		type InnerC struct{ V int }
		type DataC struct {
			A   int
			P   *InnerC
			I   any
			M   map[string]InnerC
			S   []InnerC
			Arr [1]InnerC
		}
		builder := NewPatchBuilder[DataC]()

		c := cond.Eq[DataC]("A", 1)

		builder.If(c).Unless(c).Test(DataC{A: 1})

		builder.Field("P").If(c).Unless(c)
		builder.Field("I").If(c).Unless(c)
		builder.Field("M").If(c).Unless(c)
		builder.Field("S").If(c).Unless(c)
		builder.Field("Arr").If(c).Unless(c)

		patch, _ := builder.Build()
		if patch == nil {
			t.Fatal("Build failed")
		}
	*/
}


func TestPatch_MoreApplyChecked(t *testing.T) {
	// ptrPatch
	t.Run("ptrPatch", func(t *testing.T) {
		val1 := 1
		p1 := &val1
		val2 := 2
		p2 := &val2
		patch := MustDiff(p1, p2)
		if err := patch.ApplyChecked(&p1); err != nil {
			t.Errorf("ptrPatch ApplyChecked failed: %v", err)
		}
	})
	// interfacePatch
	t.Run("interfacePatch", func(t *testing.T) {
		var i1 any = 1
		var i2 any = 2
		patch := MustDiff(i1, i2)
		if err := patch.ApplyChecked(&i1); err != nil {
			t.Errorf("interfacePatch ApplyChecked failed: %v", err)
		}
	})
}

func TestPatch_ToJSONPatch_Exhaustive(t *testing.T) {
	/*
		type Inner struct{ V int }
		type Data struct {
			P *Inner
			I any
			A []Inner
			M map[string]Inner
		}

		builder := NewPatchBuilder[Data]()

		builder.Field("P").Elem().Field("V").Set(1, 2)
		builder.Field("I").Elem().Set(1, 2)
		builder.Field("A").Index(0).Field("V").Set(1, 2)
		builder.Field("M").MapKey("k").Field("V").Set(1, 2)

		patch, _ := builder.Build()
		patch.ToJSONPatch()
	*/
}

func TestPatch_LogExhaustive(t *testing.T) {
	lp := &logPatch{message: "test"}

	lp.apply(reflect.Value{}, reflect.ValueOf(1), "/path")

	if err := lp.applyChecked(reflect.ValueOf(1), reflect.ValueOf(1), false, "/path"); err != nil {
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

func TestPatch_Walk_Basic(t *testing.T) {
	a := 10
	b := 20
	patch := MustDiff(a, b)

	var ops []string
	err := patch.Walk(func(path string, op OpKind, old, new any) error {
		ops = append(ops, fmt.Sprintf("%s:%s:%v:%v", path, op, old, new))
		return nil
	})

	if err != nil {
		t.Fatalf("Walk failed: %v", err)
	}

	expected := []string{"/:replace:10:20"}
	if fmt.Sprintf("%v", ops) != fmt.Sprintf("%v", expected) {
		t.Errorf("Expected ops %v, got %v", expected, ops)
	}
}

func TestPatch_Walk_Struct(t *testing.T) {
	type S struct {
		A int
		B string
	}
	a := S{A: 1, B: "one"}
	b := S{A: 2, B: "two"}
	patch := MustDiff(a, b)

	ops := make(map[string]string)
	err := patch.Walk(func(path string, op OpKind, old, new any) error {
		ops[path] = fmt.Sprintf("%s:%v:%v", op, old, new)
		return nil
	})

	if err != nil {
		t.Fatalf("Walk failed: %v", err)
	}

	if len(ops) != 2 {
		t.Errorf("Expected 2 ops, got %d", len(ops))
	}

	if ops["/A"] != "replace:1:2" {
		t.Errorf("Unexpected op for A: %s", ops["/A"])
	}
	if ops["/B"] != "replace:one:two" {
		t.Errorf("Unexpected op for B: %s", ops["/B"])
	}
}

func TestPatch_Walk_Slice(t *testing.T) {
	a := []int{1, 2, 3}
	b := []int{1, 4, 3, 5}
	patch := MustDiff(a, b)

	var ops []string
	err := patch.Walk(func(path string, op OpKind, old, new any) error {
		ops = append(ops, fmt.Sprintf("%s:%s:%v:%v", path, op, old, new))
		return nil
	})

	if err != nil {
		t.Fatalf("Walk failed: %v", err)
	}

	found4 := false
	found5 := false
	for _, op := range ops {
		if strings.Contains(op, ":2:4") || (strings.Contains(op, ":remove:2:<nil>") || strings.Contains(op, ":add:<nil>:4")) {
			if strings.Contains(op, "4") {
				found4 = true
			}
		}
		if strings.Contains(op, ":add:<nil>:5") {
			found5 = true
		}
	}

	if !found4 || !found5 {
		t.Errorf("Missing expected ops in %v", ops)
	}
}

func TestPatch_Walk_Map(t *testing.T) {
	a := map[string]int{"one": 1, "two": 2}
	b := map[string]int{"one": 1, "two": 20, "three": 3}
	patch := MustDiff(a, b)

	ops := make(map[string]string)
	err := patch.Walk(func(path string, op OpKind, old, new any) error {
		ops[path] = fmt.Sprintf("%s:%v:%v", op, old, new)
		return nil
	})

	if err != nil {
		t.Fatalf("Walk failed: %v", err)
	}

	if ops["/two"] != "replace:2:20" {
		t.Errorf("Unexpected op for two: %s", ops["/two"])
	}
	if ops["/three"] != "add:<nil>:3" {
		t.Errorf("Unexpected op for three: %s", ops["/three"])
	}
}

func TestPatch_Walk_KeyedSlice(t *testing.T) {
	type KeyedTask struct {
		ID     string `deep:"key"`
		Status string
	}
	a := []KeyedTask{
		{ID: "t1", Status: "todo"},
		{ID: "t2", Status: "todo"},
	}
	b := []KeyedTask{
		{ID: "t2", Status: "done"},
		{ID: "t1", Status: "todo"},
	}

	patch := MustDiff(a, b)

	ops := make(map[string]string)
	err := patch.Walk(func(path string, op OpKind, old, new any) error {
		ops[path] = fmt.Sprintf("%s:%v:%v", op, old, new)
		return nil
	})

	if err != nil {
		t.Fatalf("Walk failed: %v", err)
	}

	if len(ops) == 0 {
		t.Errorf("Expected some ops, got none")
	}
}

func TestPatch_Walk_ErrorStop(t *testing.T) {
	a := map[string]int{"one": 1, "two": 2}
	b := map[string]int{"one": 10, "two": 20}
	patch := MustDiff(a, b)

	count := 0
	err := patch.Walk(func(path string, op OpKind, old, new any) error {
		count++
		return fmt.Errorf("stop")
	})

	if err == nil || err.Error() != "stop" {
		t.Errorf("Expected 'stop' error, got %v", err)
	}
	if count != 1 {
		t.Errorf("Expected walk to stop after 1 call, got %d", count)
	}
}

type customTestStruct struct {
	V int
}

func TestCustomDiffPatch_ToJSONPatch(t *testing.T) {
	patch := &typedPatch[customTestStruct]{
		inner: &structPatch{
			fields: map[string]diffPatch{
				"V": &valuePatch{
					oldVal: reflect.ValueOf(1),
					newVal: reflect.ValueOf(2),
				},
			},
		},
	}

	// Manually wrap it in customDiffPatch
	custom := &customDiffPatch{
		patch: patch,
	}

	jsonBytes := custom.toJSONPatch("") // Use empty prefix for root

	var ops []map[string]any
	data, _ := json.Marshal(jsonBytes)
	json.Unmarshal(data, &ops)

	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}

	if ops[0]["path"] != "/V" {
		t.Errorf("expected path /V, got %s", ops[0]["path"])
	}
}

func TestPatch_Summary(t *testing.T) {
	type Config struct {
		Name    string
		Value   int
		Options []string
	}

	c1 := Config{Name: "v1", Value: 10, Options: []string{"a", "b"}}
	c2 := Config{Name: "v2", Value: 20, Options: []string{"a", "c"}}

	patch := MustDiff(c1, c2)
	if patch == nil {
		t.Fatal("Expected patch")
	}

	summary := patch.Summary()
	if summary == "" || summary == "No changes." {
		t.Errorf("Unexpected summary: %q", summary)
	}
}
