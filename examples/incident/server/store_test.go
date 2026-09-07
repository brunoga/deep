package server

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	deep "github.com/brunoga/deep/v6"

	"github.com/brunoga/deep/examples/incident/model"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Create(model.Incident{
		ID:       "inc-1",
		Title:    "checkout errors",
		Severity: model.Sev2,
		Status:   model.StatusOpen,
		Tasks: []model.Task{
			{ID: "t1", Text: "page db oncall"},
			{ID: "t2", Text: "check error rates"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	return s
}

// A patch travels as JSON in real life; build it the way a client would and
// push it through the wire form.
func wirePatch(t *testing.T, p deep.Patch[model.Incident]) deep.Patch[model.Incident] {
	t.Helper()
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var q deep.Patch[model.Incident]
	if err := json.Unmarshal(data, &q); err != nil {
		t.Fatal(err)
	}
	return q
}

func ownerPath(id string) deep.Path[model.Incident, string] {
	return deep.PathString[model.Incident, string]("/tasks/" + id + "/owner")
}

func claim(task, who string) deep.Patch[model.Incident] {
	return deep.NewPatch[model.Incident]().With(
		deep.Set(ownerPath(task), who).If(deep.Eq(ownerPath(task), "")),
	).Build()
}

// Two responders race to claim the same task; the If condition lets exactly
// one through, and the loser's patch is a skip, not an error and not an
// overwrite.
func TestClaimRace(t *testing.T) {
	s := newTestStore(t)

	res, err := s.ApplyPatch("inc-1", "ana", wirePatch(t, claim("t1", "ana")))
	if err != nil || res.Applied != 1 {
		t.Fatalf("first claim: %+v, %v", res, err)
	}
	res, err = s.ApplyPatch("inc-1", "bruno", wirePatch(t, claim("t1", "bruno")))
	if err != nil {
		t.Fatalf("second claim errored: %v", err)
	}
	if res.Applied != 0 || res.Skipped != 1 || res.Seq != 0 {
		t.Fatalf("second claim should skip without a log entry: %+v", res)
	}

	inc, _ := s.Get("inc-1")
	if inc.Tasks[0].Owner != "ana" {
		t.Fatalf("owner = %q, want ana", inc.Tasks[0].Owner)
	}
}

// The audit log records canonical changes; undo reverses one and appends —
// never rewrites — and a second undo of the same entry finds the strict Old
// stale and refuses.
func TestUndoIsAppendOnlyAndStrict(t *testing.T) {
	s := newTestStore(t)

	res, err := s.ApplyPatch("inc-1", "ana", wirePatch(t, claim("t1", "ana")))
	if err != nil {
		t.Fatal(err)
	}

	undoRes, err := s.Undo("inc-1", "bruno", res.Seq)
	if err != nil {
		t.Fatalf("undo: %v", err)
	}
	inc, _ := s.Get("inc-1")
	if inc.Tasks[0].Owner != "" {
		t.Fatalf("undo did not clear owner: %q", inc.Tasks[0].Owner)
	}
	history, _ := s.History("inc-1")
	if len(history) != 2 || history[1].Seq != undoRes.Seq || history[1].Note == "" {
		t.Fatalf("undo should append a noted entry: %+v", history)
	}

	// The state has moved on (owner is "" again); undoing the original claim
	// a second time must fail its strict check, not reapply.
	if _, err := s.Undo("inc-1", "bruno", res.Seq); !errors.Is(err, ErrConflict) {
		t.Fatalf("second undo: want ErrConflict, got %v", err)
	}
}

// Server-side validation judges the resulting incident: closing with open
// tasks is refused even though the patch itself applies cleanly.
func TestCannotCloseWithOpenTasks(t *testing.T) {
	s := newTestStore(t)
	statusPath := deep.PathString[model.Incident, model.Status]("/status")

	closeIt := deep.NewPatch[model.Incident]().With(
		deep.Set(statusPath, model.StatusClosed),
	).Build()
	if _, err := s.ApplyPatch("inc-1", "ana", wirePatch(t, closeIt)); !errors.Is(err, ErrValidation) {
		t.Fatalf("want ErrValidation, got %v", err)
	}
	inc, _ := s.Get("inc-1")
	if inc.Status != model.StatusOpen {
		t.Fatalf("failed patch mutated the incident: %v", inc.Status)
	}
}

// A guard rejection is a conflict and touches nothing.
func TestGuardRejection(t *testing.T) {
	s := newTestStore(t)
	statusPath := deep.PathString[model.Incident, model.Status]("/status")

	p := deep.NewPatch[model.Incident]().
		Guard(deep.Eq(statusPath, model.StatusResolved)).
		With(deep.Set(statusPath, model.StatusClosed)).
		Build()
	if _, err := s.ApplyPatch("inc-1", "ana", wirePatch(t, p)); !errors.Is(err, ErrConflict) {
		t.Fatalf("want ErrConflict, got %v", err)
	}
}

// /id and /updated are not editable, whatever the patch says.
func TestImmutablePaths(t *testing.T) {
	s := newTestStore(t)
	p := deep.NewPatch[model.Incident]().With(
		deep.Set(deep.PathString[model.Incident, string]("/id"), "inc-666"),
	).Build()
	if _, err := s.ApplyPatch("inc-1", "ana", wirePatch(t, p)); !errors.Is(err, ErrConflict) {
		t.Fatalf("want ErrConflict for /id, got %v", err)
	}
}

// Everything survives a restart: state, log, and the ability to keep undoing.
func TestPersistenceAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Create(model.Incident{ID: "inc-1", Title: "x", Severity: model.Sev3, Status: model.StatusOpen,
		Tasks: []model.Task{{ID: "t1", Text: "do"}}}); err != nil {
		t.Fatal(err)
	}
	res, err := s.ApplyPatch("inc-1", "ana", wirePatch(t, claim("t1", "ana")))
	if err != nil {
		t.Fatal(err)
	}

	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	inc, err := s2.Get("inc-1")
	if err != nil || inc.Tasks[0].Owner != "ana" {
		t.Fatalf("reloaded state wrong: %+v, %v", inc, err)
	}
	history, _ := s2.History("inc-1")
	if len(history) != 1 {
		t.Fatalf("reloaded log wrong: %+v", history)
	}
	// The reloaded entry's values are RawValue now; Reverse and strict apply
	// must still work through them.
	if _, err := s2.Undo("inc-1", "bruno", res.Seq); err != nil {
		t.Fatalf("undo after reload: %v", err)
	}
	inc, _ = s2.Get("inc-1")
	if inc.Tasks[0].Owner != "" {
		t.Fatalf("undo after reload did not apply: %+v", inc)
	}
}

// Compaction collapses the log head into one baseline whose replay-equivalent
// still describes the same end state; the tail keeps individual undo.
func TestCompaction(t *testing.T) {
	s := newTestStore(t)
	titlePath := deep.PathString[model.Incident, string]("/title")

	for _, title := range []string{"one", "two", "three", "four"} {
		p := deep.NewPatch[model.Incident]().With(deep.Set(titlePath, title)).Build()
		if _, err := s.ApplyPatch("inc-1", "ana", wirePatch(t, p)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Compact("inc-1", 1); err != nil {
		t.Fatal(err)
	}
	history, _ := s.History("inc-1")
	if len(history) != 2 {
		t.Fatalf("want baseline + 1 kept entry, got %+v", history)
	}
	if history[0].Author != "compaction" {
		t.Fatalf("baseline not marked: %+v", history[0])
	}
	// The merged baseline's title op must have later-wins semantics: New is
	// "three" (the last collapsed write).
	var op deep.Operation
	for _, o := range history[0].Patch.Operations {
		if o.Path == "/title" {
			op = o
		}
	}
	if got, ok := deep.ValueAs[string](op.New); !ok || got != "three" {
		t.Fatalf("baseline title op wrong: %+v", op)
	}

	// Undo of the last (uncompacted) entry still works.
	if _, err := s.Undo("inc-1", "ana", history[1].Seq); err != nil {
		t.Fatal(err)
	}
	inc, _ := s.Get("inc-1")
	if inc.Title != "three" {
		t.Fatalf("undo after compaction: title = %q", inc.Title)
	}
}

// Updated is stamped by the server on every applied change.
func TestServerStampsUpdated(t *testing.T) {
	s := newTestStore(t)
	fixed := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return fixed }

	if _, err := s.ApplyPatch("inc-1", "ana", wirePatch(t, claim("t1", "ana"))); err != nil {
		t.Fatal(err)
	}
	inc, _ := s.Get("inc-1")
	if !inc.Updated.Equal(fixed) {
		t.Fatalf("updated = %v, want %v", inc.Updated, fixed)
	}
}

// Incident IDs become directory names and room names; anything resembling a
// path stays out.
func TestTraversalIDsRejected(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"../../../tmp/evil", "a/b", "..", ".hidden", "", "x y"} {
		err := s.Create(model.Incident{ID: id, Title: "x", Severity: model.Sev3, Status: model.StatusOpen})
		if !errors.Is(err, ErrValidation) {
			t.Errorf("id %q: want ErrValidation, got %v", id, err)
		}
	}
}
