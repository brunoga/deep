package crdt

// setEntry holds an element and its live/deleted status for one Add operation.
// The unique HLC-based tag for this add is stored as the map key in setInner,
// not as a field here — it survives serialisation via the map key string.
type setEntry[T any] struct {
	Elem    T    `json:"e"`
	Deleted bool `json:"d,omitempty"`
}

// setInner is the state type managed by the underlying CRDT.
type setInner[T comparable] struct {
	Entries map[string]setEntry[T] `json:"entries"`
}

// Set is an Add-Wins Observed-Remove Set (OR-Set) CRDT built on top of [CRDT].
//
// Each Add creates a uniquely-tagged entry using the node's HLC. Remove only
// tombstones entries that exist at call time; a concurrent Add from another
// node produces a different tag, so after Merge the element is still present
// (add wins over remove).
type Set[T comparable] struct {
	inner *CRDT[setInner[T]]
}

// NewSet returns an empty Set CRDT bound to the given node ID.
func NewSet[T comparable](nodeID string) *Set[T] {
	return &Set[T]{
		inner: NewCRDT(setInner[T]{
			Entries: make(map[string]setEntry[T]),
		}, nodeID),
	}
}

// NodeID returns the unique identifier for this Set instance.
func (s *Set[T]) NodeID() string { return s.inner.NodeID() }

// Add appends a new uniquely-tagged entry for elem.
// The tag is the current HLC timestamp serialised as a string map key.
func (s *Set[T]) Add(elem T) {
	id := s.inner.Clock().Now()
	s.inner.Edit(func(si *setInner[T]) {
		si.Entries[id.String()] = setEntry[T]{Elem: elem}
	})
}

// Remove marks all non-deleted entries whose Elem equals elem as deleted.
// Only entries visible at call time are tombstoned; concurrent adds on other
// nodes create entries with different tags that this Remove never sees.
func (s *Set[T]) Remove(elem T) {
	s.inner.Edit(func(si *setInner[T]) {
		for k, e := range si.Entries {
			if !e.Deleted && e.Elem == elem {
				e.Deleted = true
				si.Entries[k] = e
			}
		}
	})
}

// Contains reports whether elem has at least one live (non-deleted) entry.
func (s *Set[T]) Contains(elem T) bool {
	state := s.inner.View()
	for _, e := range state.Entries {
		if !e.Deleted && e.Elem == elem {
			return true
		}
	}
	return false
}

// Items returns a deduplicated slice of all live elements.
func (s *Set[T]) Items() []T {
	state := s.inner.View()
	seen := make(map[T]struct{})
	for _, e := range state.Entries {
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
// Returns true if the local state changed.
func (s *Set[T]) Merge(other *Set[T]) bool {
	return s.inner.Merge(other.inner)
}
