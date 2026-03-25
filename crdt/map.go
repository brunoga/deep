package crdt

import (
	"sync"

	"github.com/brunoga/deep/v5/crdt/hlc"
)

// mapEntry holds a value at one key, with LWW metadata.
type mapEntry[V any] struct {
	Ts      hlc.HLC
	Value   V
	Deleted bool
}

// Map is a distributed LWW key-value map CRDT.
//
// Concurrent writes to the same key are resolved by Last-Write-Wins: the entry
// with the strictly higher HLC timestamp is kept. Deletes are represented as
// tombstones so that a delete with a newer timestamp wins over an older set,
// and a set with a newer timestamp wins over an older delete.
type Map[K comparable, V any] struct {
	mu      sync.RWMutex
	nodeID  string
	clock   *hlc.Clock
	entries map[K]mapEntry[V]
}

// NewMap returns an empty Map CRDT bound to the given node ID.
func NewMap[K comparable, V any](nodeID string) *Map[K, V] {
	return &Map[K, V]{
		nodeID:  nodeID,
		clock:   hlc.NewClock(nodeID),
		entries: make(map[K]mapEntry[V]),
	}
}

// NodeID returns the unique identifier for this Map instance.
func (m *Map[K, V]) NodeID() string { return m.nodeID }

// Set sets key to value with the current HLC timestamp.
func (m *Map[K, V]) Set(key K, value V) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ts := m.clock.Now()
	m.entries[key] = mapEntry[V]{Ts: ts, Value: value, Deleted: false}
}

// Delete tombstones key with the current HLC timestamp. It is a no-op if the
// key does not exist.
func (m *Map[K, V]) Delete(key K) {
	m.mu.Lock()
	defer m.mu.Unlock()
	existing, ok := m.entries[key]
	if !ok {
		return
	}
	ts := m.clock.Now()
	existing.Ts = ts
	existing.Deleted = true
	m.entries[key] = existing
}

// Get returns the value for key and true if the key exists and is not deleted.
// It returns the zero value and false otherwise.
func (m *Map[K, V]) Get(key K) (V, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.entries[key]
	if !ok || e.Deleted {
		var zero V
		return zero, false
	}
	return e.Value, true
}

// Contains reports whether key exists and is not deleted.
func (m *Map[K, V]) Contains(key K) bool {
	_, ok := m.Get(key)
	return ok
}

// Keys returns a slice of all live (non-deleted) keys. The order is
// non-deterministic.
func (m *Map[K, V]) Keys() []K {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]K, 0)
	for k, e := range m.entries {
		if !e.Deleted {
			out = append(out, k)
		}
	}
	return out
}

// Len returns the number of live (non-deleted) keys.
func (m *Map[K, V]) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	n := 0
	for _, e := range m.entries {
		if !e.Deleted {
			n++
		}
	}
	return n
}

// Merge performs a full state-based LWW merge with another Map node.
//
// For each key in other: the local clock is advanced with the remote entry's
// timestamp first, then the entry with the strictly higher timestamp wins. If
// timestamps are equal the remote entry is not accepted (local wins on ties).
//
// Returns true if the local state changed.
func (m *Map[K, V]) Merge(other *Map[K, V]) bool {
	other.mu.RLock()
	defer other.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()

	changed := false
	for k, remote := range other.entries {
		// Advance local clock to maintain causality.
		m.clock.Update(remote.Ts)

		local, exists := m.entries[k]
		if !exists || remote.Ts.After(local.Ts) {
			m.entries[k] = remote
			changed = true
		}
	}
	return changed
}
