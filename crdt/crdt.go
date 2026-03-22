// Package crdt provides Conflict-free Replicated Data Types (CRDTs) built on
// top of the deep patch engine.
//
// The central type is [CRDT], a concurrency-safe wrapper around any value of
// type T. It tracks causal history using a per-field Hybrid Logical Clock (HLC)
// and resolves concurrent edits with Last-Write-Wins (LWW) semantics.
//
// # Basic workflow
//
//  1. Create nodes: nodeA := crdt.NewCRDT(initial, "node-a")
//  2. Edit locally: delta := nodeA.Edit(func(v *T) { v.Field = newVal })
//  3. Distribute: send delta (JSON-serializable) to peers
//  4. Apply remotely: nodeB.ApplyDelta(delta)
//
// For full-state synchronization between two nodes use [CRDT.Merge].
//
// # Text CRDT
//
// [Text] is a convergent, ordered sequence of [TextRun] segments. It supports
// concurrent insertions and deletions across nodes and is integrated with
// [CRDT] directly — no separate registration required.
package crdt

import (
	"encoding/json"
	"log/slog"
	"sync"

	deep "github.com/brunoga/deep/v5"
	"github.com/brunoga/deep/v5/crdt/hlc"
)

// CRDT represents a Conflict-free Replicated Data Type wrapper around type T.
type CRDT[T any] struct {
	mu         sync.RWMutex
	value      T
	clocks     map[string]hlc.HLC
	tombstones map[string]hlc.HLC
	nodeID     string
	clock      *hlc.Clock
}

// Delta represents a set of changes with a causal timestamp.
// Obtain a Delta via CRDT.Edit; apply it on remote nodes via CRDT.ApplyDelta.
type Delta[T any] struct {
	patch     deep.Patch[T]
	Timestamp hlc.HLC `json:"t"`
}

func (d Delta[T]) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Patch     deep.Patch[T] `json:"p"`
		Timestamp hlc.HLC       `json:"t"`
	}{d.patch, d.Timestamp})
}

func (d *Delta[T]) UnmarshalJSON(data []byte) error {
	var m struct {
		Patch     deep.Patch[T] `json:"p"`
		Timestamp hlc.HLC       `json:"t"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	d.patch = m.Patch
	d.Timestamp = m.Timestamp
	return nil
}

// NewCRDT creates a new CRDT wrapper.
func NewCRDT[T any](initial T, nodeID string) *CRDT[T] {
	return &CRDT[T]{
		value:      initial,
		clocks:     make(map[string]hlc.HLC),
		tombstones: make(map[string]hlc.HLC),
		nodeID:     nodeID,
		clock:      hlc.NewClock(nodeID),
	}
}

// NodeID returns the unique identifier for this CRDT instance.
func (c *CRDT[T]) NodeID() string { return c.nodeID }

// Clock returns the internal hybrid logical clock.
func (c *CRDT[T]) Clock() *hlc.Clock { return c.clock }

// View returns a deep copy of the current value.
func (c *CRDT[T]) View() T {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return deep.Clone(c.value)
}

// Edit applies fn to a copy of the current value, computes a delta, advances
// the local clock, and returns the delta for distribution to peers. Returns an
// empty Delta if the edit produces no changes.
func (c *CRDT[T]) Edit(fn func(*T)) Delta[T] {
	c.mu.Lock()
	defer c.mu.Unlock()

	workingCopy := deep.Clone(c.value)
	fn(&workingCopy)

	patch, err := deep.Diff(c.value, workingCopy)
	if err != nil {
		slog.Default().Error("crdt: Edit diff failed", "err", err)
		return Delta[T]{}
	}
	if patch.IsEmpty() {
		return Delta[T]{}
	}

	now := c.clock.Now()
	c.updateMetadataLocked(patch, now)
	c.value = workingCopy

	return Delta[T]{patch: patch, Timestamp: now}
}

func (c *CRDT[T]) updateMetadataLocked(patch deep.Patch[T], ts hlc.HLC) {
	for _, op := range patch.Operations {
		if op.Kind == deep.OpRemove {
			c.tombstones[op.Path] = ts
		} else {
			c.clocks[op.Path] = ts
		}
	}
}

// ApplyDelta applies a delta from a remote peer using Last-Write-Wins resolution.
// Returns true if any operations were accepted.
func (c *CRDT[T]) ApplyDelta(delta Delta[T]) bool {
	if delta.patch.IsEmpty() {
		return false
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.clock.Update(delta.Timestamp)

	// Text is a convergent CRDT with its own merge semantics — always apply,
	// skipping the LWW clock filter that would discard concurrent inserts/deletes.
	if _, ok := any(c.value).(Text); ok {
		return deep.Apply(&c.value, delta.patch) == nil
	}

	var filtered []deep.Operation
	for _, op := range delta.patch.Operations {
		opTime := delta.Timestamp

		// LWW: effective local time is the max of the write clock and tombstone.
		lTime := c.clocks[op.Path]
		if lTomb, ok := c.tombstones[op.Path]; ok && lTomb.After(lTime) {
			lTime = lTomb
		}

		if !opTime.After(lTime) {
			continue // local is newer or equal — skip
		}

		filtered = append(filtered, op)
		if op.Kind == deep.OpRemove {
			c.tombstones[op.Path] = opTime
		} else {
			c.clocks[op.Path] = opTime
		}
	}

	if len(filtered) == 0 {
		return false
	}
	return deep.Apply(&c.value, deep.Patch[T]{Operations: filtered}) == nil
}

// Merge performs a full state-based merge with another CRDT node.
// For each changed field the node with the strictly newer effective timestamp
// (max of write clock and tombstone) wins.
func (c *CRDT[T]) Merge(other *CRDT[T]) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, h := range other.clocks {
		c.clock.Update(h)
	}
	for _, h := range other.tombstones {
		c.clock.Update(h)
	}

	// Text has its own convergent merge that doesn't rely on per-field clocks.
	if v, ok := any(c.value).(Text); ok {
		otherV := any(other.value).(Text)
		c.value = any(MergeTextRuns(v, otherV)).(T)
		c.mergeMeta(other)
		return true
	}

	patch, err := deep.Diff(c.value, other.value)
	if err != nil || patch.IsEmpty() {
		c.mergeMeta(other)
		return false
	}

	// State-based LWW: apply each op only if the remote effective time is
	// strictly newer than the local effective time for that path.
	var filtered []deep.Operation
	for _, op := range patch.Operations {
		rClock, hasRC := other.clocks[op.Path]
		rTomb, hasRT := other.tombstones[op.Path]

		// If remote has no timing info for this path, local wins by default.
		if !hasRC && !hasRT {
			continue
		}

		lTime := c.clocks[op.Path]
		if lTomb, ok := c.tombstones[op.Path]; ok && lTomb.After(lTime) {
			lTime = lTomb
		}

		rTime := rClock
		if hasRT && rTomb.After(rTime) {
			rTime = rTomb
		}

		if !rTime.After(lTime) {
			continue // local is newer or equal
		}

		filtered = append(filtered, op)
		if op.Kind == deep.OpRemove {
			if hasRT {
				c.tombstones[op.Path] = rTomb
			}
		} else {
			if hasRC {
				c.clocks[op.Path] = rClock
			}
		}
	}

	c.mergeMeta(other)

	if len(filtered) == 0 {
		return false
	}
	_ = deep.Apply(&c.value, deep.Patch[T]{Operations: filtered})
	return true
}

func (c *CRDT[T]) mergeMeta(other *CRDT[T]) {
	for k, v := range other.clocks {
		if existing, ok := c.clocks[k]; !ok || v.After(existing) {
			c.clocks[k] = v
		}
	}
	for k, v := range other.tombstones {
		if existing, ok := c.tombstones[k]; !ok || v.After(existing) {
			c.tombstones[k] = v
		}
	}
}

func (c *CRDT[T]) MarshalJSON() ([]byte, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return json.Marshal(map[string]any{
		"value":      c.value,
		"clocks":     c.clocks,
		"tombstones": c.tombstones,
		"nodeID":     c.nodeID,
		"latest":     c.clock.Latest,
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
	c.value = m.Value
	c.clocks = m.Clocks
	c.tombstones = m.Tombstones
	c.nodeID = m.NodeID
	c.clock = hlc.NewClock(m.NodeID)
	c.clock.Latest = m.Latest
	return nil
}
