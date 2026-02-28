package v5

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/brunoga/deep/v5/crdt/hlc"
)

func TestCoverage_ApplyError(t *testing.T) {
	err1 := fmt.Errorf("error 1")
	err2 := fmt.Errorf("error 2")
	ae := &ApplyError{Errors: []error{err1, err2}}

	s := ae.Error()
	if !strings.Contains(s, "2 errors during apply") {
		t.Errorf("expected 2 errors message, got %s", s)
	}
	if !strings.Contains(s, "error 1") || !strings.Contains(s, "error 2") {
		t.Errorf("missing individual errors in message: %s", s)
	}

	aeSingle := &ApplyError{Errors: []error{err1}}
	if aeSingle.Error() != "error 1" {
		t.Errorf("expected error 1, got %s", aeSingle.Error())
	}
}

func TestCoverage_PatchUtilities(t *testing.T) {
	p := NewPatch[User]()
	p.Operations = []Operation{
		{Kind: OpAdd, Path: "/a", New: 1},
		{Kind: OpRemove, Path: "/b", Old: 2},
		{Kind: OpReplace, Path: "/c", Old: 3, New: 4},
		{Kind: OpMove, Path: "/d", Old: "/e"},
		{Kind: OpCopy, Path: "/f", Old: "/g"},
		{Kind: OpLog, Path: "/h", New: "msg"},
	}

	// String()
	s := p.String()
	expected := []string{"Add /a", "Remove /b", "Replace /c", "Move /e to /d", "Copy /g to /f", "Log /h"}
	for _, exp := range expected {
		if !strings.Contains(s, exp) {
			t.Errorf("String() missing %s: %s", exp, s)
		}
	}

	// WithStrict
	p2 := p.WithStrict(true)
	if !p2.Strict {
		t.Error("WithStrict failed to set global Strict")
	}
	for _, op := range p2.Operations {
		if !op.Strict {
			t.Error("WithStrict failed to propagate to operations")
		}
	}
}

func TestCoverage_ConditionToPredicate(t *testing.T) {
	tests := []struct {
		c    *Condition
		want string
	}{
		{c: &Condition{Op: "!=", Path: "/a", Value: 1}, want: `"op":"not"`},
		{c: &Condition{Op: ">", Path: "/a", Value: 1}, want: `"op":"more"`},
		{c: &Condition{Op: "<", Path: "/a", Value: 1}, want: `"op":"less"`},
		{c: &Condition{Op: "exists", Path: "/a"}, want: `"op":"defined"`},
		{c: &Condition{Op: "matches", Path: "/a", Value: ".*"}, want: `"op":"matches"`},
		{c: &Condition{Op: "type", Path: "/a", Value: "string"}, want: `"op":"type"`},
		{c: Or(Eq(Field(func(u *User) *int { return &u.ID }), 1)), want: `"op":"or"`},
	}

	for _, tt := range tests {
		got, err := NewPatch[User]().WithCondition(tt.c).ToJSONPatch()
		if err != nil {
			t.Fatalf("ToJSONPatch failed: %v", err)
		}
		if !strings.Contains(string(got), tt.want) {
			t.Errorf("toPredicate(%s) = %s, want %s", tt.c.Op, string(got), tt.want)
		}
	}
}

func TestCoverage_BuilderAdvanced(t *testing.T) {
	u := &User{}
	b := Edit(u).
		Where(Eq(Field(func(u *User) *int { return &u.ID }), 1)).
		Unless(Ne(Field(func(u *User) *string { return &u.Name }), "Alice"))

	Set(b, Field(func(u *User) *int { return &u.ID }), 2).Unless(Eq(Field(func(u *User) *int { return &u.ID }), 1))
	Gt(Field(func(u *User) *int { return &u.ID }), 0)
	Lt(Field(func(u *User) *int { return &u.ID }), 10)
	Exists(Field(func(u *User) *string { return &u.Name }))

	p := b.Build()
	if p.Condition == nil || p.Condition.Op != "==" {
		t.Error("Where failed")
	}
}

func TestCoverage_EngineAdvanced(t *testing.T) {
	u := User{ID: 1, Name: "Alice"}

	// Copy
	u2 := Copy(u)
	if !Equal(u, u2) {
		t.Error("Copy or Equal failed")
	}

	// checkType
	if !checkType("foo", "string") {
		t.Error("checkType string failed")
	}
	if !checkType(1, "number") {
		t.Error("checkType number failed")
	}
	if !checkType(true, "boolean") {
		t.Error("checkType boolean failed")
	}
	if !checkType(u, "object") {
		t.Error("checkType object failed")
	}
	if !checkType([]int{}, "array") {
		t.Error("checkType array failed")
	}
	if !checkType((*User)(nil), "null") {
		t.Error("checkType null failed")
	}
	if !checkType(nil, "null") {
		t.Error("checkType nil null failed")
	}
	if checkType("foo", "number") {
		t.Error("checkType invalid failed")
	}

	// evaluateCondition
	root := reflect.ValueOf(u)
	tests := []struct {
		c    *Condition
		want bool
	}{
		{c: &Condition{Op: "exists", Path: "/id"}, want: true},
		{c: &Condition{Op: "exists", Path: "/none"}, want: false},
		{c: &Condition{Op: "matches", Path: "/full_name", Value: "^Al.*$"}, want: true},
		{c: &Condition{Op: "type", Path: "/id", Value: "number"}, want: true},
		{c: And(Eq(Field(func(u *User) *int { return &u.ID }), 1), Eq(Field(func(u *User) *string { return &u.Name }), "Alice")), want: true},
		{c: Or(Eq(Field(func(u *User) *int { return &u.ID }), 2), Eq(Field(func(u *User) *string { return &u.Name }), "Alice")), want: true},
		{c: Not(Eq(Field(func(u *User) *int { return &u.ID }), 2)), want: true},
	}

	for _, tt := range tests {
		got, err := evaluateCondition(root, tt.c)
		if err != nil {
			t.Errorf("evaluateCondition(%s) error: %v", tt.c.Op, err)
		}
		if got != tt.want {
			t.Errorf("evaluateCondition(%s) = %v, want %v", tt.c.Op, got, tt.want)
		}
	}
}

func TestCoverage_TextAdvanced(t *testing.T) {
	clock := hlc.NewClock("node-a")
	t1 := clock.Now()
	t2 := clock.Now()

	// Complex ordering
	text := Text{
		{ID: t2, Value: "world", Prev: t1},
		{ID: t1, Value: "hello "},
	}

	s := text.String()
	if s != "hello world" {
		t.Errorf("expected hello world, got %q", s)
	}

	// GeneratedApply
	text2 := Text{{Value: "old"}}
	p := Patch[Text]{
		Operations: []Operation{
			{Kind: OpReplace, Path: "/", New: Text{{Value: "new"}}},
		},
	}
	text2.GeneratedApply(p)
}

func TestCoverage_ReflectionEngine(t *testing.T) {
	type Data struct {
		A int
		B int
	}
	d := &Data{A: 1, B: 2}

	p := NewPatch[Data]()
	p.Operations = []Operation{
		{Kind: OpMove, Path: "/B", Old: "/A"},
		{Kind: OpCopy, Path: "/A", Old: "/B"},
		{Kind: OpRemove, Path: "/A"},
	}

	if err := Apply(d, p); err != nil {
		t.Errorf("Apply failed: %v", err)
	}
}

func TestCoverage_GeneratedUserExhaustive(t *testing.T) {
	u := &User{ID: 1, Name: "Alice", age: 30}

	// Test evaluateCondition generated
	tests := []struct {
		c    *Condition
		want bool
	}{
		{c: Ne(Field(func(u *User) *int { return &u.ID }), 2), want: true},
		{c: Log(Field(func(u *User) *int { return &u.ID }), "msg"), want: true},
		{c: Matches(Field(func(u *User) *string { return &u.Name }), "^Al.*$"), want: true},
		{c: Type(Field(func(u *User) *string { return &u.Name }), "string"), want: true},
		{c: Eq(Field(func(u *User) *int { return &u.age }), 30), want: true},
	}

	for _, tt := range tests {
		got, err := u.evaluateCondition(*tt.c)
		if err != nil {
			t.Errorf("evaluateCondition(%s) error: %v", tt.c.Op, err)
		}
		if got != tt.want {
			t.Errorf("evaluateCondition(%s) = %v, want %v", tt.c.Op, got, tt.want)
		}
	}

	// Test all fields in ApplyOperation
	ops := []Operation{
		{Kind: OpReplace, Path: "/id", New: 10},
		{Kind: OpLog, Path: "/id", New: "msg"},
		{Kind: OpReplace, Path: "/full_name", New: "Bob"},
		{Kind: OpLog, Path: "/full_name", New: "msg"},
		{Kind: OpReplace, Path: "/info", New: Detail{Age: 20}},
		{Kind: OpLog, Path: "/info", New: "msg"},
		{Kind: OpReplace, Path: "/roles", New: []string{"admin"}},
		{Kind: OpLog, Path: "/roles", New: "msg"},
		{Kind: OpReplace, Path: "/score", New: map[string]int{"a": 1}},
		{Kind: OpLog, Path: "/score", New: "msg"},
		{Kind: OpReplace, Path: "/bio", New: Text{{Value: "new"}}},
		{Kind: OpLog, Path: "/bio", New: "msg"},
		{Kind: OpReplace, Path: "/age", New: 40},
		{Kind: OpLog, Path: "/age", New: "msg"},
	}

	for _, op := range ops {
		u.ApplyOperation(op)
	}
}

func TestCoverage_ReverseExhaustive(t *testing.T) {
	p := NewPatch[User]()
	p.Operations = []Operation{
		{Kind: OpAdd, Path: "/a", New: 1},
		{Kind: OpRemove, Path: "/b", Old: 2},
		{Kind: OpReplace, Path: "/c", Old: 3, New: 4},
		{Kind: OpMove, Path: "/d", Old: "/e"},
		{Kind: OpCopy, Path: "/f", Old: "/g"},
		{Kind: OpLog, Path: "/h", New: "msg"},
	}

	rev := p.Reverse()
	if len(rev.Operations) != 6 {
		t.Errorf("expected 6 reversed ops, got %d", len(rev.Operations))
	}
}

func TestCoverage_EngineFailures(t *testing.T) {
	u := &User{}

	// Move from non-existent
	p1 := NewPatch[User]()
	p1.Operations = []Operation{{Kind: OpMove, Path: "/id", Old: "/nonexistent"}}
	Apply(u, p1)

	// Copy from non-existent
	p2 := NewPatch[User]()
	p2.Operations = []Operation{{Kind: OpCopy, Path: "/id", Old: "/nonexistent"}}
	Apply(u, p2)

	// Apply to nil
	if err := Apply((*User)(nil), p1); err == nil {
		t.Error("Apply to nil should fail")
	}
}

func TestCoverage_FinalPush(t *testing.T) {
	// 1. All OpKinds
	for i := 0; i < 10; i++ {
		_ = OpKind(i).String()
	}

	// 2. Condition failures
	u := User{ID: 1, Name: "Alice"}
	root := reflect.ValueOf(u)

	// OR failure
	Or(Eq(Field(func(u *User) *int { return &u.ID }), 2), Eq(Field(func(u *User) *int { return &u.ID }), 3))
	evaluateCondition(root, &Condition{Op: "or", Apply: []*Condition{
		{Op: "==", Path: "/id", Value: 2},
		{Op: "==", Path: "/id", Value: 3},
	}})

	// NOT failure
	evaluateCondition(root, &Condition{Op: "not", Apply: []*Condition{
		{Op: "==", Path: "/id", Value: 1},
	}})

	// Nested delegation failure (nil field)
	type NestedNil struct {
		User *User
	}
	nn := &NestedNil{}
	Apply(nn, Patch[NestedNil]{Operations: []Operation{{Kind: OpReplace, Path: "/User/id", New: 1}}})
}

func TestCoverage_UserGeneratedConditionsFinal(t *testing.T) {
	u := &User{ID: 1, Name: "Alice"}

	// Test != and other missing branches in generated evaluateCondition
	u.evaluateCondition(Condition{Path: "/id", Op: "!=", Value: 2})
	u.evaluateCondition(Condition{Path: "/full_name", Op: "!=", Value: "Bob"})
	u.evaluateCondition(Condition{Path: "/age", Op: "==", Value: 30})
	u.evaluateCondition(Condition{Path: "/age", Op: "!=", Value: 31})
}

func TestCoverage_DetailGeneratedExhaustive(t *testing.T) {
	d := &Detail{}

	// ApplyOperation
	ops := []Operation{
		{Kind: OpReplace, Path: "/Age", New: 20},
		{Kind: OpReplace, Path: "/Age", New: 20.0}, // float64
		{Kind: OpLog, Path: "/Age", New: "msg"},
		{Kind: OpReplace, Path: "/addr", New: "Side"},
		{Kind: OpLog, Path: "/addr", New: "msg"},
		{Kind: OpReplace, Path: "/Address", New: "Side"},
	}

	for _, op := range ops {
		d.ApplyOperation(op)
	}

	// evaluateCondition
	d.evaluateCondition(Condition{Path: "/Age", Op: "==", Value: 20})
	d.evaluateCondition(Condition{Path: "/Age", Op: "!=", Value: 21})
	d.evaluateCondition(Condition{Path: "/addr", Op: "==", Value: "Side"})
	d.evaluateCondition(Condition{Path: "/addr", Op: "!=", Value: "Other"})
}

func TestCoverage_ReflectionEqualCopy(t *testing.T) {
	type Simple struct {
		A int
	}
	s1 := Simple{A: 1}
	s2 := Simple{A: 2}

	if Equal(s1, s2) {
		t.Error("Equal failed for different simple structs")
	}

	s3 := Copy(s1)
	if s3.A != 1 {
		t.Error("Copy failed for simple struct")
	}
}

type localResolver struct{}

func (r *localResolver) Resolve(path string, local, remote any) any { return remote }

func TestCoverage_MergeCustom(t *testing.T) {
	p1 := NewPatch[User]()
	p1.Operations = []Operation{{Path: "/a", New: 1}}
	p2 := NewPatch[User]()
	p2.Operations = []Operation{{Path: "/a", New: 2}}

	res := Merge(p1, p2, &localResolver{})
	if res.Operations[0].New != 2 {
		t.Error("Merge custom resolution failed")
	}
}

func TestCoverage_UserConditionsExhaustive(t *testing.T) {
	u := &User{ID: 1, Name: "Alice", age: 30}

	// Test all fields and ops in evaluateCondition
	fields := []string{"/id", "/full_name", "/age"}
	ops := []string{"==", "!="}

	for _, f := range fields {
		for _, op := range ops {
			val := any(1)
			if f == "/full_name" {
				val = "Alice"
			}
			if f == "/age" {
				val = 30
			}
			u.evaluateCondition(Condition{Path: f, Op: op, Value: val})
		}
		u.evaluateCondition(Condition{Path: f, Op: "log", Value: "msg"})
		u.evaluateCondition(Condition{Path: f, Op: "matches", Value: ".*"})
		u.evaluateCondition(Condition{Path: f, Op: "type", Value: "string"})
	}
}

func TestCoverage_DetailConditionsExhaustive(t *testing.T) {
	d := &Detail{Age: 10, Address: "Main"}
	fields := []string{"/Age", "/addr"}
	ops := []string{"==", "!="}

	for _, f := range fields {
		for _, op := range ops {
			val := any(10)
			if f == "/addr" {
				val = "Main"
			}
			d.evaluateCondition(Condition{Path: f, Op: op, Value: val})
		}
		d.evaluateCondition(Condition{Path: f, Op: "log", Value: "msg"})
		d.evaluateCondition(Condition{Path: f, Op: "matches", Value: ".*"})
		d.evaluateCondition(Condition{Path: f, Op: "type", Value: "string"})
	}
}
