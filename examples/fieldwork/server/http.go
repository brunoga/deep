package server

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	deep "github.com/brunoga/deep/v6"

	"github.com/brunoga/deep/examples/fieldwork/model"
)

// API serves the store over HTTP: direct edits for the office, one /sync
// endpoint for the field.
type API struct {
	store *Store
	token string
	mux   *http.ServeMux
}

// APIOption configures the API.
type APIOption func(*API)

// WithToken requires "Authorization: Bearer <token>" on every request.
func WithToken(token string) APIOption {
	return func(a *API) { a.token = token }
}

// NewAPI wires the routes.
func NewAPI(store *Store, opts ...APIOption) *API {
	a := &API{store: store, mux: http.NewServeMux()}
	for _, o := range opts {
		o(a)
	}
	a.mux.HandleFunc("GET /assets", a.list)
	a.mux.HandleFunc("POST /assets", a.create)
	a.mux.HandleFunc("GET /assets/{id}", a.get)
	a.mux.HandleFunc("POST /assets/{id}/change", a.change)
	a.mux.HandleFunc("GET /assets/{id}/history", a.history)
	a.mux.HandleFunc("POST /sync", a.sync)
	return a
}

func (a *API) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if a.token != "" {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(got), []byte(a.token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}
	a.mux.ServeHTTP(w, r)
}

func author(r *http.Request) string {
	if who := r.Header.Get("X-Author"); who != "" {
		return who
	}
	return "anonymous"
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, ErrRejected), errors.Is(err, ErrBadVersion):
		status = http.StatusUnprocessableEntity
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

// ListResponse is the office's asset inventory.
type ListResponse struct {
	Assets   []model.Asset    `json:"assets"`
	Versions map[string]int64 `json:"versions"`
}

func (a *API) list(w http.ResponseWriter, _ *http.Request) {
	assets, versions := a.store.List()
	writeJSON(w, http.StatusOK, ListResponse{Assets: assets, Versions: versions})
}

func (a *API) create(w http.ResponseWriter, r *http.Request) {
	var asset model.Asset
	if err := json.NewDecoder(r.Body).Decode(&asset); err != nil {
		http.Error(w, "invalid asset: "+err.Error(), http.StatusBadRequest)
		return
	}
	created, version, err := a.store.Create(asset)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, GetResponse{Asset: created, Version: version})
}

// GetResponse pairs an asset with its version.
type GetResponse struct {
	Asset   model.Asset `json:"asset"`
	Version int64       `json:"version"`
}

func (a *API) get(w http.ResponseWriter, r *http.Request) {
	asset, version, err := a.store.Get(r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, GetResponse{Asset: asset, Version: version})
}

func (a *API) change(w http.ResponseWriter, r *http.Request) {
	var p deep.Patch[model.Asset]
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "invalid patch: "+err.Error(), http.StatusBadRequest)
		return
	}
	asset, version, err := a.store.Change(r.PathValue("id"), author(r), p)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, GetResponse{Asset: asset, Version: version})
}

func (a *API) history(w http.ResponseWriter, r *http.Request) {
	log, err := a.store.History(r.PathValue("id"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, log)
}

// SyncRequest is one round trip from the field: pushes for locally-edited
// assets, and the versions the client knows for everything else so the
// server can send what moved.
type SyncRequest struct {
	Pushes []Push           `json:"pushes,omitempty"`
	Known  map[string]int64 `json:"known,omitempty"`
}

// SyncResponse answers both halves: per-push results, and updates for
// records the client did not touch but that changed under it — plus any
// records it has never seen.
type SyncResponse struct {
	Results []PushResult  `json:"results,omitempty"`
	Updated []GetResponse `json:"updated,omitempty"`
}

func (a *API) sync(w http.ResponseWriter, r *http.Request) {
	var req SyncRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid sync request: "+err.Error(), http.StatusBadRequest)
		return
	}
	resp := SyncResponse{Results: a.store.Sync(author(r), req.Pushes)}

	pushed := map[string]bool{}
	for _, p := range req.Pushes {
		pushed[p.ID] = true
	}
	assets, versions := a.store.List()
	for _, asset := range assets {
		if pushed[asset.ID] {
			continue // its state travelled in the push result
		}
		if known, ok := req.Known[asset.ID]; !ok || versions[asset.ID] > known {
			resp.Updated = append(resp.Updated, GetResponse{Asset: asset, Version: versions[asset.ID]})
		}
	}
	writeJSON(w, http.StatusOK, resp)
}
