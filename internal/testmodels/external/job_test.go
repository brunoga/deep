package external_test

import (
	"testing"
	"time"

	deep "github.com/brunoga/deep/v6"
	"github.com/brunoga/deep/v6/crdt"
	"github.com/brunoga/deep/v6/internal/testmodels/external"
)

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return ts
}

// TestPointerToExternalTypeRoundTrip covers the field shape from issue #46:
// deep-gen used to emit an empty type assertion (op.Old.()) for *time.Time.
func TestPointerToExternalTypeRoundTrip(t *testing.T) {
	start := mustTime(t, "2024-01-01T00:00:00Z")
	later := mustTime(t, "2024-06-01T00:00:00Z")

	a := external.Job{}
	b := external.Job{StartAt: &start}

	p, err := deep.Diff(a, b)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(p.Operations) != 1 || p.Operations[0].Path != "/startAt" {
		t.Fatalf("unexpected patch: %+v", p.Operations)
	}

	got := external.Job{}
	if err := deep.Apply(&got, p); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got.StartAt == nil || !got.StartAt.Equal(start) {
		t.Fatalf("StartAt = %v, want %v", got.StartAt, start)
	}

	p2, err := deep.Diff(b, external.Job{StartAt: &later})
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if err := deep.Apply(&got, p2); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got.StartAt == nil || !got.StartAt.Equal(later) {
		t.Fatalf("StartAt = %v, want %v", got.StartAt, later)
	}
}

// TestStrictCheckComparesPointedAtValue asserts the strict Old check compares
// what the pointer points at rather than the pointer itself, which would only
// ever match the exact same allocation.
func TestStrictCheckComparesPointedAtValue(t *testing.T) {
	start := mustTime(t, "2024-01-01T00:00:00Z")
	sameValue := start
	later := mustTime(t, "2024-06-01T00:00:00Z")

	job := external.Job{StartAt: &start}
	p := deep.Patch[external.Job]{
		Operations: []deep.Operation{
			{Kind: deep.OpReplace, Path: "/startAt", Old: &sameValue, New: &later},
		},
	}.AsStrict()

	if err := deep.Apply(&job, p); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !job.StartAt.Equal(later) {
		t.Fatalf("StartAt = %v, want %v", job.StartAt, later)
	}

	stale := mustTime(t, "1999-01-01T00:00:00Z")
	bad := deep.Patch[external.Job]{
		Operations: []deep.Operation{
			{Kind: deep.OpReplace, Path: "/startAt", Old: &stale, New: &start},
		},
	}.AsStrict()
	if err := deep.Apply(&job, bad); err == nil {
		t.Fatal("Apply with a stale Old value: got nil error, want a strict check failure")
	}
}

func TestEqualAndCloneWithExternalTypes(t *testing.T) {
	start := mustTime(t, "2024-01-01T00:00:00Z")
	sameValue := start
	at := mustTime(t, "2024-02-01T00:00:00Z")

	job := external.Job{
		StartAt:  &start,
		Deadline: start.Add(time.Hour),
		Timeout:  30 * time.Second,
		Window:   []time.Time{start, at},
		Retries:  map[string]*time.Time{"first": &start},
		Stages:   []external.Stage{{Name: "build", At: at}},
		Owners:   map[string]*external.Stage{"qa": {Name: "qa", At: at}},
		Priority: 3,
		Grid:     [2]int{1, 2},
	}

	// Two jobs holding distinct pointers to equal values are equal.
	other := job
	other.StartAt = &sameValue
	if !deep.Equal(job, other) {
		t.Fatal("jobs with distinct pointers to equal times: got not equal, want equal")
	}

	clone := deep.Clone(job)
	if !deep.Equal(job, clone) {
		t.Fatal("clone is not equal to the original")
	}
	if clone.StartAt == job.StartAt {
		t.Fatal("clone shares the StartAt pointer with the original")
	}
	if clone.Retries["first"] == job.Retries["first"] {
		t.Fatal("clone shares a map value pointer with the original")
	}
	if clone.Owners["qa"] == job.Owners["qa"] {
		t.Fatal("clone shares an Owners pointer with the original")
	}

	*clone.StartAt = mustTime(t, "2030-01-01T00:00:00Z")
	clone.Stages[0].Name = "test"
	if !job.StartAt.Equal(start) {
		t.Fatalf("mutating the clone changed the original: StartAt = %v", job.StartAt)
	}
	if job.Stages[0].Name != "build" {
		t.Fatalf("mutating the clone changed the original: Stages[0].Name = %q", job.Stages[0].Name)
	}
}

// TestSubPathIntoGeneratedStruct checks that delegation into a struct declared
// in the same package still happens when its own fields come from elsewhere.
func TestSubPathIntoGeneratedStruct(t *testing.T) {
	at := mustTime(t, "2024-02-01T00:00:00Z")
	job := external.Job{Owners: map[string]*external.Stage{"qa": {Name: "qa", At: at}}}

	p := deep.Patch[external.Job]{
		Operations: []deep.Operation{
			{Kind: deep.OpReplace, Path: "/owners/qa/name", New: "release"},
		},
	}
	if err := deep.Apply(&job, p); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := job.Owners["qa"].Name; got != "release" {
		t.Fatalf("Owners[qa].Name = %q, want %q", got, "release")
	}
}

// TestCloneOpaqueWithReferences asserts that Clone deep-copies opaque types
// whose internals the generator cannot see: a crdt.LWW[[]int] carries a slice,
// and a cloned Job must not share it with the original.
func TestCloneOpaqueWithReferences(t *testing.T) {
	job := external.Job{History: crdt.LWW[[]int]{Value: []int{1, 2, 3}}}
	clone := deep.Clone(job)

	if !deep.Equal(job, clone) {
		t.Fatal("clone is not equal to the original")
	}
	clone.History.Value[0] = 99
	if job.History.Value[0] != 1 {
		t.Fatalf("mutating the clone changed the original: %v", job.History.Value)
	}
}
