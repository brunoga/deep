package server

import (
	"fmt"
	"log/slog"
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
// seeding a room from disk, and persisting it back.
//
// Two CRDT properties carry the correctness here, where a lesser data type
// would need locks spanning the hub and the disk:
//
//   - Seeding happens on every join, not once per room lifetime: applying an
//     update a document already holds is a no-op, so replaying the file into
//     a room that was already seeded costs a file read and changes nothing —
//     and a room that raced an eviction gets its history back on the next
//     join instead of silently starting empty.
//   - Writes merge with what is on disk rather than overwriting it: a room
//     that missed a seed can never clobber the file, because the file's own
//     runs are folded in before the write.
type Notes struct {
	hub *deepws.Hub
	dir string

	mu sync.Mutex
	// live marks rooms with a session since the last eviction — which rooms
	// Text reads from the hub and Persist snapshots on shutdown.
	live map[string]bool
}

// NewNotes builds the notes hub. authorized gates each join (the API's token
// check, shared so both transports honour the same token); exists confirms
// the room names a real incident. idle is how long an empty room lingers
// before its document is persisted and dropped.
func NewNotes(dir string, idle time.Duration, authorized func(*http.Request) bool, exists func(id string) bool) (*Notes, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	n := &Notes{dir: dir, live: map[string]bool{}}
	n.hub = deepws.NewHub(
		deepws.WithAuth(func(r *http.Request, room string) error {
			if !authorized(r) {
				return fmt.Errorf("bad token")
			}
			if !exists(room) {
				return fmt.Errorf("no such incident")
			}
			// The join is legitimate: fold the persisted notes into the room.
			// From the auth hook, this runs before the websocket upgrade —
			// and so before the client's handshake reads the room's state.
			return n.seed(room)
		}),
		deepws.WithRoomEviction(idle, n.persist),
	)
	return n, nil
}

// ServeHTTP hands the connection to the hub.
func (n *Notes) ServeHTTP(w http.ResponseWriter, r *http.Request) { n.hub.ServeHTTP(w, r) }

func (n *Notes) path(id string) string { return filepath.Join(n.dir, id+".notes.bin") }

// load reads a persisted document's full-state update. A file that does not
// exist is an empty history; a file that cannot be decoded is quarantined —
// renamed aside and reported — rather than allowed to lock every responder
// out of the room forever.
func (n *Notes) load(id string) (crdt.Update, bool) {
	data, err := os.ReadFile(n.path(id))
	if err != nil {
		return crdt.Update{}, false
	}
	var u crdt.Update
	if err := u.UnmarshalBinary(data); err != nil {
		quarantine := fmt.Sprintf("%s.corrupt-%d", n.path(id), time.Now().Unix())
		_ = os.Rename(n.path(id), quarantine)
		slog.Error("quarantined corrupt notes file", "incident", id, "moved_to", quarantine, "err", err)
		return crdt.Update{}, false
	}
	return u, true
}

// seed folds the persisted notes into the room. Idempotent by the CRDT's
// nature, so it runs on every join.
func (n *Notes) seed(id string) error {
	u, ok := n.load(id)
	n.mu.Lock()
	n.live[id] = true
	n.mu.Unlock()
	if ok {
		n.hub.Room(id, func(doc *crdt.Document) { doc.Apply(u) })
	}
	return nil
}

// persist is the eviction hook: the room is gone from the hub, its document
// handed here to survive on disk.
func (n *Notes) persist(id string, doc *crdt.Document) {
	n.write(id, doc)
	n.mu.Lock()
	delete(n.live, id)
	n.mu.Unlock()
}

// write saves one document, merged over whatever the file already holds and
// renamed into place — so a write can lose nothing and a crash mid-write
// corrupts nothing.
func (n *Notes) write(id string, doc *crdt.Document) {
	merged := crdt.NewDocument(hlc.NewClock("notes:" + id))
	if u, ok := n.load(id); ok {
		merged.Apply(u)
	}
	merged.Apply(doc.Since(crdt.StateVector{}))

	data, err := merged.Since(crdt.StateVector{}).MarshalBinary()
	if err != nil {
		slog.Error("encoding notes", "incident", id, "err", err)
		return
	}
	tmp := n.path(id) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		slog.Error("writing notes", "incident", id, "err", err)
		return
	}
	if err := os.Rename(tmp, n.path(id)); err != nil {
		slog.Error("writing notes", "incident", id, "err", err)
	}
}

// Persist snapshots every live room to disk without evicting anyone — the
// graceful-shutdown path, so notes typed minutes ago do not ride on the
// eviction timer to survive a restart. Racing the eviction timer is harmless:
// both paths merge with the file before writing.
func (n *Notes) Persist() {
	n.mu.Lock()
	ids := make([]string, 0, len(n.live))
	for id := range n.live {
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
	n.mu.Lock()
	isLive := n.live[id]
	n.mu.Unlock()
	if !isLive {
		// Not live: the truth is on disk, and reading it there avoids
		// creating a hub room the eviction timer is not watching.
		u, ok := n.load(id)
		if !ok {
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
