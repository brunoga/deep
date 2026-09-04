package deep_test

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"strings"
	"testing"

	"github.com/brunoga/deep/v6"
	"github.com/brunoga/deep/v6/condition"
	"github.com/brunoga/deep/v6/crdt"
	"github.com/brunoga/deep/v6/crdt/hlc"
	"github.com/brunoga/deep/v6/internal/testmodels"
)

func TestGobSerialization(t *testing.T) {
	gob.Register(deep.Patch[testmodels.User]{})
	gob.Register(deep.Operation{})
	gob.Register(testmodels.User{})
	gob.Register([]testmodels.User{})
	gob.Register(map[string]testmodels.User{})

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
		{Kind: deep.OpMove, Path: "/d", From: "/e"},
		{Kind: deep.OpCopy, Path: "/f", From: "/g"},
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

	// AsStrict
	p2 := p.AsStrict()
	if !p2.Strict {
		t.Error("AsStrict failed to set global Strict")
	}
	// Operation.Strict is stamped from Patch.Strict at apply time, not at build time.
	// Verify ops in the built patch do not carry the flag (it's runtime-only).
	for _, op := range p2.Operations {
		if op.Strict {
			t.Error("AsStrict should not pre-stamp Strict onto operations before Apply")
		}
	}
}

func TestConditionToPredicate(t *testing.T) {
	tests := []struct {
		c    *condition.Condition
		want string
	}{
		{c: &condition.Condition{Op: "!=", Path: "/a", Value: 1}, want: `"op":"not"`},
		{c: &condition.Condition{Op: ">", Path: "/a", Value: 1}, want: `"op":"more"`},
		{c: &condition.Condition{Op: "<", Path: "/a", Value: 1}, want: `"op":"less"`},
		{c: &condition.Condition{Op: "exists", Path: "/a"}, want: `"op":"defined"`},
		{c: &condition.Condition{Op: "matches", Path: "/a", Value: ".*"}, want: `"op":"matches"`},
		{c: &condition.Condition{Op: "type", Path: "/a", Value: "string"}, want: `"op":"type"`},
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
		{Kind: deep.OpMove, Path: "/d", From: "/e"},
		{Kind: deep.OpCopy, Path: "/f", From: "/g"},
		{Kind: deep.OpLog, Path: "/h", New: "msg"},
	}

	rev := p.Reverse()
	// OpLog has no state effect; Reverse skips it instead of emitting a
	// malformed op whose Kind defaults to OpAdd.
	if len(rev.Operations) != 5 {
		t.Errorf("expected 5 reversed ops (OpLog skipped), got %d", len(rev.Operations))
	}
	for _, op := range rev.Operations {
		if op.Kind == deep.OpLog {
			t.Errorf("Reverse should drop OpLog, got %+v", op)
		}
		// Reversing OpLog used to emit {Kind:OpAdd, Path:"/h", New:nil}; guard
		// against that exact regression.
		if op.Path == "/h" {
			t.Errorf("Reverse leaked OpLog at /h as an OpAdd: %+v", op)
		}
	}
}

// TestPatchReverseOpCopyWithPriorValue asserts that when an OpCopy carries the
// displaced destination value in Old, Reverse emits an OpReplace that restores
// that value rather than an OpRemove that strands it.
func TestPatchReverseOpCopyWithPriorValue(t *testing.T) {
	p := deep.Patch[testmodels.User]{}
	p.Operations = []deep.Operation{
		// Pre-copy /dst held "before"; copy overwrote it with "after".
		{Kind: deep.OpCopy, Path: "/dst", From: "/src", Old: "before", New: "after"},
	}
	rev := p.Reverse()
	if len(rev.Operations) != 1 {
		t.Fatalf("expected 1 reversed op, got %d", len(rev.Operations))
	}
	got := rev.Operations[0]
	if got.Kind != deep.OpReplace {
		t.Errorf("reverse of OpCopy with prior value should be OpReplace, got %v", got.Kind)
	}
	if got.Path != "/dst" {
		t.Errorf("reverse target path = %q, want /dst", got.Path)
	}
	if got.Old != "after" || got.New != "before" {
		t.Errorf("reverse should restore prior value: got Old=%v New=%v, want Old=after New=before", got.Old, got.New)
	}
}

// TestPatchReverseOpMoveSymmetric asserts OpMove reverses by swapping From and
// Path, restoring the original location.
func TestPatchReverseOpMoveSymmetric(t *testing.T) {
	p := deep.Patch[testmodels.User]{}
	p.Operations = []deep.Operation{
		{Kind: deep.OpMove, Path: "/dst", From: "/src"},
	}
	rev := p.Reverse()
	if len(rev.Operations) != 1 {
		t.Fatalf("expected 1 reversed op, got %d", len(rev.Operations))
	}
	got := rev.Operations[0]
	if got.Kind != deep.OpMove || got.Path != "/src" || got.From != "/dst" {
		t.Errorf("reverse OpMove: got Path=%s From=%s, want Path=/src From=/dst", got.Path, got.From)
	}
}

// TestPatchReverseOpLogOnly asserts that a patch containing only OpLog ops
// reverses to an empty patch rather than a sequence of malformed OpAdds.
func TestPatchReverseOpLogOnly(t *testing.T) {
	p := deep.Patch[testmodels.User]{}
	p.Operations = []deep.Operation{
		{Kind: deep.OpLog, Path: "/", New: "first"},
		{Kind: deep.OpLog, Path: "/", New: "second"},
	}
	rev := p.Reverse()
	if len(rev.Operations) != 0 {
		t.Errorf("expected empty reverse of OpLog-only patch, got %+v", rev.Operations)
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

// TestParseJSONPatchGuardOnlyLeading asserts the Guard-extraction heuristic
// only fires on the leading entry. A later {"op":"test","path":"/","if":...}
// must not overwrite Guard or be re-interpreted as a second guard; deep does
// not model standalone test ops, so trailing tests are dropped as unknown.
func TestParseJSONPatchGuardOnlyLeading(t *testing.T) {
	raw := []byte(`[
		{"op":"test","path":"/","if":{"op":"more","path":"/age","value":18}},
		{"op":"replace","path":"/name","value":"Alice"},
		{"op":"test","path":"/","if":{"op":"more","path":"/age","value":99}}
	]`)
	type Doc struct {
		Name string `json:"name"`
		Age  int    `json:"age"`
	}
	p, err := deep.ParseJSONPatch[Doc](raw)
	if err != nil {
		t.Fatalf("ParseJSONPatch: %v", err)
	}
	if p.Guard == nil {
		t.Fatal("expected leading test op to be lifted into Guard")
	}
	// The leading test specified Gt(/age, 18); the trailing one carries
	// Gt(/age, 99). If the trailing entry were also lifted into Guard, the
	// 99 value would clobber the 18.
	if gv, ok := p.Guard.Value.(float64); !ok || gv != 18 {
		t.Errorf("Guard value should remain 18 from leading test, got %v", p.Guard.Value)
	}
}

func TestParseJSONPatchRoundTrip(t *testing.T) {
	type Doc struct {
		Name  string `json:"name"`
		Alias string `json:"alias"`
		Age   int    `json:"age"`
	}

	// Build a patch with all supported op types and conditions.
	namePath := deep.Field[Doc, string](func(d *Doc) *string { return &d.Name })
	aliasPath := deep.Field[Doc, string](func(d *Doc) *string { return &d.Alias })
	agePath := deep.Field[Doc, int](func(d *Doc) *int { return &d.Age })

	original := deep.NewPatch[Doc]().
		With(
			deep.Set(namePath, "Alice"),
			deep.Add(agePath, 30),
			deep.Remove(namePath),
			deep.Move(namePath, aliasPath),
			deep.Copy(namePath, aliasPath).If(deep.Eq(namePath, "Alice")),
		).
		Log("done").
		Guard(deep.Gt(agePath, 18)).
		Build()

	data, err := original.ToJSONPatch()
	if err != nil {
		t.Fatalf("ToJSONPatch: %v", err)
	}

	rt, err := deep.ParseJSONPatch[Doc](data)
	if err != nil {
		t.Fatalf("ParseJSONPatch: %v", err)
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
	if err := deep.Apply(&s, deep.NewPatch[S]().With(deep.Set(xPath, 10).Unless(deep.Ge(xPath, 5))).Build()); err != nil {
		t.Fatal(err)
	}
	// Ge(X, 5) is true when X==5, so Unless fires and op is skipped → X stays 5.
	if s.X != 5 {
		t.Errorf("Ge condition: got %d, want 5", s.X)
	}

	if err := deep.Apply(&s, deep.NewPatch[S]().With(deep.Set(xPath, 10).Unless(deep.Le(xPath, 4))).Build()); err != nil {
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

	p := deep.NewPatch[S]().With(deep.Move(aPath, bPath)).Build()
	if len(p.Operations) != 1 || p.Operations[0].Kind != deep.OpMove {
		t.Error("Move not added correctly")
	}
	if p.Operations[0].From != aPath.String() || p.Operations[0].Path != bPath.String() {
		t.Errorf("Move paths wrong: from=%v to=%v", p.Operations[0].From, p.Operations[0].Path)
	}

	p2 := deep.NewPatch[S]().With(deep.Copy(aPath, bPath)).Build()
	if len(p2.Operations) != 1 || p2.Operations[0].Kind != deep.OpCopy {
		t.Error("Copy not added correctly")
	}
}

func TestLWWSet(t *testing.T) {
	clock := hlc.NewClock("test")
	ts1 := clock.Now()
	ts2 := clock.Now()

	var reg crdt.LWW[string]
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

// TestJSONPatchStrictRoundTrip asserts that a strict patch survives the trip
// through ToJSONPatch/ParseJSONPatch: the strict Old checks are encoded as
// RFC 6902 test operations and decoded back into Strict + Old.
func TestJSONPatchStrictRoundTrip(t *testing.T) {
	type Cfg struct {
		Theme string `json:"theme"`
		Size  int    `json:"size"`
	}
	p := deep.Patch[Cfg]{
		Operations: []deep.Operation{
			{Kind: deep.OpReplace, Path: "/theme", Old: "dark", New: "light"},
			{Kind: deep.OpRemove, Path: "/size", Old: 12},
		},
	}.AsStrict()

	data, err := p.ToJSONPatch()
	if err != nil {
		t.Fatalf("ToJSONPatch: %v", err)
	}

	got, err := deep.ParseJSONPatch[Cfg](data)
	if err != nil {
		t.Fatalf("ParseJSONPatch: %v", err)
	}
	if !got.Strict {
		t.Errorf("Strict flag lost in round-trip: %s", data)
	}
	if len(got.Operations) != 2 {
		t.Fatalf("got %d operations, want 2: %s", len(got.Operations), data)
	}
	// Parsed values stay encoded until apply time; ValueAs decodes them
	// against the type the caller knows them to be.
	if old, ok := deep.ValueAs[string](got.Operations[0].Old); !ok || old != "dark" {
		t.Errorf("Old[0] = %v, want dark", got.Operations[0].Old)
	}
	if old, ok := deep.ValueAs[int](got.Operations[1].Old); !ok || old != 12 {
		t.Errorf("Old[1] = %v, want 12", got.Operations[1].Old)
	}
}

// TestJSONPatchNonStrictHasNoTests asserts a non-strict patch does not emit
// test operations even when Old values are present.
func TestJSONPatchNonStrictHasNoTests(t *testing.T) {
	type Cfg struct {
		Theme string `json:"theme"`
	}
	p := deep.Patch[Cfg]{
		Operations: []deep.Operation{
			{Kind: deep.OpReplace, Path: "/theme", Old: "dark", New: "light"},
		},
	}
	data, err := p.ToJSONPatch()
	if err != nil {
		t.Fatalf("ToJSONPatch: %v", err)
	}
	if strings.Contains(string(data), `"test"`) {
		t.Errorf("non-strict patch emitted test ops: %s", data)
	}
}
