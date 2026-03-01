package deep_test

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"strings"
	"testing"

	"github.com/brunoga/deep/v5"
	"github.com/brunoga/deep/v5/internal/testmodels"
)

func TestGobSerialization(t *testing.T) {
	deep.Register[testmodels.User]()

	u1 := testmodels.User{ID: 1, Name: "Alice"}
	u2 := testmodels.User{ID: 2, Name: "Bob"}
	patch := deep.Diff(u1, u2)

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
	patch := deep.Diff(u1, u2)

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

	p := deep.NewPatch[testmodels.User]()
	p.Operations = []deep.Operation{
		{Kind: deep.OpReplace, Path: "/full_name", Old: "Alice", New: "Bob"},
	}
	p = p.WithCondition(deep.Eq(deep.Field(func(u *testmodels.User) *int { return &u.ID }), 1))

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
	p := deep.NewPatch[testmodels.User]()
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
	for _, op := range p2.Operations {
		if !op.Strict {
			t.Error("WithStrict failed to propagate to operations")
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
		got, err := deep.NewPatch[testmodels.User]().WithCondition(tt.c).ToJSONPatch()
		if err != nil {
			t.Fatalf("ToJSONPatch failed: %v", err)
		}
		if !strings.Contains(string(got), tt.want) {
			t.Errorf("toPredicate(%s) = %s, want %s", tt.c.Op, string(got), tt.want)
		}
	}
}

func TestPatchReverseExhaustive(t *testing.T) {
	p := deep.NewPatch[testmodels.User]()
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
	p1 := deep.NewPatch[testmodels.User]()
	p1.Operations = []deep.Operation{{Path: "/a", New: 1}}
	p2 := deep.NewPatch[testmodels.User]()
	p2.Operations = []deep.Operation{{Path: "/a", New: 2}}

	res := deep.Merge(p1, p2, &localResolver{})
	if res.Operations[0].New != 2 {
		t.Error("Merge custom resolution failed")
	}
}

type localResolver struct{}

func (r *localResolver) Resolve(path string, local, remote any) any { return remote }
