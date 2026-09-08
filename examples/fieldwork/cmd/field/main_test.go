package main

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/brunoga/deep/examples/fieldwork/client"
	"github.com/brunoga/deep/examples/fieldwork/model"
	"github.com/brunoga/deep/examples/fieldwork/server"
)

func cli(t *testing.T) (*client.Client, *client.Local) {
	t.Helper()
	store := server.NewStore()
	if _, _, err := store.Create(model.Asset{
		ID: "pump-7", Name: "Intake pump 7", Site: "riverside", Status: model.StatusOK,
		Checks: []model.Check{{ID: "seals", Label: "inspect seals"}},
	}); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(server.NewAPI(store))
	t.Cleanup(srv.Close)

	c := client.New(srv.URL, "", "ana")
	local, err := client.OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Sync(local); err != nil {
		t.Fatal(err)
	}
	return c, local
}

// A mistyped check id must be an error. Silently succeeding is worse than
// useless here: the technician believes an inspection is recorded.
func TestCheckRejectsUnknownID(t *testing.T) {
	c, local := cli(t)

	err := run(c, local, []string{"check", "pump-7", "sealz"})
	if err == nil {
		t.Fatal("mistyped check id reported success")
	}
	if !strings.Contains(err.Error(), "sealz") {
		t.Fatalf("error should name the check: %v", err)
	}
	pending, _ := local.Pending()
	if len(pending) != 0 {
		t.Fatalf("failed check left work pending: %+v", pending)
	}

	if err := run(c, local, []string{"check", "pump-7", "seals"}); err != nil {
		t.Fatal(err)
	}
	got, _ := local.Get("pump-7")
	if !got.Checks[0].Done || got.Checks[0].By != "ana" {
		t.Fatalf("real check did not land: %+v", got.Checks)
	}
}
