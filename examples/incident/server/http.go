package server

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	deep "github.com/brunoga/deep/v6"

	"github.com/brunoga/deep/examples/incident/model"
)

// API serves the store over HTTP. Structured edits travel as deep patches in
// JSON; the notes websocket (the hub) is mounted alongside by the caller.
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
	a.mux.HandleFunc("POST /incidents", a.create)
	a.mux.HandleFunc("GET /incidents", a.list)
	a.mux.HandleFunc("GET /incidents/{id}", a.get)
	a.mux.HandleFunc("POST /incidents/{id}/patch", a.patch)
	a.mux.HandleFunc("GET /incidents/{id}/history", a.history)
	a.mux.HandleFunc("POST /incidents/{id}/undo", a.undo)
	a.mux.HandleFunc("POST /incidents/{id}/compact", a.compact)
	a.registerProtoRoutes()
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

// Authorized reports whether a request carries the API's token; the hub's
// auth hook shares the check so the websocket side honours the same token.
func (a *API) Authorized(r *http.Request) bool {
	if a.token == "" {
		return true
	}
	// The browserless websocket client sends the token as a header; allow a
	// query parameter too for clients that cannot set headers.
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if got == "" {
		got = r.URL.Query().Get("token")
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(a.token)) == 1
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

// writeError maps the store's error taxonomy onto status codes and still
// includes the per-operation outcomes when there are any: a client that sent
// three conditional operations wants to know which one conflicted.
func writeError(w http.ResponseWriter, err error, res Result) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, ErrConflict):
		status = http.StatusConflict
	case errors.Is(err, ErrValidation):
		status = http.StatusUnprocessableEntity
	}
	writeJSON(w, status, struct {
		Error string `json:"error"`
		Result
	}{err.Error(), res})
}

func (a *API) create(w http.ResponseWriter, r *http.Request) {
	var inc model.Incident
	if err := json.NewDecoder(r.Body).Decode(&inc); err != nil {
		http.Error(w, "invalid incident: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := a.store.Create(inc); err != nil {
		writeError(w, err, Result{})
		return
	}
	created, err := a.store.Get(inc.ID)
	if err != nil {
		writeError(w, err, Result{})
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (a *API) list(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, a.store.List())
}

func (a *API) get(w http.ResponseWriter, r *http.Request) {
	inc, err := a.store.Get(r.PathValue("id"))
	if err != nil {
		writeError(w, err, Result{})
		return
	}
	writeJSON(w, http.StatusOK, inc)
}

func (a *API) patch(w http.ResponseWriter, r *http.Request) {
	var p deep.Patch[model.Incident]
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
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

func (a *API) history(w http.ResponseWriter, r *http.Request) {
	log, err := a.store.History(r.PathValue("id"))
	if err != nil {
		writeError(w, err, Result{})
		return
	}
	writeJSON(w, http.StatusOK, log)
}

func (a *API) compact(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Keep int `json:"keep"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid compact request: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := a.store.Compact(r.PathValue("id"), req.Keep); err != nil {
		writeError(w, err, Result{})
		return
	}
	log, err := a.store.History(r.PathValue("id"))
	if err != nil {
		writeError(w, err, Result{})
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"entries": len(log)})
}

func (a *API) undo(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Seq int64 `json:"seq"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid undo request: "+err.Error(), http.StatusBadRequest)
		return
	}
	res, err := a.store.Undo(r.PathValue("id"), author(r), req.Seq)
	if err != nil {
		writeError(w, err, res)
		return
	}
	writeJSON(w, http.StatusOK, res)
}
