package deep_test

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"strings"
	"testing"

	"github.com/brunoga/deep/v5"
	"github.com/brunoga/deep/v5/crdt/hlc"
	"github.com/brunoga/deep/v5/internal/testmodels"
)

func TestGobSerialization(t *testing.T) {
	deep.Register[testmodels.User]()

	u1 := testmodels.User{ID: 1, Name: "Alice"}
	u2 := testmodels.User{ID: 2, Name: "Bob"}
	patch, err := deep.Diff(u1, u2)
	if err != nil {
		t.Fatalf("Diff failed: %v", err)
	}

	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	if err := enc.Encode(patch); err != nil {
		t.Fatalf("Gob Encode failed: %v", err)
	}

	var patch2 deep.Patch[testmodels.User]
	dec := gob.NewDecoder(&buf)
	if err := dec.Decode(&patch2); err != nil {
		t.Fatalf("Gob Decode failed: %v", err)
	}

	u3 := u1
	deep.Apply(&u3, patch2)
	if !deep.Equal(u2, u3) {
		t.Errorf("Gob roundtrip failed: got %+v, want %+v", u3, u2)
	}
}

func TestReverse(t *testing.T) {
	u1 := testmodels.User{ID: 1, Name: "Alice"}
	u2 := testmodels.User{ID: 2, Name: "Bob"}

	// 1. Create patch u1 -> u2
	patch, err := deep.Diff(u1, u2)
	if err != nil {
		t.Fatalf("Diff failed: %v", err)
	}

	// 2. Reverse patch
	reverse := patch.Reverse()

	// 3. Apply reverse to u2
	u3 := u2
	if err := deep.Apply(&u3, reverse); err != nil {
		t.Fatalf("Reverse apply failed: %v", err)
	}

	// 4. Verify we are back to u1
	if !deep.Equal(u1, u3) {
		t.Errorf("Reverse failed: got %+v, want %+v", u3, u1)
	}
}

func TestPatchToJSONPatch(t *testing.T) {
	deep.Register[testmodels.User]()

	p := deep.Patch[testmodels.User]{}
	p.Operations = []deep.Operation{
		{Kind: deep.OpReplace, Path: "/full_name", Old: "Alice", New: "Bob"},
	}
	p = p.WithGuard(deep.Eq(deep.Field(func(u *testmodels.User) *int { return &u.ID }), 1))

	data, err := p.ToJSONPatch()
	if err != nil {
		t.Fatalf("ToJSONPatch failed: %v", err)
	}

	var raw []map[string]any
	json.Unmarshal(data, &raw)

	if len(raw) != 2 {
		t.Fatalf("expected 2 ops (global condition + replace), got %d", len(raw))
	}

	if raw[0]["op"] != "test" {
		t.Errorf("expected first op to be test (global condition), got %v", raw[0]["op"])
	}
}

func TestPatchUtilities(t *testing.T) {
	p := deep.Patch[testmodels.User]{}
	p.Operations = []deep.Operation{
		{Kind: deep.OpAdd, Path: "/a", New: 1},
		{Kind: deep.OpRemove, Path: "/b", Old: 2},
		{Kind: deep.OpReplace, Path: "/c", Old: 3, New: 4},
		{Kind: deep.OpMove, Path: "/d", Old: "/e"},
		{Kind: deep.OpCopy, Path: "/f", Old: "/g"},
		{Kind: deep.OpLog, Path: "/h", New: "msg"},
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
	// Operation.Strict is stamped from Patch.Strict at apply time, not at build time.
	// Verify ops in the built patch do not carry the flag (it's runtime-only).
	for _, op := range p2.Operations {
		if op.Strict {
			t.Error("WithStrict should not pre-stamp Strict onto operations before Apply")
		}
	}
}

func TestConditionToPredicate(t *testing.T) {
	tests := []struct {
		c    *deep.Condition
		want string
	}{
		{c: &deep.Condition{Op: "!=", Path: "/a", Value: 1}, want: `"op":"not"`},
		{c: &deep.Condition{Op: ">", Path: "/a", Value: 1}, want: `"op":"more"`},
		{c: &deep.Condition{Op: "<", Path: "/a", Value: 1}, want: `"op":"less"`},
		{c: &deep.Condition{Op: "exists", Path: "/a"}, want: `"op":"defined"`},
		{c: &deep.Condition{Op: "matches", Path: "/a", Value: ".*"}, want: `"op":"matches"`},
		{c: &deep.Condition{Op: "type", Path: "/a", Value: "string"}, want: `"op":"type"`},
		{c: deep.Or(deep.Eq(deep.Field(func(u *testmodels.User) *int { return &u.ID }), 1)), want: `"op":"or"`},
	}

	for _, tt := range tests {
		got, err := deep.Patch[testmodels.User]{}.WithGuard(tt.c).ToJSONPatch()
		if err != nil {
			t.Fatalf("ToJSONPatch failed: %v", err)
		}
		if !strings.Contains(string(got), tt.want) {
			t.Errorf("toPredicate(%s) = %s, want %s", tt.c.Op, string(got), tt.want)
		}
	}
}

func TestPatchReverseExhaustive(t *testing.T) {
	p := deep.Patch[testmodels.User]{}
	p.Operations = []deep.Operation{
		{Kind: deep.OpAdd, Path: "/a", New: 1},
		{Kind: deep.OpRemove, Path: "/b", Old: 2},
		{Kind: deep.OpReplace, Path: "/c", Old: 3, New: 4},
		{Kind: deep.OpMove, Path: "/d", Old: "/e"},
		{Kind: deep.OpCopy, Path: "/f", Old: "/g"},
		{Kind: deep.OpLog, Path: "/h", New: "msg"},
	}

	rev := p.Reverse()
	if len(rev.Operations) != 6 {
		t.Errorf("expected 6 reversed ops, got %d", len(rev.Operations))
	}
}

func TestPatchMergeCustom(t *testing.T) {
	p1 := deep.Patch[testmodels.User]{}
	p1.Operations = []deep.Operation{{Path: "/a", New: 1}}
	p2 := deep.Patch[testmodels.User]{}
	p2.Operations = []deep.Operation{{Path: "/a", New: 2}}

	res := deep.Merge(p1, p2, &localResolver{})
	if res.Operations[0].New != 2 {
		t.Error("Merge custom resolution failed")
	}
}

type localResolver struct{}

func (r *localResolver) Resolve(path string, local, remote any) any { return remote }

func TestPatchIsEmpty(t *testing.T) {
	p := deep.Patch[testmodels.User]{}
	if !p.IsEmpty() {
		t.Error("new patch should be empty")
	}
	p.Operations = append(p.Operations, deep.Operation{Kind: deep.OpAdd, Path: "/name", New: "x"})
	if p.IsEmpty() {
		t.Error("patch with operations should not be empty")
	}
}

func TestFromJSONPatchRoundTrip(t *testing.T) {
	type Doc struct {
		Name  string `json:"name"`
		Alias string `json:"alias"`
		Age   int    `json:"age"`
	}

	// Build a patch with all supported op types and conditions.
	namePath := deep.Field[Doc, string](func(d *Doc) *string { return &d.Name })
	aliasPath := deep.Field[Doc, string](func(d *Doc) *string { return &d.Alias })
	agePath := deep.Field[Doc, int](func(d *Doc) *int { return &d.Age })

	original := deep.Edit(&Doc{}).
		With(
			deep.Set(namePath, "Alice"),
			deep.Add(agePath, 30),
			deep.Remove(namePath),
			deep.Move(namePath, aliasPath),
			deep.Copy(namePath, aliasPath).If(deep.Eq(namePath, "Alice")),
		).
		Log("done").
		Where(deep.Gt(agePath, 18)).
		Build()

	data, err := original.ToJSONPatch()
	if err != nil {
		t.Fatalf("ToJSONPatch: %v", err)
	}

	rt, err := deep.FromJSONPatch[Doc](data)
	if err != nil {
		t.Fatalf("FromJSONPatch: %v", err)
	}

	if len(rt.Operations) != len(original.Operations) {
		t.Errorf("op count: got %d, want %d", len(rt.Operations), len(original.Operations))
	}
	if rt.Guard == nil {
		t.Error("global condition not round-tripped")
	}
	if rt.Guard != nil && rt.Guard.Op != ">" {
		t.Errorf("global condition op: got %q, want \">\"", rt.Guard.Op)
	}
}

func TestGeLeConditions(t *testing.T) {
	type S struct{ X int }
	xPath := deep.Field[S, int](func(s *S) *int { return &s.X })

	s := S{X: 5}
	if err := deep.Apply(&s, deep.Edit(&s).With(deep.Set(xPath, 10).Unless(deep.Ge(xPath, 5))).Build()); err != nil {
		t.Fatal(err)
	}
	// Ge(X, 5) is true when X==5, so Unless fires and op is skipped → X stays 5.
	if s.X != 5 {
		t.Errorf("Ge condition: got %d, want 5", s.X)
	}

	if err := deep.Apply(&s, deep.Edit(&s).With(deep.Set(xPath, 10).Unless(deep.Le(xPath, 4))).Build()); err != nil {
		t.Fatal(err)
	}
	// Le(X, 4) is false when X==5, so Unless does not fire → X becomes 10.
	if s.X != 10 {
		t.Errorf("Le condition: got %d, want 10", s.X)
	}
}

func TestBuilderMoveCopy(t *testing.T) {
	type S struct {
		A string `json:"a"`
		B string `json:"b"`
	}
	aPath := deep.Field[S, string](func(s *S) *string { return &s.A })
	bPath := deep.Field[S, string](func(s *S) *string { return &s.B })

	p := deep.Edit(&S{}).With(deep.Move(aPath, bPath)).Build()
	if len(p.Operations) != 1 || p.Operations[0].Kind != deep.OpMove {
		t.Error("Move not added correctly")
	}
	if p.Operations[0].Old != aPath.String() || p.Operations[0].Path != bPath.String() {
		t.Errorf("Move paths wrong: from=%v to=%v", p.Operations[0].Old, p.Operations[0].Path)
	}

	p2 := deep.Edit(&S{}).With(deep.Copy(aPath, bPath)).Build()
	if len(p2.Operations) != 1 || p2.Operations[0].Kind != deep.OpCopy {
		t.Error("Copy not added correctly")
	}
}

func TestLWWSet(t *testing.T) {
	deep.Register[string]()
	clock := hlc.NewClock("test")
	ts1 := clock.Now()
	ts2 := clock.Now()

	var reg deep.LWW[string]
	if reg.Set("first", ts1); reg.Value != "first" {
		t.Error("LWW.Set should accept first value")
	}
	if reg.Set("second", ts2); reg.Value != "second" {
		t.Error("LWW.Set should accept newer timestamp")
	}
	if accepted := reg.Set("old", ts1); accepted || reg.Value != "second" {
		t.Error("LWW.Set should reject older timestamp")
	}
}
