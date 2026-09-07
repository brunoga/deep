package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	deep "github.com/brunoga/deep/v6"

	"github.com/brunoga/deep/examples/incident/model"
)

func newTestAPI(t *testing.T) (*httptest.Server, *Store) {
	t.Helper()
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(NewAPI(store, WithToken("sekrit")))
	t.Cleanup(srv.Close)
	return srv, store
}

func do(t *testing.T, method, url string, body any, out any) int {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	req, err := http.NewRequest(method, url, &buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer sekrit")
	req.Header.Set("X-Author", "ana")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatalf("%s %s: decoding response: %v", method, url, err)
		}
	}
	return resp.StatusCode
}

func TestHTTPLifecycle(t *testing.T) {
	srv, _ := newTestAPI(t)

	inc := model.Incident{
		ID: "inc-1", Title: "checkout errors", Severity: model.Sev2, Status: model.StatusOpen,
		Tasks: []model.Task{{ID: "t1", Text: "page db oncall"}},
	}
	if code := do(t, "POST", srv.URL+"/incidents", inc, nil); code != http.StatusCreated {
		t.Fatalf("create: %d", code)
	}

	// Patch over the wire, exactly as a client builds it.
	p := deep.NewPatch[model.Incident]().With(
		deep.Set(deep.PathString[model.Incident, string]("/tasks/t1/owner"), "ana").
			If(deep.Eq(deep.PathString[model.Incident, string]("/tasks/t1/owner"), "")),
	).Build()
	var res Result
	if code := do(t, "POST", srv.URL+"/incidents/inc-1/patch", p, &res); code != http.StatusOK {
		t.Fatalf("patch: %d", code)
	}
	if res.Applied != 1 || res.Seq == 0 {
		t.Fatalf("patch result: %+v", res)
	}

	var got model.Incident
	if code := do(t, "GET", srv.URL+"/incidents/inc-1", nil, &got); code != http.StatusOK {
		t.Fatalf("get: %d", code)
	}
	if got.Tasks[0].Owner != "ana" {
		t.Fatalf("owner = %q", got.Tasks[0].Owner)
	}

	var history []LogEntry
	if code := do(t, "GET", srv.URL+"/incidents/inc-1/history", nil, &history); code != http.StatusOK {
		t.Fatalf("history: %d", code)
	}
	if len(history) != 1 || history[0].Author != "ana" {
		t.Fatalf("history: %+v", history)
	}

	var undoRes Result
	if code := do(t, "POST", srv.URL+"/incidents/inc-1/undo", map[string]int64{"seq": res.Seq}, &undoRes); code != http.StatusOK {
		t.Fatalf("undo: %d", code)
	}
	// A fresh variable on purpose: owner is omitempty, and decoding into the
	// previous response would merge over the stale value instead of clearing it.
	var after model.Incident
	do(t, "GET", srv.URL+"/incidents/inc-1", nil, &after)
	if after.Tasks[0].Owner != "" {
		t.Fatalf("undo did not clear owner: %q", after.Tasks[0].Owner)
	}
}

func TestHTTPStatusCodes(t *testing.T) {
	srv, store := newTestAPI(t)
	if err := store.Create(model.Incident{ID: "inc-1", Title: "x", Severity: model.Sev3, Status: model.StatusOpen,
		Tasks: []model.Task{{ID: "t1", Text: "open task"}}}); err != nil {
		t.Fatal(err)
	}

	// No token: 401.
	resp, err := http.Get(srv.URL + "/incidents")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no token: %d", resp.StatusCode)
	}

	// Unknown incident: 404.
	if code := do(t, "GET", srv.URL+"/incidents/nope", nil, nil); code != http.StatusNotFound {
		t.Fatalf("unknown: %d", code)
	}

	// Guard not met: 409.
	statusPath := deep.PathString[model.Incident, model.Status]("/status")
	guarded := deep.NewPatch[model.Incident]().
		Guard(deep.Eq(statusPath, model.StatusResolved)).
		With(deep.Set(statusPath, model.StatusClosed)).Build()
	if code := do(t, "POST", srv.URL+"/incidents/inc-1/patch", guarded, nil); code != http.StatusConflict {
		t.Fatalf("guard: %d", code)
	}

	// Valid patch, invalid outcome: 422.
	closing := deep.NewPatch[model.Incident]().With(deep.Set(statusPath, model.StatusClosed)).Build()
	if code := do(t, "POST", srv.URL+"/incidents/inc-1/patch", closing, nil); code != http.StatusUnprocessableEntity {
		t.Fatalf("validation: %d", code)
	}

	// Garbage body: 400.
	req, _ := http.NewRequest("POST", srv.URL+"/incidents/inc-1/patch", bytes.NewBufferString("{"))
	req.Header.Set("Authorization", "Bearer sekrit")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("garbage: %d", resp.StatusCode)
	}
}
