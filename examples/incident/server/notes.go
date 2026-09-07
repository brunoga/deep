package server

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/brunoga/deep/v6/crdt"
	"github.com/brunoga/deep/v6/crdt/hlc"
	deepws "github.com/brunoga/deep/ws"
)

// Notes serves each incident's collaborative timeline: one CRDT document per
// incident, in a websocket room named by the incident ID. The hub does the
// syncing; this wrapper adds what a host application owns — authorization,
// seeding a room from disk when the first responder arrives, and persisting
// it back when the last one leaves.
type Notes struct {
	hub *deepws.Hub
	dir string

	mu     sync.Mutex
	seeded map[string]bool
}

// NewNotes builds the notes hub. authorized gates each join (the API's token
// check, shared so both transports honour the same token); exists confirms
// the room names a real incident. idle is how long an empty room lingers
// before its document is persisted and dropped.
func NewNotes(dir string, idle time.Duration, authorized func(*http.Request) bool, exists func(id string) bool) (*Notes, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	n := &Notes{dir: dir, seeded: map[string]bool{}}
	n.hub = deepws.NewHub(
		deepws.WithAuth(func(r *http.Request, room string) error {
			if !authorized(r) {
				return fmt.Errorf("bad token")
			}
			if !exists(room) {
				return fmt.Errorf("no such incident")
			}
			// The join is legitimate: make sure the room starts from what the
			// last session left behind. Seeding from the auth hook means it
			// happens before the websocket upgrade — and so before the
			// client's handshake reads the room's state.
			return n.seed(room)
		}),
		deepws.WithRoomEviction(idle, n.persist),
	)
	return n, nil
}

// ServeHTTP hands the connection to the hub.
func (n *Notes) ServeHTTP(w http.ResponseWriter, r *http.Request) { n.hub.ServeHTTP(w, r) }

func (n *Notes) path(id string) string { return filepath.Join(n.dir, id+".notes.bin") }

// seed loads a room's persisted document, once per room lifetime. Eviction
// clears the mark, so the next first-joiner seeds again.
func (n *Notes) seed(id string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.seeded[id] {
		return nil
	}
	data, err := os.ReadFile(n.path(id))
	if err != nil {
		if os.IsNotExist(err) {
			n.seeded[id] = true
			return nil
		}
		return err
	}
	var u crdt.Update
	if err := u.UnmarshalBinary(data); err != nil {
		return fmt.Errorf("corrupt notes for %s: %w", id, err)
	}
	n.hub.Room(id, func(doc *crdt.Document) { doc.Apply(u) })
	n.seeded[id] = true
	return nil
}

// persist is the eviction hook: the room is gone from the hub, its document
// handed here to survive on disk.
func (n *Notes) persist(id string, doc *crdt.Document) {
	n.write(id, doc)
	n.mu.Lock()
	delete(n.seeded, id)
	n.mu.Unlock()
}

// write saves one document. A full-state update — Since(nothing) — is the
// document in its own wire form, ready to Apply into a fresh room.
func (n *Notes) write(id string, doc *crdt.Document) {
	u := doc.Since(crdt.StateVector{})
	data, err := u.MarshalBinary()
	if err != nil {
		return
	}
	_ = os.WriteFile(n.path(id), data, 0o644)
}

// Persist snapshots every live room to disk without evicting anyone — the
// graceful-shutdown path, so notes typed minutes ago do not ride on the
// eviction timer to survive a restart.
func (n *Notes) Persist() {
	n.mu.Lock()
	ids := make([]string, 0, len(n.seeded))
	for id := range n.seeded {
		ids = append(ids, id)
	}
	n.mu.Unlock()
	for _, id := range ids {
		n.hub.Room(id, func(doc *crdt.Document) { n.write(id, doc) })
	}
}

// Text reads a room's current contents — for rendering an incident's
// timeline over plain HTTP without joining the room.
func (n *Notes) Text(id string) string {
	// A room that is not live right now lives on disk; read it without
	// disturbing the hub (Room would create a live room the eviction timer is
	// not watching).
	n.mu.Lock()
	live := n.seeded[id]
	n.mu.Unlock()
	if !live {
		data, err := os.ReadFile(n.path(id))
		if err != nil {
			return ""
		}
		var u crdt.Update
		if err := u.UnmarshalBinary(data); err != nil {
			return ""
		}
		doc := crdt.NewDocument(hlc.NewClock("reader"))
		doc.Apply(u)
		return doc.String()
	}
	var text string
	n.hub.Room(id, func(doc *crdt.Document) { text = doc.String() })
	return text
}
