package crdt

import (
	"sync"

	"github.com/brunoga/deep/v5/crdt/hlc"
)

// setEntry is one tagged add-operation in the OR-Set log.
// The ID field is the unique HLC-based tag assigned at Add time; it acts as
// the unique identity for each add operation, giving the Add-Wins property
// during merge.
type setEntry[T any] struct {
	ID      hlc.HLC `json:"id"`
	Elem    T       `json:"e"`
	Deleted bool    `json:"d,omitempty"`
}

// Set is an Add-Wins Observed-Remove Set (OR-Set) CRDT.
//
// Each Add creates a uniquely-tagged entry using the node's HLC. Remove only
// tombstones entries that exist at call time; a concurrent Add from another
// node produces a different tag, so after Merge the element is still present
// (add wins over remove).
type Set[T comparable] struct {
	mu      sync.RWMutex
	entries map[string]setEntry[T] // keyed by HLC.String()
	nodeID  string
	clock   *hlc.Clock
}

// NewSet returns an empty Set CRDT bound to the given node ID.
func NewSet[T comparable](nodeID string) *Set[T] {
	return &Set[T]{
		entries: make(map[string]setEntry[T]),
		nodeID:  nodeID,
		clock:   hlc.NewClock(nodeID),
	}
}

// Add appends a new uniquely-tagged entry for elem.
func (s *Set[T]) Add(elem T) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.clock.Now()
	s.entries[id.String()] = setEntry[T]{ID: id, Elem: elem}
}

// Remove marks all non-deleted entries whose Elem equals elem as deleted.
// Only entries visible at call time are tombstoned; concurrent adds on other
// nodes create entries with different tags that this Remove never sees.
func (s *Set[T]) Remove(elem T) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, e := range s.entries {
		if !e.Deleted && e.Elem == elem {
			e.Deleted = true
			s.entries[k] = e
		}
	}
}

// Contains reports whether elem has at least one live (non-deleted) entry.
func (s *Set[T]) Contains(elem T) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, e := range s.entries {
		if !e.Deleted && e.Elem == elem {
			return true
		}
	}
	return false
}

// Items returns a deduplicated slice of all live elements.
func (s *Set[T]) Items() []T {
	s.mu.RLock()
	defer s.mu.RUnlock()
	seen := make(map[T]struct{})
	for _, e := range s.entries {
		if !e.Deleted {
			seen[e.Elem] = struct{}{}
		}
	}
	out := make([]T, 0, len(seen))
	for elem := range seen {
		out = append(out, elem)
	}
	return out
}

// Len returns the number of distinct live elements.
func (s *Set[T]) Len() int {
	return len(s.Items())
}

// Merge performs a full state-based OR-Set merge with another Set node.
//
// For each entry in other:
//   - If the entry is absent locally, add it (union semantics).
//   - If the entry is present locally and remote has Deleted=true, mark local
//     as deleted too (tombstone propagation).
//   - If local already has the entry as deleted, it stays deleted.
//
// A remote live entry always wins over a local absence (add-wins property):
// concurrent removes on different nodes only tombstone entries they knew about
// at remove time; new entries created on a different node are never affected.
//
// Returns true if the local state changed.
func (s *Set[T]) Merge(other *Set[T]) bool {
	other.mu.RLock()
	defer other.mu.RUnlock()

	s.mu.Lock()
	defer s.mu.Unlock()

	changed := false
	for k, remote := range other.entries {
		// Advance local clock to maintain causality.
		s.clock.Update(remote.ID)

		local, exists := s.entries[k]
		if !exists {
			// New entry from remote — add it (preserving its deleted state).
			s.entries[k] = remote
			changed = true
			continue
		}
		// Entry exists locally. Apply tombstone if remote marked it deleted and
		// local hasn't yet.
		if remote.Deleted && !local.Deleted {
			local.Deleted = true
			s.entries[k] = local
			changed = true
		}
	}
	return changed
}

// NodeID returns the unique identifier for this Set instance.
func (s *Set[T]) NodeID() string {
	return s.nodeID
}
