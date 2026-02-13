package deep

import (
	"reflect"
	"testing"
)

func TestPatch_SummaryAndRelease(t *testing.T) {
	type Config struct {
		Name    string
		Value   int
		Options []string
	}

	c1 := Config{Name: "v1", Value: 10, Options: []string{"a", "b"}}
	c2 := Config{Name: "v2", Value: 20, Options: []string{"a", "c"}}

	patch := Diff(c1, c2)
	if patch == nil {
		t.Fatal("Expected patch")
	}

	summary := patch.Summary()
	if summary == "" || summary == "No changes." {
		t.Errorf("Unexpected summary: %q", summary)
	}

	// Just ensure it doesn't panic and clears the inner patch
	patch.Release()
	
	summary2 := patch.Summary()
	if summary2 != "No changes." {
		t.Errorf("Expected 'No changes.' after Release, got %q", summary2)
	}
}

func TestPatch_ApplyResolved_Simple(t *testing.T) {
	type Config struct {
		Value int
	}
	c1 := Config{Value: 10}
	c2 := Config{Value: 20}

	patch := Diff(c1, c2)
	
	target := Config{Value: 10}
	
	// Resolver that rejects everything
	err := patch.ApplyResolved(&target, ConflictResolverFunc(func(path string, op OpKind, old, new any, v reflect.Value) bool {
		return false
	}))
	if err != nil {
		t.Fatalf("ApplyResolved failed: %v", err)
	}
	
	if target.Value != 10 {
		t.Errorf("Value should not have changed, got %d", target.Value)
	}

	// Resolver that accepts everything
	err = patch.ApplyResolved(&target, ConflictResolverFunc(func(path string, op OpKind, old, new any, v reflect.Value) bool {
		return true
	}))
	if err != nil {
		t.Fatalf("ApplyResolved failed: %v", err)
	}
	
	if target.Value != 20 {
		t.Errorf("Value should have changed to 20, got %d", target.Value)
	}
}

type ConflictResolverFunc func(path string, op OpKind, old, new any, v reflect.Value) bool
func (f ConflictResolverFunc) Resolve(path string, op OpKind, old, new any, v reflect.Value) bool {
	return f(path, op, old, new, v)
}

func TestCondition_Aliases(t *testing.T) {
	type Data struct { I int; S string }
	d := Data{I: 10, S: "FOO"}

	tests := []struct {
		name string
		cond Condition[Data]
		want bool
	}{
		{"Ne", Ne[Data]("I", 11), true},
		{"NeFold", NeFold[Data]("S", "bar"), true},
		{"GreaterEqual", GreaterEqual[Data]("I", 10), true},
		{"LessEqual", LessEqual[Data]("I", 10), true},
		{"EqualFieldFold", EqualFieldFold[Data]("S", "S"), true}, // Dummy check
	}

	for _, tt := range tests {
		got, _ := tt.cond.Evaluate(&d)
		if got != tt.want {
			t.Errorf("%s: got %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestRegisterCustomDiff(t *testing.T) {
	type Custom struct {
		Val string
	}

	d := NewDiffer()
	RegisterCustomDiff(d, func(a, b Custom) (Patch[Custom], error) {
		if a.Val == b.Val {
			return nil, nil
		}
		builder := NewBuilder[Custom]()
		node, _ := builder.Root().Field("Val")
		node.Put("CUSTOM:" + b.Val)
		return builder.Build()
	})

	c1 := Custom{Val: "old"}
	c2 := Custom{Val: "new"}

	patch := DiffTyped(d, c1, c2)
	if patch == nil {
		t.Fatal("Expected patch")
	}

	target := Custom{Val: "old"}
	patch.Apply(&target)

	if target.Val != "CUSTOM:new" {
		t.Errorf("Expected CUSTOM:new, got %s", target.Val)
	}
}
