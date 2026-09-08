package client

import (
	"net/http/httptest"
	"testing"

	deep "github.com/brunoga/deep/v6"

	"github.com/brunoga/deep/examples/fieldwork/model"
	"github.com/brunoga/deep/examples/fieldwork/server"
)

func fixture(t *testing.T) (*server.Store, *Client, *Local) {
	t.Helper()
	store := server.NewStore()
	if _, _, err := store.Create(model.Asset{
		ID: "pump-7", Name: "Intake pump 7", Site: "riverside", Status: model.StatusOK,
		Assignee: "ana",
		Readings: map[string]model.Reading{"ph": {Value: 7.1, Unit: "pH"}},
		Checks:   []model.Check{{ID: "seals", Label: "inspect seals"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Create(model.Asset{
		ID: "valve-2", Name: "Outflow valve", Site: "riverside", Status: model.StatusOK,
	}); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(server.NewAPI(store))
	t.Cleanup(srv.Close)

	c := New(srv.URL, "", "ana")
	local, err := OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// First sync with nothing pending is the initial download.
	if _, err := c.Sync(local); err != nil {
		t.Fatal(err)
	}
	return store, c, local
}

// The outbox is derived, not maintained: ten offline edits to one field
// still owe the server one operation, and an edit that is undone owes
// nothing at all.
func TestPendingIsDerived(t *testing.T) {
	_, _, local := fixture(t)

	for i := range 10 {
		if err := local.Edit("pump-7", func(a *model.Asset) {
			r := a.Readings["ph"]
			r.Value = 6.0 + float64(i)/10
			r.By = "ana"
			a.Readings["ph"] = r
		}); err != nil {
			t.Fatal(err)
		}
	}
	pending, err := local.Pending()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("want one asset pending, got %d", len(pending))
	}
	if n := len(pending[0].Patch.Operations); n != 2 { // value and by
		t.Fatalf("ten edits produced %d operations: %s", n, pending[0].Patch)
	}

	// Undo the edit by hand; the outbox empties itself.
	if err := local.Edit("pump-7", func(a *model.Asset) {
		a.Readings["ph"] = model.Reading{Value: 7.1, Unit: "pH"}
	}); err != nil {
		t.Fatal(err)
	}
	pending, err = local.Pending()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("undone work still pending: %+v", pending)
	}
}

// The full offline story: the device works with no server, the office moves
// meanwhile, and one sync reconciles both — with the policy's decisions
// reported rather than silent.
func TestOfflineRoundTrip(t *testing.T) {
	store, c, local := fixture(t)

	// ── Offline. Several assets, several kinds of edit. ──────────────
	if err := local.Edit("pump-7", func(a *model.Asset) {
		a.Status = model.StatusFault
		r := a.Readings["ph"]
		r.Value, r.By = 6.4, "ana"
		a.Readings["ph"] = r
		a.Readings["flow"] = model.Reading{Value: 118, Unit: "l/min", By: "ana"}
		a.Checks[0].Done, a.Checks[0].By = true, "ana"
		a.Notes = "seals leaking"
	}); err != nil {
		t.Fatal(err)
	}
	if err := local.Edit("valve-2", func(a *model.Asset) {
		a.Status = model.StatusAttention
	}); err != nil {
		t.Fatal(err)
	}

	// ── Meanwhile, the office. ───────────────────────────────────────
	base, _, _ := store.Get("pump-7")
	office := deep.Clone(base)
	office.Assignee = "bruno"
	office.Notes = "scada flagged drift"
	office.Status = model.StatusAttention
	officePatch, err := deep.Diff(base, office)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Change("pump-7", "dispatch", officePatch); err != nil {
		t.Fatal(err)
	}

	// ── One round trip. ──────────────────────────────────────────────
	report, err := c.Sync(local)
	if err != nil {
		t.Fatal(err)
	}
	if report.Merged != 1 || report.Pushed != 1 {
		t.Fatalf("report: %+v", report)
	}
	if len(report.Rejected) != 0 {
		t.Fatalf("unexpected rejection: %+v", report.Rejected)
	}
	if len(report.Conflicts) != 1 {
		t.Fatalf("conflicts should have been reported: %+v", report)
	}

	// Field measurements and the worse status survived; the office's
	// assignment survived; both notes are there.
	got, err := local.Get("pump-7")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != model.StatusFault {
		t.Errorf("status: %v", got.Status)
	}
	if got.Readings["ph"].Value != 6.4 || got.Readings["flow"].Value != 118 {
		t.Errorf("readings: %+v", got.Readings)
	}
	if got.Assignee != "bruno" {
		t.Errorf("assignee: %q", got.Assignee)
	}
	if !got.Checks[0].Done {
		t.Errorf("check lost: %+v", got.Checks)
	}

	// The device and the server agree, and nothing is outstanding.
	serverState, _, _ := store.Get("pump-7")
	if !deep.Equal(got, serverState) {
		t.Fatalf("device and server disagree:\n device %+v\n server %+v", got, serverState)
	}
	pending, err := local.Pending()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("still pending after sync: %+v", pending)
	}

	// A second sync is a no-op: nothing to push, nothing new to adopt.
	report2, err := c.Sync(local)
	if err != nil {
		t.Fatal(err)
	}
	if report2.Pushed != 0 || report2.Merged != 0 || report2.Updated != 0 {
		t.Fatalf("idempotent sync did work: %+v", report2)
	}
}

// A server-side rejection keeps the technician's work: the device can fix
// the problem and sync again, and nothing is lost in between.
func TestRejectionKeepsLocalWork(t *testing.T) {
	_, c, local := fixture(t)

	if err := local.Edit("pump-7", func(a *model.Asset) {
		a.Name = "" // legal locally? no — Validate refuses it
	}); err == nil {
		t.Fatal("local Edit accepted an invalid asset")
	}

	// Force an invalid working copy the way a buggy peer would, bypassing
	// Edit's validation, and check the server refuses it and the device
	// keeps the work.
	if err := local.Edit("pump-7", func(a *model.Asset) {
		a.Status = model.StatusFault
		a.Notes = "genuine work"
	}); err != nil {
		t.Fatal(err)
	}
	local.mu.Lock()
	local.entries["pump-7"].Working.Status = "not-a-status"
	local.mu.Unlock()

	report, err := c.Sync(local)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Rejected) != 1 {
		t.Fatalf("want a rejection, got %+v", report)
	}
	got, _ := local.Get("pump-7")
	if got.Notes != "genuine work" {
		t.Fatalf("rejection discarded local work: %+v", got)
	}

	// Fix it and sync again: the corrected diff lands.
	if err := local.Edit("pump-7", func(a *model.Asset) { a.Status = model.StatusFault }); err != nil {
		t.Fatal(err)
	}
	report, err = c.Sync(local)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Rejected) != 0 || report.Pushed+report.Merged != 1 {
		t.Fatalf("retry after fix: %+v", report)
	}
	got, _ = local.Get("pump-7")
	if got.Status != model.StatusFault || got.Notes != "genuine work" {
		t.Fatalf("fixed sync outcome: %+v", got)
	}
}

// The device survives being switched off: shadow, working copy and base
// version all come back, and the outbox is still exactly the difference.
func TestLocalStorePersists(t *testing.T) {
	_, _, local := fixture(t)
	dir := local.dir

	if err := local.Edit("pump-7", func(a *model.Asset) {
		a.Notes = "written before the battery died"
	}); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenLocal(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := reopened.Get("pump-7")
	if err != nil {
		t.Fatal(err)
	}
	if got.Notes != "written before the battery died" {
		t.Fatalf("working copy lost: %+v", got)
	}
	pending, err := reopened.Pending()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || len(pending[0].Patch.Operations) != 1 {
		t.Fatalf("outbox not reconstructed: %+v", pending)
	}
	if pending[0].BaseVersion == 0 {
		t.Fatal("base version lost")
	}
}

// Revert throws local work away without touching the server.
func TestRevert(t *testing.T) {
	_, _, local := fixture(t)
	before, _ := local.Get("pump-7")

	if err := local.Edit("pump-7", func(a *model.Asset) { a.Notes = "oops" }); err != nil {
		t.Fatal(err)
	}
	if err := local.Revert("pump-7"); err != nil {
		t.Fatal(err)
	}
	after, _ := local.Get("pump-7")
	if !deep.Equal(after, before) {
		t.Fatalf("revert did not restore:\n got  %+v\n want %+v", after, before)
	}
	pending, _ := local.Pending()
	if len(pending) != 0 {
		t.Fatalf("revert left work pending: %+v", pending)
	}
}
