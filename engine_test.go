package deep_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/brunoga/deep/v5"
	"github.com/brunoga/deep/v5/crdt"
	"github.com/brunoga/deep/v5/crdt/hlc"
	"github.com/brunoga/deep/v5/internal/testmodels"
)

func TestCausality(t *testing.T) {
	type Doc struct {
		Title deep.LWW[string]
	}

	clock := hlc.NewClock("node-a")
	ts1 := clock.Now()
	ts2 := clock.Now()

	d1 := Doc{Title: deep.LWW[string]{Value: "Original", Timestamp: ts1}}

	// Newer update
	p1 := deep.NewPatch[Doc]()
	p1.Operations = append(p1.Operations, deep.Operation{
		Kind:      deep.OpReplace,
		Path:      "/Title",
		New:       deep.LWW[string]{Value: "Newer", Timestamp: ts2},
		Timestamp: ts2,
	})

	// Older update (simulating delayed arrival)
	p2 := deep.NewPatch[Doc]()
	p2.Operations = append(p2.Operations, deep.Operation{
		Kind:      deep.OpReplace,
		Path:      "/Title",
		New:       deep.LWW[string]{Value: "Older", Timestamp: ts1},
		Timestamp: ts1,
	})

	// 1. Apply newer then older -> newer should win
	res1 := d1
	deep.Apply(&res1, p1)
	deep.Apply(&res1, p2)
	if res1.Title.Value != "Newer" {
		t.Errorf("newer update lost: got %s, want Newer", res1.Title.Value)
	}

	// 2. Merge patches
	merged := deep.Merge(p1, p2, nil)
	res2 := d1
	deep.Apply(&res2, merged)
	if res2.Title.Value != "Newer" {
		t.Errorf("merged update lost: got %s, want Newer", res2.Title.Value)
	}
}

func TestApplyOperation(t *testing.T) {
	u := testmodels.User{
		ID:   1,
		Name: "Alice",
		Bio:  crdt.Text{{Value: "Hello"}},
	}

	p := deep.NewPatch[testmodels.User]()
	p.Operations = append(p.Operations, deep.Operation{
		Kind: deep.OpReplace,
		Path: "/full_name",
		New:  "Bob",
	})

	if err := deep.Apply(&u, p); err != nil {
		t.Fatalf("Apply failed: %v", err)
	}

	if u.Name != "Bob" {
		t.Errorf("expected Bob, got %s", u.Name)
	}
}

func TestApplyError(t *testing.T) {
	err1 := fmt.Errorf("error 1")
	err2 := fmt.Errorf("error 2")
	ae := &deep.ApplyError{Errors: []error{err1, err2}}

	s := ae.Error()
	if !strings.Contains(s, "2 errors during apply") {
		t.Errorf("expected 2 errors message, got %s", s)
	}
	if !strings.Contains(s, "error 1") || !strings.Contains(s, "error 2") {
		t.Errorf("missing individual errors in message: %s", s)
	}

	aeSingle := &deep.ApplyError{Errors: []error{err1}}
	if aeSingle.Error() != "error 1" {
		t.Errorf("expected error 1, got %s", aeSingle.Error())
	}
}

func TestEngineAdvanced(t *testing.T) {
	u := testmodels.User{ID: 1, Name: "Alice"}

	// Copy
	u2 := deep.Copy(u)
	if !deep.Equal(u, u2) {
		t.Error("Copy or Equal failed")
	}

	// checkType
	if !deep.CheckType("foo", "string") {
		t.Error("deep.CheckType string failed")
	}
	if !deep.CheckType(1, "number") {
		t.Error("deep.CheckType number failed")
	}
	if !deep.CheckType(true, "boolean") {
		t.Error("deep.CheckType boolean failed")
	}
	if !deep.CheckType(u, "object") {
		t.Error("deep.CheckType object failed")
	}
	if !deep.CheckType([]int{}, "array") {
		t.Error("deep.CheckType array failed")
	}
	if !deep.CheckType((*testmodels.User)(nil), "null") {
		t.Error("deep.CheckType null failed")
	}
	if !deep.CheckType(nil, "null") {
		t.Error("deep.CheckType nil null failed")
	}
	if deep.CheckType("foo", "number") {
		t.Error("deep.CheckType invalid failed")
	}

	// evaluateCondition
	root := reflect.ValueOf(u)
	tests := []struct {
		c    *deep.Condition
		want bool
	}{
		{c: &deep.Condition{Op: "exists", Path: "/id"}, want: true},
		{c: &deep.Condition{Op: "exists", Path: "/none"}, want: false},
		{c: &deep.Condition{Op: "matches", Path: "/full_name", Value: "^Al.*$"}, want: true},
		{c: &deep.Condition{Op: "type", Path: "/id", Value: "number"}, want: true},
		{c: deep.And(deep.Eq(deep.Field(func(u *testmodels.User) *int { return &u.ID }), 1), deep.Eq(deep.Field(func(u *testmodels.User) *string { return &u.Name }), "Alice")), want: true},
		{c: deep.Or(deep.Eq(deep.Field(func(u *testmodels.User) *int { return &u.ID }), 2), deep.Eq(deep.Field(func(u *testmodels.User) *string { return &u.Name }), "Alice")), want: true},
		{c: deep.Not(deep.Eq(deep.Field(func(u *testmodels.User) *int { return &u.ID }), 2)), want: true},
	}

	for _, tt := range tests {
		got, err := deep.EvaluateCondition(root, tt.c)
		if err != nil {
			t.Errorf("deep.EvaluateCondition(%s) error: %v", tt.c.Op, err)
		}
		if got != tt.want {
			t.Errorf("deep.EvaluateCondition(%s) = %v, want %v", tt.c.Op, got, tt.want)
		}
	}
}

func TestReflectionEngineAdvanced(t *testing.T) {
	type Data struct {
		A int
		B int
	}
	d := &Data{A: 1, B: 2}

	p := deep.NewPatch[Data]()
	p.Operations = []deep.Operation{
		{Kind: deep.OpMove, Path: "/B", Old: "/A"},
		{Kind: deep.OpCopy, Path: "/A", Old: "/B"},
		{Kind: deep.OpRemove, Path: "/A"},
	}

	if err := deep.Apply(d, p); err != nil {
		t.Errorf("Apply failed: %v", err)
	}
}

func TestEngineFailures(t *testing.T) {
	u := &testmodels.User{}

	// Move from non-existent
	p1 := deep.NewPatch[testmodels.User]()
	p1.Operations = []deep.Operation{{Kind: deep.OpMove, Path: "/id", Old: "/nonexistent"}}
	deep.Apply(u, p1)

	// Copy from non-existent
	p2 := deep.NewPatch[testmodels.User]()
	p2.Operations = []deep.Operation{{Kind: deep.OpCopy, Path: "/id", Old: "/nonexistent"}}
	deep.Apply(u, p2)

	// Apply to nil
	if err := deep.Apply((*testmodels.User)(nil), p1); err == nil {
		t.Error("Apply to nil should fail")
	}
}

func TestFinalPush(t *testing.T) {
	// 1. All deep.OpKinds
	for i := 0; i < 10; i++ {
		_ = deep.OpKind(i).String()
	}

	// 2. deep.Condition failures
	u := testmodels.User{ID: 1, Name: "Alice"}
	root := reflect.ValueOf(u)

	// OR failure
	deep.Or(deep.Eq(deep.Field(func(u *testmodels.User) *int { return &u.ID }), 2), deep.Eq(deep.Field(func(u *testmodels.User) *int { return &u.ID }), 3))
	deep.EvaluateCondition(root, &deep.Condition{Op: "or", Apply: []*deep.Condition{
		{Op: "==", Path: "/id", Value: 2},
		{Op: "==", Path: "/id", Value: 3},
	}})

	// NOT failure
	deep.EvaluateCondition(root, &deep.Condition{Op: "not", Apply: []*deep.Condition{
		{Op: "==", Path: "/id", Value: 1},
	}})

	// Nested delegation failure (nil field)
	type NestedNil struct {
		User *testmodels.User
	}
	nn := &NestedNil{}
	deep.Apply(nn, deep.Patch[NestedNil]{Operations: []deep.Operation{{Kind: deep.OpReplace, Path: "/User/id", New: 1}}})
}

func TestReflectionEqualCopy(t *testing.T) {
	type Simple struct {
		A int
	}
	s1 := Simple{A: 1}
	s2 := Simple{A: 2}

	if deep.Equal(s1, s2) {
		t.Error("deep.Equal failed for different simple structs")
	}

	s3 := deep.Copy(s1)
	if s3.A != 1 {
		t.Error("deep.Copy failed for simple struct")
	}
}

func TestTextAdvanced(t *testing.T) {
	clock := hlc.NewClock("node-a")
	t1 := clock.Now()
	t2 := clock.Now()

	// Complex ordering
	text := crdt.Text{
		{ID: t2, Value: "world", Prev: t1},
		{ID: t1, Value: "hello "},
	}

	s := text.String()
	if s != "hello world" {
		t.Errorf("expected hello world, got %q", s)
	}

	// deep.ApplyOperation
	text2 := crdt.Text{{Value: "old"}}
	op := deep.Operation{
		Kind: deep.OpReplace,
		Path: "/",
		New:  crdt.Text{{Value: "new"}},
	}
	text2.ApplyOperation(op)
}

func BenchmarkApply(b *testing.B) {
	u1 := testmodels.User{ID: 1, Name: "Alice"}
	u2 := testmodels.User{ID: 1, Name: "Bob"}
	p := deep.Diff(u1, u2)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		u3 := u1
		deep.Apply(&u3, p)
	}
}
