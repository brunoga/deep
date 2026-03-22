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
// [CRDT] via a custom diff/apply strategy registered at init time.
package crdt

import (
	"encoding/json"
	"log/slog"
	"sync"

	"github.com/brunoga/deep/v5/crdt/hlc"
	"github.com/brunoga/deep/v5/internal/cond"
	"github.com/brunoga/deep/v5/internal/engine"
	crdtresolver "github.com/brunoga/deep/v5/internal/resolvers/crdt"
)

func init() {
	engine.RegisterCustomPatch(&textPatch{})
	engine.RegisterCustomDiff[Text](func(a, b Text) (engine.Patch[Text], error) {
		// Optimization: if both are same, return nil
		if len(a) == len(b) {
			same := true
			for i := range a {
				if a[i].ID != b[i].ID || a[i].Value != b[i].Value || a[i].Deleted != b[i].Deleted {
					same = false
					break
				}
			}
			if same {
				return nil, nil
			}
		}
		return &textPatch{Runs: b}, nil
	})
}

// textPatch is a specialized patch for Text CRDT.
type textPatch struct {
	Runs Text
}

func (p *textPatch) PatchKind() string { return "text" }

func (p *textPatch) Apply(v *Text) {
	*v = MergeTextRuns(*v, p.Runs)
}

func (p *textPatch) ApplyChecked(v *Text) error {
	p.Apply(v)
	return nil
}

func (p *textPatch) ApplyResolved(v *Text, r engine.ConflictResolver) error {
	*v = MergeTextRuns(*v, p.Runs)
	return nil
}

func (p *textPatch) Walk(fn func(path string, op engine.OpKind, old, new any) error) error {
	return fn("", engine.OpReplace, nil, p.Runs)
}

func (p *textPatch) WithCondition(c cond.Condition[Text]) engine.Patch[Text] { return p }
func (p *textPatch) WithStrict(strict bool) engine.Patch[Text]               { return p }
func (p *textPatch) Reverse() engine.Patch[Text]                             { return p }
func (p *textPatch) ToJSONPatch() ([]byte, error)                            { return nil, nil }
func (p *textPatch) Summary() string                                         { return "Text update" }
func (p *textPatch) String() string                                          { return "TextPatch" }

func (p *textPatch) MarshalSerializable() (any, error) {
	return engine.PatchToSerializable(p)
}

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
	patch     engine.Patch[T]
	Timestamp hlc.HLC `json:"t"`
}

func (d Delta[T]) MarshalJSON() ([]byte, error) {
	patchBytes, err := json.Marshal(d.patch)
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		Patch     json.RawMessage `json:"p"`
		Timestamp hlc.HLC         `json:"t"`
	}{
		Patch:     patchBytes,
		Timestamp: d.Timestamp,
	})
}

func (d *Delta[T]) UnmarshalJSON(data []byte) error {
	var m struct {
		Patch     json.RawMessage `json:"p"`
		Timestamp hlc.HLC         `json:"t"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	d.Timestamp = m.Timestamp
	if len(m.Patch) > 0 && string(m.Patch) != "null" {
		p := engine.NewPatch[T]()
		if err := json.Unmarshal(m.Patch, p); err != nil {
			return err
		}
		d.patch = p
	}
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
func (c *CRDT[T]) NodeID() string {
	return c.nodeID
}

// Clock returns the internal hybrid logical clock.
func (c *CRDT[T]) Clock() *hlc.Clock {
	return c.clock
}

// View returns a deep copy of the current value.
// If the copy fails (e.g. the value contains an unsupported kind), the zero
// value for T is returned and the error is logged via slog.Default().
func (c *CRDT[T]) View() T {
	c.mu.RLock()
	defer c.mu.RUnlock()
	copied, err := engine.Copy(c.value)
	if err != nil {
		slog.Default().Error("crdt: View copy failed", "err", err)
		var zero T
		return zero
	}
	return copied
}

// Edit applies fn to a copy of the current value, computes a delta, advances
// the local clock, and returns the delta for distribution to peers. Returns an
// empty Delta if the edit produces no changes.
func (c *CRDT[T]) Edit(fn func(*T)) Delta[T] {
	c.mu.Lock()
	defer c.mu.Unlock()

	workingCopy, err := engine.Copy(c.value)
	if err != nil {
		slog.Default().Error("crdt: Edit copy failed", "err", err)
		return Delta[T]{}
	}
	fn(&workingCopy)

	patch, err := engine.Diff(c.value, workingCopy)
	if err != nil {
		slog.Default().Error("crdt: Edit diff failed", "err", err)
		return Delta[T]{}
	}
	if patch == nil {
		return Delta[T]{}
	}

	now := c.clock.Now()
	if err := c.updateMetadataLocked(patch, now); err != nil {
		slog.Default().Error("crdt: Edit metadata update failed", "err", err)
		return Delta[T]{}
	}

	c.value = workingCopy

	return Delta[T]{
		patch:     patch,
		Timestamp: now,
	}
}

// createDelta wraps an existing internal patch into a Delta, applies it
// locally, and advances the clock. Used internally by tests.
func (c *CRDT[T]) createDelta(patch engine.Patch[T]) Delta[T] {
	if patch == nil {
		return Delta[T]{}
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.clock.Now()
	if err := c.updateMetadataLocked(patch, now); err != nil {
		slog.Default().Error("crdt: createDelta metadata update failed", "err", err)
		return Delta[T]{}
	}

	patch.Apply(&c.value)

	return Delta[T]{
		patch:     patch,
		Timestamp: now,
	}
}

func (c *CRDT[T]) updateMetadataLocked(patch engine.Patch[T], ts hlc.HLC) error {
	return patch.Walk(func(path string, op engine.OpKind, old, new any) error {
		if op == engine.OpRemove {
			c.tombstones[path] = ts
		} else {
			c.clocks[path] = ts
		}
		return nil
	})
}

// ApplyDelta applies a delta using LWW resolution.
func (c *CRDT[T]) ApplyDelta(delta Delta[T]) bool {
	if delta.patch == nil {
		return false
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.clock.Update(delta.Timestamp)

	resolver := &crdtresolver.LWWResolver{
		Clocks:     c.clocks,
		Tombstones: c.tombstones,
		OpTime:     delta.Timestamp,
	}

	if err := delta.patch.ApplyResolved(&c.value, resolver); err != nil {
		return false
	}

	return true
}

// Merge merges another state.
func (c *CRDT[T]) Merge(other *CRDT[T]) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Update local clock
	for _, h := range other.clocks {
		c.clock.Update(h)
	}
	for _, h := range other.tombstones {
		c.clock.Update(h)
	}

	patch, err := engine.Diff(c.value, other.value)
	if err != nil || patch == nil {
		c.mergeMeta(other)
		return false
	}

	// State-based Resolver
	if v, ok := any(c.value).(Text); ok {
		// Special case for Text
		otherV := any(other.value).(Text)
		c.value = any(MergeTextRuns(v, otherV)).(T)
		c.mergeMeta(other)
		return true
	}

	resolver := &crdtresolver.StateResolver{
		LocalClocks:      c.clocks,
		LocalTombstones:  c.tombstones,
		RemoteClocks:     other.clocks,
		RemoteTombstones: other.tombstones,
	}

	if err := patch.ApplyResolved(&c.value, resolver); err != nil {
		return false
	}

	c.mergeMeta(other)
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
