package server

import (
	"io"
	"net/http"

	deepproto "github.com/brunoga/deep/proto"
	"github.com/brunoga/deep/proto/wire"
	"google.golang.org/protobuf/proto"

	"github.com/brunoga/deep/examples/incident/model"
	"github.com/brunoga/deep/examples/incident/pb"
)

// The protobuf face of the API: snapshots go out as commandpost.Incident
// messages, and patches come in inside the deep wire envelope
// (wire.Patch) — the endpoint a consumer in another language uses. The
// envelope is type-neutral: whatever representation the sender built its
// patch against, the operations decode here against the Go model.

func (a *API) registerProtoRoutes() {
	a.mux.HandleFunc("GET /incidents/{id}/proto", a.getProto)
	a.mux.HandleFunc("POST /incidents/{id}/patch.pb", a.patchProto)
}

func (a *API) getProto(w http.ResponseWriter, r *http.Request) {
	inc, err := a.store.Get(r.PathValue("id"))
	if err != nil {
		writeError(w, err, Result{})
		return
	}
	data, err := proto.Marshal(pb.FromModel(inc))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/x-protobuf")
	_, _ = w.Write(data)
}

func (a *API) patchProto(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var envelope wire.Patch
	if err := proto.Unmarshal(body, &envelope); err != nil {
		http.Error(w, "invalid patch envelope: "+err.Error(), http.StatusBadRequest)
		return
	}
	p, err := deepproto.FromProto[model.Incident](&envelope)
	if err != nil {
		http.Error(w, "invalid patch: "+err.Error(), http.StatusBadRequest)
		return
	}
	res, err := a.store.ApplyPatch(r.PathValue("id"), author(r), p)
	if err != nil {
		writeError(w, err, res)
		return
	}
	writeJSON(w, http.StatusOK, res)
}
