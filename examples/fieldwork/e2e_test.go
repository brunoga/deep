// The whole fieldwork story against one real server: two devices and the
// office editing the same assets, offline and online, reconciled by sync.
package fieldwork_test

import (
	"net/http/httptest"
	"strings"
	"testing"

	deep "github.com/brunoga/deep/v6"

	"github.com/brunoga/deep/examples/fieldwork/client"
	"github.com/brunoga/deep/examples/fieldwork/model"
	"github.com/brunoga/deep/examples/fieldwork/server"
)

func TestTwoDevicesAndTheOffice(t *testing.T) {
	store := server.NewStore()
	if err := store.Create(model.Asset{
		ID: "pump-7", Name: "Intake pump 7", Site: "riverside", Status: model.StatusOK,
		Readings: map[string]model.Reading{"ph": {Value: 7.1, Unit: "pH", By: "scada"}},
		Checks: []model.Check{
			{ID: "seals", Label: "inspect seals"},
			{ID: "filter", Label: "replace filter"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(server.NewAPI(store, server.WithToken("sekrit")))
	defer srv.Close()

	// Two technicians, each with their own device, both starting synced.
	ana := client.New(srv.URL, "sekrit", "ana")
	anaDev, err := client.OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	bo := client.New(srv.URL, "sekrit", "bo")
	boDev, err := client.OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, pair := range []struct {
		c *client.Client
		l *client.Local
	}{{ana, anaDev}, {bo, boDev}} {
		if _, err := pair.c.Sync(pair.l); err != nil {
			t.Fatal(err)
		}
	}

	// ── Everyone works at once, nobody synced. ───────────────────────
	if err := anaDev.Edit("pump-7", func(a *model.Asset) {
		r := a.Readings["ph"]
		r.Value, r.By = 6.4, "ana"
		a.Readings["ph"] = r
		a.Checks[0].Done, a.Checks[0].By = true, "ana"
		a.Status = model.StatusFault
		a.Notes = "seals leaking"
	}); err != nil {
		t.Fatal(err)
	}
	if err := boDev.Edit("pump-7", func(a *model.Asset) {
		a.Readings["temp"] = model.Reading{Value: 41.2, Unit: "C", By: "bo"}
		a.Checks[1].Done, a.Checks[1].By = true, "bo"
		a.Notes = "filter swapped"
	}); err != nil {
		t.Fatal(err)
	}
	office, _, _ := store.Get("pump-7")
	next := deep.Clone(office)
	next.Assignee = "bruno"
	officePatch, err := deep.Diff(office, next)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Change("pump-7", "dispatch", officePatch); err != nil {
		t.Fatal(err)
	}

	// ── They come back into signal one after the other. ──────────────
	anaReport, err := ana.Sync(anaDev)
	if err != nil {
		t.Fatal(err)
	}
	if anaReport.Merged != 1 {
		t.Fatalf("ana's sync: %+v", anaReport)
	}
	boReport, err := bo.Sync(boDev)
	if err != nil {
		t.Fatal(err)
	}
	if boReport.Merged != 1 {
		t.Fatalf("bo's sync: %+v", boReport)
	}

	// ── Everything from everybody survived. ──────────────────────────
	final, _, _ := store.Get("pump-7")
	if final.Readings["ph"].Value != 6.4 || final.Readings["ph"].By != "ana" {
		t.Errorf("ana's reading lost: %+v", final.Readings)
	}
	if final.Readings["temp"].Value != 41.2 {
		t.Errorf("bo's reading lost: %+v", final.Readings)
	}
	if !final.Checks[0].Done || final.Checks[0].By != "ana" {
		t.Errorf("ana's check lost: %+v", final.Checks)
	}
	if !final.Checks[1].Done || final.Checks[1].By != "bo" {
		t.Errorf("bo's check lost: %+v", final.Checks)
	}
	if final.Status != model.StatusFault {
		t.Errorf("worse status lost: %v", final.Status)
	}
	if final.Assignee != "bruno" {
		t.Errorf("office reassignment lost: %q", final.Assignee)
	}
	for _, note := range []string{"seals leaking", "filter swapped"} {
		if !strings.Contains(final.Notes, note) {
			t.Errorf("note %q lost: %q", note, final.Notes)
		}
	}

	// ── And everyone converges: one more sync each, all three agree. ──
	if _, err := ana.Sync(anaDev); err != nil {
		t.Fatal(err)
	}
	if _, err := bo.Sync(boDev); err != nil {
		t.Fatal(err)
	}
	anaView, _ := anaDev.Get("pump-7")
	boView, _ := boDev.Get("pump-7")
	serverView, _, _ := store.Get("pump-7")
	if !deep.Equal(anaView, serverView) || !deep.Equal(boView, serverView) {
		t.Fatalf("devices did not converge:\n ana    %+v\n bo     %+v\n server %+v",
			anaView, boView, serverView)
	}
	for _, dev := range []*client.Local{anaDev, boDev} {
		pending, err := dev.Pending()
		if err != nil {
			t.Fatal(err)
		}
		if len(pending) != 0 {
			t.Fatalf("device still owes work after converging: %+v", pending)
		}
	}

	// The server's version log tells the whole story, and every entry
	// reverses — the history is real, not decorative.
	log, err := store.History("pump-7")
	if err != nil {
		t.Fatal(err)
	}
	authors := map[string]bool{}
	for _, e := range log {
		authors[e.Author] = true
	}
	for _, who := range []string{"ana", "bo", "dispatch"} {
		if !authors[who] {
			t.Errorf("history missing %s: %v", who, authors)
		}
	}
	back := deep.Clone(serverView)
	for i := len(log) - 1; i >= 0; i-- {
		if err := deep.Apply(&back, log[i].Patch.Reverse()); err != nil {
			t.Fatalf("reversing v%d: %v", log[i].Version, err)
		}
	}
	if back.Status != model.StatusOK || back.Readings["ph"].Value != 7.1 || len(back.Readings) != 1 {
		t.Fatalf("full rewind did not reach the original: %+v", back)
	}
}
