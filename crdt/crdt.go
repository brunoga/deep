package crdt

import (
	"encoding/json"
	"sync"

	"github.com/brunoga/deep/v2"
	"github.com/brunoga/deep/v2/crdt/hlc"
	crdtresolver "github.com/brunoga/deep/v2/resolvers/crdt"
)

// CRDT represents a Conflict-free Replicated Data Type wrapper around type T.
type CRDT[T any] struct {
	mu         sync.RWMutex
	Value      T
	Clocks     map[string]hlc.HLC
	Tombstones map[string]hlc.HLC
	NodeID     string
	Clock      *hlc.Clock
}

// Delta represents a set of changes with a causal timestamp.
type Delta[T any] struct {
	Patch     deep.Patch[T] `json:"p"`
	Timestamp hlc.HLC       `json:"t"`
}

// NewCRDT creates a new CRDT wrapper.
func NewCRDT[T any](initial T, nodeID string) *CRDT[T] {
	return &CRDT[T]{
		Value:      initial,
		Clocks:     make(map[string]hlc.HLC),
		Tombstones: make(map[string]hlc.HLC),
		NodeID:     nodeID,
		Clock:      hlc.NewClock(nodeID),
	}
}

// View returns a deep copy of the current value.
func (c *CRDT[T]) View() T {
	c.mu.RLock()
	defer c.mu.RUnlock()
	copied, _ := deep.Copy(c.Value)
	return copied
}

// Edit applies changes and returns a Delta.
func (c *CRDT[T]) Edit(fn func(*T)) Delta[T] {
	c.mu.Lock()
	defer c.mu.Unlock()

	workingCopy, _ := deep.Copy(c.Value)
	fn(&workingCopy)

	patch := deep.Diff(c.Value, workingCopy)
	if patch == nil {
		return Delta[T]{}
	}

	now := c.Clock.Now()
	c.updateMetadataLocked(patch, now)

	c.Value = workingCopy

	return Delta[T]{
		Patch:     patch,
		Timestamp: now,
	}
}

// CreateDelta takes an existing patch, applies it to the local value,
// updates local metadata, and returns a Delta. Use this if you have
// already generated a patch manually.
func (c *CRDT[T]) CreateDelta(patch deep.Patch[T]) Delta[T] {
	if patch == nil {
		return Delta[T]{}
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.Clock.Now()
	c.updateMetadataLocked(patch, now)

	patch.Apply(&c.Value)

	return Delta[T]{
		Patch:     patch,
		Timestamp: now,
	}
}

func (c *CRDT[T]) updateMetadataLocked(patch deep.Patch[T], ts hlc.HLC) {
	patch.Walk(func(path string, op deep.OpKind, old, new any) error {
		if op == deep.OpRemove {
			c.Tombstones[path] = ts
		} else {
			c.Clocks[path] = ts
		}
		return nil
	})
}

// ApplyDelta applies a delta using LWW resolution.
func (c *CRDT[T]) ApplyDelta(delta Delta[T]) bool {
	if delta.Patch == nil {
		return false
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.Clock.Update(delta.Timestamp)

	resolver := &crdtresolver.LWWResolver{
		Clocks:     c.Clocks,
		Tombstones: c.Tombstones,
		OpTime:     delta.Timestamp,
	}

	if err := delta.Patch.ApplyResolved(&c.Value, resolver); err != nil {
		return false
	}

	return true
}

// Merge merges another state.
func (c *CRDT[T]) Merge(other *CRDT[T]) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Update local clock
	for _, h := range other.Clocks {
		c.Clock.Update(h)
	}
	for _, h := range other.Tombstones {
		c.Clock.Update(h)
	}

	patch := deep.Diff(c.Value, other.Value)
	if patch == nil {
		c.mergeMeta(other)
		return false
	}

	// State-based Resolver
	resolver := &crdtresolver.StateResolver{
		LocalClocks:      c.Clocks,
		LocalTombstones:  c.Tombstones,
		RemoteClocks:     other.Clocks,
		RemoteTombstones: other.Tombstones,
	}

	if err := patch.ApplyResolved(&c.Value, resolver); err != nil {
		return false
	}

	c.mergeMeta(other)
	return true
}

func (c *CRDT[T]) mergeMeta(other *CRDT[T]) {
	for k, v := range other.Clocks {
		if existing, ok := c.Clocks[k]; !ok || v.After(existing) {
			c.Clocks[k] = v
		}
	}
	for k, v := range other.Tombstones {
		if existing, ok := c.Tombstones[k]; !ok || v.After(existing) {
			c.Tombstones[k] = v
		}
	}
}

func (c *CRDT[T]) MarshalJSON() ([]byte, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return json.Marshal(map[string]any{
		"value":      c.Value,
		"clocks":     c.Clocks,
		"tombstones": c.Tombstones,
		"nodeID":     c.NodeID,
		"latest":     c.Clock.Latest,
	})
}

func (c *CRDT[T]) UnmarshalJSON(data []byte) error {
	var m struct {
		Value      T                  `json:"value"`
		Clocks     map[string]hlc.HLC `json:"clocks"`
		Tombstones map[string]hlc.HLC `json:"tombstones"`
		NodeID     string             `json:"nodeID"`
		Latest     hlc.HLC            `json:"latest"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	c.Value = m.Value
	c.Clocks = m.Clocks
	c.Tombstones = m.Tombstones
	c.NodeID = m.NodeID
	c.Clock = hlc.NewClock(m.NodeID)
	c.Clock.Latest = m.Latest
	return nil
}
