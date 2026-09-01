package shapes_test

import (
	"testing"

	deep "github.com/brunoga/deep/v5"
	"github.com/brunoga/deep/v5/condition"
	"github.com/brunoga/deep/v5/internal/testmodels/multirun"
	"github.com/brunoga/deep/v5/internal/testmodels/shapes"
)

// Embedded fields must flow through Diff, Apply, Equal and Clone; they used to
// be silently dropped (Clone zeroed them, Equal ignored them).
func TestEmbeddedField(t *testing.T) {
	a := shapes.Doc{Meta: shapes.Meta{Version: 1, Author: "ann"}, Name: "d"}
	b := shapes.Doc{Meta: shapes.Meta{Version: 2, Author: "ann"}, Name: "d"}

	if deep.Equal(a, b) {
		t.Error("Equal ignored the embedded field")
	}

	c := deep.Clone(a)
	if c.Version != 1 || c.Author != "ann" {
		t.Errorf("Clone dropped the embedded field: %+v", c.Meta)
	}

	p, err := deep.Diff(a, b)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(p.Operations) == 0 {
		t.Fatal("Diff ignored the embedded field")
	}
	got := deep.Clone(a)
	if err := deep.Apply(&got, p); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !deep.Equal(got, b) {
		t.Errorf("round-trip: got %+v, want %+v", got, b)
	}
}

// A keyed-slice element whose key survives but whose content changed must
// produce a patch; it used to diff to nothing.
func TestKeyedSliceModifiedElement(t *testing.T) {
	a := shapes.Doc{Entries: []shapes.Entry{{ID: "e1", N: 5}, {ID: "e2", N: 1}}}
	b := shapes.Doc{Entries: []shapes.Entry{{ID: "e1", N: 7}, {ID: "e2", N: 1}}}

	p, err := deep.Diff(a, b)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(p.Operations) != 1 || p.Operations[0].Path != "/entries/e1/n" {
		t.Fatalf("unexpected patch: %+v", p.Operations)
	}
	got := deep.Clone(a)
	if err := deep.Apply(&got, p); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !deep.Equal(got, b) {
		t.Errorf("round-trip: got %+v", got.Entries)
	}
}

// Removing a key inside a map-valued entry must not delete the whole entry.
func TestNestedMapRemoveDeeperPath(t *testing.T) {
	d := shapes.Doc{Nested: map[string]map[string]int{"a": {"x": 1, "y": 2}}}
	p := deep.Patch[shapes.Doc]{Operations: []deep.Operation{
		{Kind: deep.OpRemove, Path: "/nested/a/x"},
	}}
	if err := deep.Apply(&d, p); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	inner, ok := d.Nested["a"]
	if !ok {
		t.Fatal("outer entry was deleted")
	}
	if _, ok := inner["x"]; ok {
		t.Error("inner key was not removed")
	}
	if _, ok := inner["y"]; !ok {
		t.Error("sibling key was removed")
	}
}

// Removing or replacing an entry in a pointer-valued map must work at the
// entry level; OpRemove used to error with "unsupported root operation".
func TestPtrMapEntryOps(t *testing.T) {
	d := shapes.Doc{Stages: map[string]*shapes.Meta{"qa": {Version: 1}}}

	replace := deep.Patch[shapes.Doc]{Operations: []deep.Operation{
		{Kind: deep.OpReplace, Path: "/stages/qa", New: &shapes.Meta{Version: 2}},
	}}
	if err := deep.Apply(&d, replace); err != nil {
		t.Fatalf("replace: %v", err)
	}
	if got := d.Stages["qa"].Version; got != 2 {
		t.Fatalf("replace: Version = %d, want 2", got)
	}

	remove := deep.Patch[shapes.Doc]{Operations: []deep.Operation{
		{Kind: deep.OpRemove, Path: "/stages/qa"},
	}}
	if err := deep.Apply(&d, remove); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, ok := d.Stages["qa"]; ok {
		t.Error("entry was not removed")
	}
}

// Map keys containing '/' or '~' must round-trip through diff and apply.
func TestMapKeyEscaping(t *testing.T) {
	a := shapes.Doc{Nested: map[string]map[string]int{}}
	b := shapes.Doc{Nested: map[string]map[string]int{"a/b": {"x": 1}, "t~e": {"y": 2}}}

	p, err := deep.Diff(a, b)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	for _, op := range p.Operations {
		if op.Path == "/nested/a/b" {
			t.Errorf("unescaped path emitted: %q", op.Path)
		}
	}
	got := deep.Clone(a)
	if err := deep.Apply(&got, p); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !deep.Equal(got, b) {
		t.Errorf("round-trip: got %+v, want %+v", got.Nested, b.Nested)
	}
}

// A malformed condition value must surface as an error, not a panic.
func TestConditionTypeValueMismatch(t *testing.T) {
	d := shapes.Doc{Name: "x"}
	p := deep.Patch[shapes.Doc]{
		Guard: &condition.Condition{Path: "/name", Op: "type", Value: 42},
		Operations: []deep.Operation{
			{Kind: deep.OpReplace, Path: "/name", New: "y"},
		},
	}
	err := deep.Apply(&d, p)
	if err == nil {
		t.Error("expected an error for a non-string type value")
	}
	if d.Name != "x" {
		t.Error("guard did not block the operation")
	}
}

// Conditions on nested paths are outside the generated fast path and must fall
// back to the reflection evaluator instead of erroring.
func TestNestedPathCondition(t *testing.T) {
	d := shapes.Doc{Meta: shapes.Meta{Version: 3}, Name: "x"}
	p := deep.Patch[shapes.Doc]{
		Guard: &condition.Condition{Path: "/Meta/version", Op: ">", Value: 2},
		Operations: []deep.Operation{
			{Kind: deep.OpReplace, Path: "/name", New: "y"},
		},
	}
	if err := deep.Apply(&d, p); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if d.Name != "y" {
		t.Error("nested-path guard was not evaluated")
	}
}

// An op-level condition that cannot be evaluated must surface an error rather
// than silently skipping the operation — matching how the patch-level Guard
// already behaves.
func TestOpConditionErrorSurfaces(t *testing.T) {
	d := shapes.Doc{Name: "x"}
	p := deep.Patch[shapes.Doc]{Operations: []deep.Operation{
		{
			Kind: deep.OpReplace, Path: "/name", New: "y",
			If: &condition.Condition{Path: "/name", Op: "matches", Value: 42},
		},
	}}
	err := deep.Apply(&d, p)
	if err == nil {
		t.Fatal("expected an error for an unevaluable op condition")
	}
	if d.Name != "x" {
		t.Error("operation was applied despite the condition error")
	}
}

// A pointer-to-struct field appearing or disappearing must show up in Diff;
// both transitions used to produce an empty patch.
func TestPtrStructNilTransitions(t *testing.T) {
	set := shapes.Doc{Side: &shapes.Meta{Version: 3}}
	unset := shapes.Doc{}

	for name, tc := range map[string]struct{ from, to shapes.Doc }{
		"nil-to-set": {unset, set},
		"set-to-nil": {set, unset},
	} {
		p, err := deep.Diff(tc.from, tc.to)
		if err != nil {
			t.Fatalf("%s: Diff: %v", name, err)
		}
		if len(p.Operations) == 0 {
			t.Fatalf("%s: empty patch", name)
		}
		got := deep.Clone(tc.from)
		if err := deep.Apply(&got, p); err != nil {
			t.Fatalf("%s: Apply: %v", name, err)
		}
		if !deep.Equal(got, tc.to) {
			t.Errorf("%s: round-trip failed: %+v", name, got.Side)
		}
	}
}

// An atomic non-comparable field diffs as one whole-value replace. Before the
// fix this shape did not even compile (it was diffed with ==).
func TestAtomicNonComparable(t *testing.T) {
	a := shapes.Doc{Blob: shapes.Payload{Bytes: []byte{1}, Label: "a"}}
	b := shapes.Doc{Blob: shapes.Payload{Bytes: []byte{1, 2}, Label: "b"}}
	p, err := deep.Diff(a, b)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(p.Operations) != 1 || p.Operations[0].Path != "/blob" {
		t.Fatalf("want a single whole-value replace at /blob, got %+v", p.Operations)
	}
	got := deep.Clone(a)
	if err := deep.Apply(&got, p); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !deep.Equal(got, b) {
		t.Errorf("round-trip: %+v", got.Blob)
	}
}

// A strict map-entry replace with a wrong Old value must be rejected, exactly
// as the reflection engine rejects it; the fast path used to apply it blindly.
func TestStrictMapEntry(t *testing.T) {
	d := shapes.Doc{Scores: map[string]int{"k": 5}}
	bad := deep.Patch[shapes.Doc]{Operations: []deep.Operation{
		{Kind: deep.OpReplace, Path: "/scores/k", Old: 999, New: 7},
	}}.AsStrict()
	if err := deep.Apply(&d, bad); err == nil {
		t.Error("strict replace with wrong Old was applied")
	}
	if d.Scores["k"] != 5 {
		t.Errorf("value mutated to %d despite failed strict check", d.Scores["k"])
	}

	good := deep.Patch[shapes.Doc]{Operations: []deep.Operation{
		{Kind: deep.OpReplace, Path: "/scores/k", Old: 5, New: 7},
	}}.AsStrict()
	if err := deep.Apply(&d, good); err != nil {
		t.Fatalf("strict replace with correct Old failed: %v", err)
	}
	if d.Scores["k"] != 7 {
		t.Errorf("Scores[k] = %d, want 7", d.Scores["k"])
	}
}

// Two deep-gen runs over one package must produce files that compile together
// and behave; the shared helper both used to emit collided.
func TestMultiRunPackage(t *testing.T) {
	a := multirun.Alpha{M: map[string]int{"x": 1}}
	b := multirun.Alpha{M: map[string]int{"y": 2}}
	p, err := deep.Diff(a, b)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	got := deep.Clone(a)
	if err := deep.Apply(&got, p); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !deep.Equal(got, b) {
		t.Errorf("round-trip: %v", got.M)
	}
}
