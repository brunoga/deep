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

	mu   sync.Mutex
	live map[string]bool
}

// New opens a server keeping its documents in dir. idle is how long a room
// with nobody in it lingers before being written out and dropped.
func New(dir string, idle time.Duration) (*Server, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	s := &Server{dir: dir, live: map[string]bool{}}
	s.hub = deepws.NewHub(
		deepws.WithAuth(func(_ *http.Request, room string) error {
			if !NamePattern.MatchString(room) {
				return fmt.Errorf("bad document name")
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

func (s *Server) seed(name string) error {
	u, ok := s.load(name)
	s.mu.Lock()
	s.live[name] = true
	s.mu.Unlock()
	if ok {
		s.hub.Room(name, func(doc *crdt.Document) { doc.Apply(u) })
	}
	return nil
}

func (s *Server) persist(name string, doc *crdt.Document) {
	s.write(name, doc)
	s.mu.Lock()
	delete(s.live, name)
	s.mu.Unlock()
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

	data, err := merged.Since(crdt.StateVector{}).MarshalBinary()
	if err != nil {
		slog.Error("encoding a document", "name", name, "err", err)
		return
	}
	tmp := s.path(name) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		slog.Error("writing a document", "name", name, "err", err)
		return
	}
	if err := os.Rename(tmp, s.path(name)); err != nil {
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
	s.mu.Lock()
	live := s.live[name]
	s.mu.Unlock()

	var text string
	if live {
		s.hub.Room(name, func(doc *crdt.Document) { text = doc.String() })
	} else if u, ok := s.load(name); ok {
		doc := crdt.NewDocument(hlc.NewClock("reader"))
		doc.Apply(u)
		text = doc.String()
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
