package crdt

// Map is a distributed LWW key-value map CRDT built on top of [CRDT].
//
// Concurrent writes to the same key are resolved by Last-Write-Wins: the write
// with the strictly higher HLC timestamp wins. Deletions remove the key from
// the map and record a tombstone timestamp, so a delete with a newer timestamp
// wins over an older set, and a set with a newer timestamp wins over an older
// delete.
type Map[K comparable, V any] struct {
	inner *CRDT[map[K]V]
}

// NewMap returns an empty Map CRDT bound to the given node ID.
func NewMap[K comparable, V any](nodeID string) *Map[K, V] {
	return &Map[K, V]{
		inner: NewCRDT(make(map[K]V), nodeID),
	}
}

// NodeID returns the unique identifier for this Map instance.
func (m *Map[K, V]) NodeID() string { return m.inner.NodeID() }

// Set sets key to value.
func (m *Map[K, V]) Set(key K, value V) {
	m.inner.Edit(func(mp *map[K]V) {
		(*mp)[key] = value
	})
}

// Delete removes key from the map. It is a no-op if the key does not exist.
func (m *Map[K, V]) Delete(key K) {
	m.inner.Edit(func(mp *map[K]V) {
		delete(*mp, key)
	})
}

// Get returns the value for key and true if the key exists.
// It returns the zero value and false otherwise.
func (m *Map[K, V]) Get(key K) (V, bool) {
	state := m.inner.View()
	v, ok := state[key]
	return v, ok
}

// Contains reports whether key exists in the map.
func (m *Map[K, V]) Contains(key K) bool {
	_, ok := m.Get(key)
	return ok
}

// Keys returns a slice of all live keys. The order is non-deterministic.
func (m *Map[K, V]) Keys() []K {
	state := m.inner.View()
	keys := make([]K, 0, len(state))
	for k := range state {
		keys = append(keys, k)
	}
	return keys
}

// Len returns the number of entries in the map.
func (m *Map[K, V]) Len() int {
	return len(m.inner.View())
}

// Merge performs a full state-based LWW merge with another Map node.
// Returns true if the local state changed.
func (m *Map[K, V]) Merge(other *Map[K, V]) bool {
	return m.inner.Merge(other.inner)
}
