// Package server hosts the collaborative editor: one CRDT document per file,
// a websocket room for each, and a directory to keep them in.
//
// There is very little here on purpose. The hard part of a collaborative
// editor — that two people typing in the same place converge, and that
// neither has to ask permission — is the document's own property, not the
// server's. What the server contributes is the room, the durability, and the
// list of files.
package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/brunoga/deep/v6/crdt"
	"github.com/brunoga/deep/v6/crdt/hlc"
	deepws "github.com/brunoga/deep/ws"
)

// NamePattern constrains file names: they become room names, path segments
// and file names on disk.
var NamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

// Server serves the editor: the websocket rooms, the file list, and the
// static client.
type Server struct {
	hub *deepws.Hub
	dir string

	mu sync.Mutex
	// live maps a document to the room document currently holding it. The
	// value is an identity, not a flag: an eviction hands back the document
	// it evicted, and a room created since — by somebody who joined while the
	// eviction was in flight — must not be marked dead by it.
	live map[string]*crdt.Document
}

// New opens a server keeping its documents in dir. idle is how long a room
// with nobody in it lingers before being written out and dropped.
func New(dir string, idle time.Duration) (*Server, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	s := &Server{dir: dir, live: map[string]*crdt.Document{}}
	s.hub = deepws.NewHub(
		deepws.WithAuth(func(r *http.Request, room string) error {
			if !NamePattern.MatchString(room) {
				return fmt.Errorf("bad document name")
			}
			// Seeding has a side effect — it creates the room — and this hook
			// runs before the upgrade, so a request that is not even trying
			// to become a websocket is refused here rather than allowed to
			// bring a document into being by asking for it. A determined
			// client can still forge the headers; what this stops is every
			// crawler, probe and mistyped URL doing it by accident.
			if !isUpgrade(r) {
				return fmt.Errorf("not a websocket request")
			}
			// Seeding from the auth hook runs before the upgrade, so a joining
			// client's handshake already sees the file's contents. Applying an
			// update a document already holds is a no-op, so doing this on
			// every join costs a file read and nothing else — and a room that
			// raced an eviction gets its history back rather than silently
			// starting empty.
			return s.seed(room)
		}),
		deepws.WithRoomEviction(idle, s.persist),
	)
	return s, nil
}

// Hub exposes the websocket handler.
func (s *Server) Hub() http.Handler { return s.hub }

func (s *Server) path(name string) string { return filepath.Join(s.dir, name+".doc") }

// load reads a document's stored state. A file that cannot be decoded is set
// aside rather than allowed to lock everybody out of the room.
func (s *Server) load(name string) (crdt.Update, bool) {
	data, err := os.ReadFile(s.path(name))
	if err != nil {
		return crdt.Update{}, false
	}
	var u crdt.Update
	if err := u.UnmarshalBinary(data); err != nil {
		aside := fmt.Sprintf("%s.corrupt-%d", s.path(name), time.Now().Unix())
		_ = os.Rename(s.path(name), aside)
		slog.Error("quarantined a corrupt document", "name", name, "moved_to", aside, "err", err)
		return crdt.Update{}, false
	}
	return u, true
}

// isUpgrade reports whether a request is asking to become a websocket.
func isUpgrade(r *http.Request) bool {
	for _, token := range strings.Split(r.Header.Get("Connection"), ",") {
		if strings.EqualFold(strings.TrimSpace(token), "upgrade") {
			return strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
		}
	}
	return false
}

func (s *Server) seed(name string) error {
	u, ok := s.load(name)
	// The room is marked live from inside the callback, which runs with the
	// room in hand: marking it before would leave an eviction finishing in
	// the background free to mark it dead again a moment later.
	s.hub.Room(name, func(doc *crdt.Document) {
		if ok {
			doc.Apply(u)
		}
		s.mu.Lock()
		s.live[name] = doc
		s.mu.Unlock()
	})
	return nil
}

func (s *Server) persist(name string, doc *crdt.Document) {
	s.write(name, doc)
	s.mu.Lock()
	if s.live[name] == doc {
		delete(s.live, name)
	}
	s.mu.Unlock()
}

// liveDoc reports the document a room is holding, or nil when nobody has the
// document open.
func (s *Server) liveDoc(name string) *crdt.Document {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.live[name]
}

// write saves a document, merged over whatever the file already holds and
// renamed into place: a write can lose nothing, and a crash mid-write
// corrupts nothing.
func (s *Server) write(name string, doc *crdt.Document) {
	merged := crdt.NewDocument(hlc.NewClock("editor:" + name))
	if u, ok := s.load(name); ok {
		merged.Apply(u)
	}
	merged.Apply(doc.Since(crdt.StateVector{}))

	if merged.String() == "" {
		if _, err := os.Stat(s.path(name)); err != nil {
			// A room nobody typed into is not a document. Rooms are cheap to
			// bring into being — anyone who can reach the websocket can name
			// one — and writing a file for each would turn that into disk.
			return
		}
	}

	data, err := merged.Since(crdt.StateVector{}).MarshalBinary()
	if err != nil {
		slog.Error("encoding a document", "name", name, "err", err)
		return
	}
	// A unique temp file, not a fixed one: a shutdown and an eviction can
	// write the same document at the same moment, and two writers sharing a
	// path can rename a half-written file into place — exactly what the
	// rename is meant to prevent.
	tmp, err := os.CreateTemp(s.dir, name+".*.tmp")
	if err != nil {
		slog.Error("writing a document", "name", name, "err", err)
		return
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		slog.Error("writing a document", "name", name, "err", err)
		return
	}
	if err := tmp.Close(); err != nil {
		slog.Error("writing a document", "name", name, "err", err)
		return
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		slog.Error("writing a document", "name", name, "err", err)
		return
	}
	if err := os.Rename(tmp.Name(), s.path(name)); err != nil {
		slog.Error("writing a document", "name", name, "err", err)
	}
}

// Persist writes every live room to disk without evicting anyone — the
// graceful-shutdown path, so a document typed into a minute ago does not ride
// on the eviction timer to survive a restart.
func (s *Server) Persist() {
	s.mu.Lock()
	names := slices.Sorted(maps.Keys(s.live))
	s.mu.Unlock()
	for _, name := range names {
		s.hub.Room(name, func(doc *crdt.Document) { s.write(name, doc) })
	}
}

// Document is one file in the listing.
type Document struct {
	Name  string `json:"name"`
	Lines int    `json:"lines"`
	Chars int    `json:"chars"`
	Live  bool   `json:"live"`
}

// List reports the documents on disk and in memory, newest state first.
func (s *Server) List() []Document {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil
	}
	// The names are gathered first and described afterwards, with no lock
	// held: describing a document reads the room, and reading the room takes
	// the same lock — doing it inside the loop deadlocks the moment somebody
	// is actually editing.
	names := map[string]bool{}
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".doc")
		if !ok || !NamePattern.MatchString(name) {
			continue
		}
		names[name] = true
	}
	s.mu.Lock()
	for name := range s.live {
		names[name] = true
	}
	s.mu.Unlock()

	out := make([]Document, 0, len(names))
	for _, name := range slices.Sorted(maps.Keys(names)) {
		out = append(out, s.describe(name))
	}
	return out
}

func (s *Server) describe(name string) Document {
	live := s.liveDoc(name) != nil

	var text string
	if live {
		s.hub.Room(name, func(doc *crdt.Document) { text = doc.String() })
	}
	if text == "" {
		// Either the document is not open, or the room was evicted between
		// the check and the read and what came back was a fresh empty one.
		// The file is the answer in both cases.
		if u, ok := s.load(name); ok {
			doc := crdt.NewDocument(hlc.NewClock("reader"))
			doc.Apply(u)
			text = doc.String()
		}
	}
	return Document{
		Name:  name,
		Lines: strings.Count(text, "\n") + 1,
		Chars: len([]rune(text)),
		Live:  live,
	}
}

// Create registers an empty document so it shows in the listing before
// anybody has typed into it.
func (s *Server) Create(name string) error {
	if !NamePattern.MatchString(name) {
		return fmt.Errorf("bad document name %q", name)
	}
	if _, err := os.Stat(s.path(name)); err == nil {
		return fmt.Errorf("document %q already exists", name)
	}
	return os.WriteFile(s.path(name), mustEncodeEmpty(), 0o644)
}

func mustEncodeEmpty() []byte {
	data, err := crdt.NewDocument(hlc.NewClock("editor")).Since(crdt.StateVector{}).MarshalBinary()
	if err != nil {
		panic(fmt.Sprintf("editor: encoding an empty document: %v", err))
	}
	return data
}

// API serves the document listing.
func (s *Server) API() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /documents", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, s.List())
	})
	mux.HandleFunc("POST /documents", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request: "+err.Error(), http.StatusBadRequest)
			return
		}
		if err := s.Create(req.Name); err != nil {
			http.Error(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		writeJSON(w, http.StatusCreated, s.describe(req.Name))
	})
	return mux
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
