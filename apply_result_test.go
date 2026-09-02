package deep_test

import (
	"encoding/json"
	"errors"
	"testing"

	deep "github.com/brunoga/deep/v5"
	"github.com/brunoga/deep/v5/internal/testmodels"
)

// reflectDoc has no generated code, so it exercises the reflection path. Its
// fields mirror the generated model used alongside it, so the two paths can be
// held to the same behaviour.
type reflectDoc struct {
	Status  string         `json:"status"`
	Version int            `json:"version"`
	Stock   int            `json:"stock"`
	Labels  map[string]int `json:"labels"`
}

func TestApplyWithResultReportsSkippedOperations(t *testing.T) {
	// The gap this exists to close: Apply returns nil whether a conditional
	// operation ran or was skipped, so a caller cannot tell whether the
	// condition held.
	doc := reflectDoc{Status: "draft", Version: 1}

	p := deep.Patch[reflectDoc]{Operations: []deep.Operation{
		{Kind: deep.OpReplace, Path: "/status", New: "published",
			If: deep.Eq(deep.Field(func(d *reflectDoc) *int { return &d.Version }), 1)},
		{Kind: deep.OpReplace, Path: "/stock", New: 5,
			If: deep.Eq(deep.Field(func(d *reflectDoc) *int { return &d.Version }), 99)},
	}}

	res, err := deep.ApplyWithResult(&doc, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	applied, skipped, failed := res.Counts()
	if applied != 1 || skipped != 1 || failed != 0 {
		t.Fatalf("got %d applied, %d skipped, %d failed; want 1/1/0\n%s",
			applied, skipped, failed, res)
	}
	if res.AllApplied() {
		t.Error("AllApplied should be false when an operation was skipped")
	}
	if got := res.WithStatus(deep.StatusSkipped); len(got) != 1 || got[0].Path != "/stock" {
		t.Errorf("expected /stock to be the skipped operation, got %+v", got)
	}

	// The one whose condition held must actually have run.
	if doc.Status != "published" {
		t.Errorf("Status = %q, want published", doc.Status)
	}
	if doc.Stock != 0 {
		t.Errorf("Stock = %d, want 0 — the skipped operation ran anyway", doc.Stock)
	}
}

func TestApplyWithResultKeepsSequentialConditionSemantics(t *testing.T) {
	// Each condition must see the state the operations before it left, exactly
	// as Apply does. Evaluating them all up front would be cheaper and wrong.
	doc := reflectDoc{Version: 1}

	p := deep.Patch[reflectDoc]{Operations: []deep.Operation{
		{Kind: deep.OpReplace, Path: "/version", New: 2},
		{Kind: deep.OpReplace, Path: "/status", New: "second saw it",
			If: deep.Eq(deep.Field(func(d *reflectDoc) *int { return &d.Version }), 2)},
	}}

	res, err := deep.ApplyWithResult(&doc, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.AllApplied() {
		t.Fatalf("both operations should have applied:\n%s", res)
	}
	if doc.Status != "second saw it" {
		t.Error("the second condition did not see the first operation's effect")
	}
}

func TestApplyWithResultReportsFailures(t *testing.T) {
	doc := reflectDoc{Status: "draft"}
	p := deep.Patch[reflectDoc]{
		Strict: true,
		Operations: []deep.Operation{
			{Kind: deep.OpReplace, Path: "/status", Old: "wrong", New: "published"},
			{Kind: deep.OpReplace, Path: "/stock", New: 3},
		},
	}

	res, err := deep.ApplyWithResult(&doc, p)
	if err == nil {
		t.Fatal("expected an error for the failed strict check")
	}
	applied, skipped, failed := res.Counts()
	if applied != 1 || skipped != 0 || failed != 1 {
		t.Fatalf("got %d/%d/%d, want 1 applied / 0 skipped / 1 failed\n%s",
			applied, skipped, failed, res)
	}
	// A failure does not stop the operations after it, as with Apply.
	if doc.Stock != 3 {
		t.Error("the operation after the failure did not run")
	}
}

func TestApplyWithResultGuardIsASentinel(t *testing.T) {
	doc := reflectDoc{Version: 1}
	p := deep.Patch[reflectDoc]{
		Guard:      deep.Eq(deep.Field(func(d *reflectDoc) *int { return &d.Version }), 99),
		Operations: []deep.Operation{{Kind: deep.OpReplace, Path: "/status", New: "x"}},
	}

	res, err := deep.ApplyWithResult(&doc, p)
	if !errors.Is(err, deep.ErrGuardNotMet) {
		t.Fatalf("got %v, want ErrGuardNotMet", err)
	}
	if len(res.Outcomes) != 0 {
		t.Error("no operation should be reported when the guard rejected the patch")
	}
	if doc.Status != "" {
		t.Error("target was modified despite the guard")
	}

	// Apply reports the same sentinel, so callers can branch on it either way.
	if err := deep.Apply(&doc, p); !errors.Is(err, deep.ErrGuardNotMet) {
		t.Errorf("Apply: got %v, want ErrGuardNotMet", err)
	}
}

func TestWithAllowedPathsRejectsTheWholePatch(t *testing.T) {
	doc := reflectDoc{Status: "draft", Stock: 1}
	p := deep.Patch[reflectDoc]{Operations: []deep.Operation{
		{Kind: deep.OpReplace, Path: "/status", New: "published"},
		{Kind: deep.OpReplace, Path: "/stock", New: 99},
	}}

	err := deep.Apply(&doc, p, deep.WithAllowedPaths("/status"))
	if !errors.Is(err, deep.ErrPathNotAllowed) {
		t.Fatalf("got %v, want ErrPathNotAllowed", err)
	}
	// Nothing applied: an out-of-bounds operation voids the patch rather than
	// letting the permitted part through.
	if doc.Status != "draft" || doc.Stock != 1 {
		t.Errorf("target changed despite rejection: %+v", doc)
	}

	// The permitted subset on its own goes through.
	ok := deep.Patch[reflectDoc]{Operations: p.Operations[:1]}
	if err := deep.Apply(&doc, ok, deep.WithAllowedPaths("/status")); err != nil {
		t.Fatalf("allowed operation rejected: %v", err)
	}
	if doc.Status != "published" {
		t.Error("allowed operation did not apply")
	}
}

func TestWithAllowedPathsChecksMoveAndCopySources(t *testing.T) {
	// A move or copy reads from From, so restricting only Path would let a
	// patch pull data out of a field it was never given access to.
	doc := reflectDoc{Status: "secret", Stock: 1}
	p := deep.Patch[reflectDoc]{Operations: []deep.Operation{
		{Kind: deep.OpCopy, Path: "/labels/leak", From: "/status"},
	}}

	err := deep.Apply(&doc, p, deep.WithAllowedPaths("/labels"))
	if !errors.Is(err, deep.ErrPathNotAllowed) {
		t.Fatalf("got %v, want ErrPathNotAllowed for the From path", err)
	}
}

func TestWithAllowedPathsPrefixIsPathSegmented(t *testing.T) {
	doc := reflectDoc{Labels: map[string]int{}}
	p := deep.Patch[reflectDoc]{Operations: []deep.Operation{
		{Kind: deep.OpReplace, Path: "/labels/a", New: 1},
	}}
	if err := deep.Apply(&doc, p, deep.WithAllowedPaths("/labels")); err != nil {
		t.Errorf("/labels should allow /labels/a: %v", err)
	}
	// "/lab" must not allow "/labels" — matching has to respect segments.
	if err := deep.Apply(&doc, p, deep.WithAllowedPaths("/lab")); !errors.Is(err, deep.ErrPathNotAllowed) {
		t.Errorf("/lab should not allow /labels/a, got %v", err)
	}
}

// ── strict checks against a patch that travelled as JSON ─────────────────────

func TestStrictCheckSurvivesJSONRoundTripReflection(t *testing.T) {
	before := reflectDoc{Status: "draft", Version: 3, Stock: 7}
	after := before
	after.Stock = 8

	p, err := deep.Diff(before, after)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	p.Strict = true

	// Numbers come back as float64 whatever the field's type; a strict check
	// demanding identical types would reject state it actually matches.
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded deep.Patch[reflectDoc]
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	decoded.Strict = true

	target := before
	if err := deep.Apply(&target, decoded); err != nil {
		t.Fatalf("strict apply of a decoded patch failed: %v", err)
	}
	if target.Stock != 8 {
		t.Errorf("Stock = %d, want 8", target.Stock)
	}
}

func TestStrictCheckSurvivesJSONRoundTripGenerated(t *testing.T) {
	before := testmodels.User{ID: 3, Name: "ann", Info: testmodels.Detail{Age: 30}}
	after := before
	after.Info.Age = 31

	p, err := deep.Diff(before, after)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded deep.Patch[testmodels.User]
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	decoded.Strict = true

	target := before
	if err := deep.Apply(&target, decoded); err != nil {
		t.Fatalf("strict apply of a decoded patch failed on the generated path: %v", err)
	}
	if target.Info.Age != 31 {
		t.Errorf("Age = %d, want 31", target.Info.Age)
	}
}

func TestStrictCheckStillCatchesARealMismatch(t *testing.T) {
	// Coercion must not turn the check into a formality.
	doc := reflectDoc{Stock: 7}
	p := deep.Patch[reflectDoc]{
		Strict: true,
		Operations: []deep.Operation{
			{Kind: deep.OpReplace, Path: "/stock", Old: float64(6), New: float64(8)},
		},
	}
	if err := deep.Apply(&doc, p); err == nil {
		t.Fatal("strict check passed against a value that did not match")
	}
	if doc.Stock != 7 {
		t.Errorf("Stock = %d, want 7 — the operation applied despite the mismatch", doc.Stock)
	}
}

func TestStrictCheckOnMissingPathIsAMismatch(t *testing.T) {
	// The check used to be skipped when the path did not resolve, leaving a
	// strict operation unchecked exactly when the target had drifted furthest.
	doc := reflectDoc{Labels: map[string]int{}}
	p := deep.Patch[reflectDoc]{
		Strict: true,
		Operations: []deep.Operation{
			{Kind: deep.OpReplace, Path: "/labels/missing", Old: 4, New: 5},
		},
	}
	if err := deep.Apply(&doc, p); err == nil {
		t.Fatal("strict check passed against a path holding nothing")
	}
}

func TestEqualCoercedRejectsLossyConversions(t *testing.T) {
	cases := []struct {
		name              string
		current, expected any
		want              bool
	}{
		{"same type", 5, 5, true},
		{"json widened int", 5, float64(5), true},
		{"fractional does not match an int", 5, float64(5.7), false},
		{"different value", 5, float64(6), false},
		{"string", "a", "a", true},
		{"string mismatch", "a", "b", false},
		{"number is not a string", "5", float64(5), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := deep.EqualCoerced(c.current, c.expected); got != c.want {
				t.Errorf("EqualCoerced(%#v, %#v) = %v, want %v", c.current, c.expected, got, c.want)
			}
		})
	}
}

func TestApplyWithResultUsesTheGeneratedPath(t *testing.T) {
	// The same reporting has to hold for a type with generated code, which
	// applies through its own fast path rather than the reflection engine.
	u := testmodels.User{ID: 1, Name: "ann"}
	p := deep.Patch[testmodels.User]{Operations: []deep.Operation{
		{Kind: deep.OpReplace, Path: "/full_name", New: "bea",
			If: deep.Eq(deep.Field(func(u *testmodels.User) *int { return &u.ID }), 1)},
		{Kind: deep.OpReplace, Path: "/full_name", New: "cass",
			If: deep.Eq(deep.Field(func(u *testmodels.User) *int { return &u.ID }), 2)},
	}}

	res, err := deep.ApplyWithResult(&u, p)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	applied, skipped, _ := res.Counts()
	if applied != 1 || skipped != 1 {
		t.Fatalf("got %d applied, %d skipped; want 1/1\n%s", applied, skipped, res)
	}
	if u.Name != "bea" {
		t.Errorf("Name = %q, want bea", u.Name)
	}
}

func TestStrictWithNoRecordedOldIsNotACheck(t *testing.T) {
	// Patch.Strict stamps every operation, but a hand-built operation may not
	// have recorded what it expected to find. That is no expectation, not a
	// failed one.
	doc := reflectDoc{Stock: 7}
	p := deep.Patch[reflectDoc]{
		Strict:     true,
		Operations: []deep.Operation{{Kind: deep.OpReplace, Path: "/stock", New: 9}},
	}
	if err := deep.Apply(&doc, p); err != nil {
		t.Fatalf("strict operation without an Old should apply: %v", err)
	}
	if doc.Stock != 9 {
		t.Errorf("Stock = %d, want 9", doc.Stock)
	}
}
