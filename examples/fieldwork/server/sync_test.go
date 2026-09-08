package server

import (
	"encoding/json"
	"strings"
	"testing"

	deep "github.com/brunoga/deep/v6"

	"github.com/brunoga/deep/examples/fieldwork/model"
)

func seeded(t *testing.T) *Store {
	t.Helper()
	s := NewStore()
	if err := s.Create(model.Asset{
		ID: "pump-7", Name: "Intake pump 7", Site: "riverside", Status: model.StatusOK,
		Assignee: "ana",
		Readings: map[string]model.Reading{"ph": {Value: 7.1, Unit: "pH"}},
		Checks:   []model.Check{{ID: "seals", Label: "inspect seals"}},
	}); err != nil {
		t.Fatal(err)
	}
	return s
}

// wire pushes a patch through JSON, the way it reaches the server.
func wire(t *testing.T, p deep.Patch[model.Asset]) deep.Patch[model.Asset] {
	t.Helper()
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var q deep.Patch[model.Asset]
	if err := json.Unmarshal(data, &q); err != nil {
		t.Fatal(err)
	}
	return q
}

// diffFrom edits a copy and returns the resulting patch — what a client's
// shadow/working pair produces.
func diffFrom(t *testing.T, base model.Asset, edit func(*model.Asset)) deep.Patch[model.Asset] {
	t.Helper()
	work := deep.Clone(base)
	edit(&work)
	p, err := deep.Diff(base, work)
	if err != nil {
		t.Fatal(err)
	}
	return wire(t, p)
}

func TestSyncFastPath(t *testing.T) {
	s := seeded(t)
	base, version, _ := s.Get("pump-7")

	push := Push{ID: "pump-7", BaseVersion: version, Patch: diffFrom(t, base, func(a *model.Asset) {
		a.Readings["flow"] = model.Reading{Value: 120, Unit: "l/min", By: "ana"}
		a.Checks[0].Done = true
	})}
	res := s.Sync("ana", []Push{push})[0]
	if res.Outcome != "applied" || len(res.Conflicts) != 0 || res.Error != "" {
		t.Fatalf("fast path: %+v", res)
	}
	if res.Version != version+1 || res.Asset.Readings["flow"].Value != 120 || !res.Asset.Checks[0].Done {
		t.Fatalf("fast path result: %+v", res)
	}
}

// Disjoint concurrent edits merge with no conflicts at all: the office
// reassigns, the field measures, everything survives.
func TestSyncMergeDisjoint(t *testing.T) {
	s := seeded(t)
	base, version, _ := s.Get("pump-7")

	// Office moves first (online, direct).
	if _, err := s.Change("pump-7", "dispatch", diffFrom(t, base, func(a *model.Asset) {
		a.Assignee = "bruno"
	})); err != nil {
		t.Fatal(err)
	}

	// The technician pushes edits made against the old version.
	push := Push{ID: "pump-7", BaseVersion: version, Patch: diffFrom(t, base, func(a *model.Asset) {
		r := a.Readings["ph"]
		r.Value, r.By = 6.4, "ana"
		a.Readings["ph"] = r
	})}
	res := s.Sync("ana", []Push{push})[0]
	if res.Outcome != "merged" || len(res.Conflicts) != 0 || res.Error != "" {
		t.Fatalf("disjoint merge: %+v", res)
	}
	if res.Asset.Assignee != "bruno" || res.Asset.Readings["ph"].Value != 6.4 {
		t.Fatalf("merge lost a side: %+v", res.Asset)
	}
}

// Head-on collisions follow the policy, and every decision is reported.
func TestSyncPolicyConflicts(t *testing.T) {
	s := seeded(t)
	base, version, _ := s.Get("pump-7")

	// Office: status attention, same reading changed, a note, reassignment.
	if _, err := s.Change("pump-7", "dispatch", diffFrom(t, base, func(a *model.Asset) {
		a.Status = model.StatusAttention
		r := a.Readings["ph"]
		r.Value, r.By = 7.5, "scada"
		a.Readings["ph"] = r
		a.Notes = "scada flagged drift"
		a.Assignee = "bruno"
	})); err != nil {
		t.Fatal(err)
	}

	// Field, offline against the old base: status fault, same reading, own
	// note, keeps ana assigned (no change — not a conflict).
	push := Push{ID: "pump-7", BaseVersion: version, Patch: diffFrom(t, base, func(a *model.Asset) {
		a.Status = model.StatusFault
		r := a.Readings["ph"]
		r.Value, r.By = 6.4, "ana"
		a.Readings["ph"] = r
		a.Notes = "seals leaking"
	})}
	res := s.Sync("ana", []Push{push})[0]
	if res.Outcome != "merged" || res.Error != "" {
		t.Fatalf("policy merge: %+v", res)
	}

	kept := map[string]string{}
	for _, c := range res.Conflicts {
		kept[c.Path] = c.Kept
	}
	// The field's measurement wins; the worse status wins (fault > attention,
	// so "mine"); notes combine; nothing else collided.
	if kept["/readings/ph/value"] != "mine" || kept["/readings/ph/by"] != "mine" {
		t.Fatalf("readings policy: %+v", res.Conflicts)
	}
	if kept["/status"] != "mine" {
		t.Fatalf("status policy: %+v", res.Conflicts)
	}
	if kept["/notes"] != "combined" {
		t.Fatalf("notes policy: %+v", res.Conflicts)
	}

	a := res.Asset
	if a.Status != model.StatusFault || a.Readings["ph"].Value != 6.4 || a.Readings["ph"].By != "ana" {
		t.Fatalf("policy outcome: %+v", a)
	}
	if !strings.Contains(a.Notes, "scada flagged drift") || !strings.Contains(a.Notes, "seals leaking") {
		t.Fatalf("notes lost a side: %q", a.Notes)
	}
	if a.Assignee != "bruno" {
		t.Fatalf("office reassignment lost: %+v", a)
	}
}

// The worse status wins in the other direction too: the office saw worse.
func TestSyncStatusTheirsWins(t *testing.T) {
	s := seeded(t)
	base, version, _ := s.Get("pump-7")
	if _, err := s.Change("pump-7", "dispatch", diffFrom(t, base, func(a *model.Asset) {
		a.Status = model.StatusOffline
	})); err != nil {
		t.Fatal(err)
	}
	push := Push{ID: "pump-7", BaseVersion: version, Patch: diffFrom(t, base, func(a *model.Asset) {
		a.Status = model.StatusAttention
	})}
	res := s.Sync("ana", []Push{push})[0]
	if res.Asset.Status != model.StatusOffline {
		t.Fatalf("worse status lost: %+v", res.Asset)
	}
	if len(res.Conflicts) != 1 || res.Conflicts[0].Kept != "theirs" {
		t.Fatalf("conflict report: %+v", res.Conflicts)
	}
}

// A push whose outcome breaks validation is rejected whole; the record and
// its version stay put, and the response carries the authoritative state.
func TestSyncRejection(t *testing.T) {
	s := seeded(t)
	base, version, _ := s.Get("pump-7")
	push := Push{ID: "pump-7", BaseVersion: version, Patch: diffFrom(t, base, func(a *model.Asset) {
		a.Status = "broken-status"
	})}
	res := s.Sync("ana", []Push{push})[0]
	if res.Outcome != "rejected" || res.Error == "" {
		t.Fatalf("rejection: %+v", res)
	}
	_, v, _ := s.Get("pump-7")
	if v != version {
		t.Fatalf("rejected push spent a version: %d -> %d", version, v)
	}
}

// stateAt and ChangesSince: the version log reconstructs any past state, and
// the boundary diff replays the office's absence.
func TestVersionTravel(t *testing.T) {
	s := seeded(t)
	v1State, v1, _ := s.Get("pump-7")

	for i, edit := range []func(*model.Asset){
		func(a *model.Asset) { a.Status = model.StatusAttention },
		func(a *model.Asset) { a.Notes = "watch it" },
		func(a *model.Asset) { a.Assignee = "cyn" },
	} {
		cur, _, _ := s.Get("pump-7")
		if _, err := s.Change("pump-7", "dispatch", diffFrom(t, cur, edit)); err != nil {
			t.Fatalf("edit %d: %v", i, err)
		}
	}
	cur, curV, _ := s.Get("pump-7")
	if curV != v1+3 {
		t.Fatalf("version = %d, want %d", curV, v1+3)
	}

	since, err := s.ChangesSince("pump-7", v1)
	if err != nil {
		t.Fatal(err)
	}
	replayed := deep.Clone(v1State)
	if err := deep.Apply(&replayed, since); err != nil {
		t.Fatal(err)
	}
	if !deep.Equal(replayed, cur) {
		t.Fatalf("ChangesSince does not reach the present:\n got  %+v\n want %+v", replayed, cur)
	}

	if _, err := s.ChangesSince("pump-7", curV+1); err == nil {
		t.Fatal("future version accepted")
	}
}
