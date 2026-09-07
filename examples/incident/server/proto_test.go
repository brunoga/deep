package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	deepproto "github.com/brunoga/deep/proto"
	deep "github.com/brunoga/deep/v6"
	"github.com/brunoga/deep/v6/condition"
	"google.golang.org/protobuf/proto"

	"github.com/brunoga/deep/examples/incident/model"
	"github.com/brunoga/deep/examples/incident/pb"
)

// The protobuf face: a snapshot fetched as a message matches the store, and a
// patch sent in the wire envelope — built against the *generated* types, the
// way a non-Go sender would — lands on the Go model with its conditions
// intact.
func TestProtoEndpoints(t *testing.T) {
	srv, store := newTestAPI(t)
	if err := store.Create(model.Incident{ID: "inc-1", Title: "checkout errors",
		Severity: model.Sev2, Status: model.StatusOpen,
		Tasks: []model.Task{{ID: "t1", Text: "page db oncall"}}}); err != nil {
		t.Fatal(err)
	}

	// GET the snapshot as a protobuf message.
	req, _ := http.NewRequest("GET", srv.URL+"/incidents/inc-1/proto", nil)
	req.Header.Set("Authorization", "Bearer sekrit")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get .pb: %d %s", resp.StatusCode, data)
	}
	var snapshot pb.Incident
	if err := proto.Unmarshal(data, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.GetTitle() != "checkout errors" || snapshot.GetTasks()[0].GetId() != "t1" {
		t.Fatalf("snapshot: %+v", &snapshot)
	}

	// POST a patch in the envelope: add a keyed task whose value is a
	// protobuf message, Unless one already exists.
	envelopePatch := deep.Patch[*pb.Incident]{Operations: []deep.Operation{{
		Kind:   deep.OpAdd,
		Path:   "/tasks/statusbot",
		New:    &pb.Task{Id: "statusbot", Text: "tracking", Done: true},
		Unless: &condition.Condition{Op: "exists", Path: "/tasks/statusbot"},
	}}}
	envelope, err := deepproto.ToProto(envelopePatch)
	if err != nil {
		t.Fatal(err)
	}
	body, err := proto.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}

	post := func() Result {
		req, _ := http.NewRequest("POST", srv.URL+"/incidents/inc-1/patch.pb", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer sekrit")
		req.Header.Set("X-Author", "statusbot")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			data, _ := io.ReadAll(resp.Body)
			t.Fatalf("patch.pb: %d %s", resp.StatusCode, data)
		}
		var res Result
		if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
			t.Fatal(err)
		}
		return res
	}

	if res := post(); res.Applied != 1 {
		t.Fatalf("first envelope patch: %+v", res)
	}
	inc, _ := store.Get("inc-1")
	if len(inc.Tasks) != 2 || inc.Tasks[1].ID != "statusbot" || !inc.Tasks[1].Done {
		t.Fatalf("task did not land on the Go model: %+v", inc.Tasks)
	}
	if h, _ := store.History("inc-1"); h[len(h)-1].Author != "statusbot" {
		t.Fatalf("audit author: %+v", h[len(h)-1])
	}

	// Replaying the same envelope skips on its own condition — idempotent by
	// construction, no bot-side bookkeeping.
	if res := post(); res.Applied != 0 || res.Skipped != 1 {
		t.Fatalf("replayed envelope patch: %+v", res)
	}
}
