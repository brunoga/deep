package crdt

import (
	"encoding/json"
	"fmt"
	"sort"
	"sync"

	"github.com/brunoga/deep/v3"
	"github.com/brunoga/deep/v3/crdt/hlc"
	crdtresolver "github.com/brunoga/deep/v3/resolvers/crdt"
)

func init() {
	deep.RegisterCustomPatch("text", &textPatch{})
	deep.RegisterCustomDiff[Text](nil, func(a, b Text) (deep.Patch[Text], error) {
		// Optimization: if both are same, return nil
		if len(a) == len(b) {
			same := true
			for i := range a {
				if a[i] != b[i] {
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
	*v = p.Runs.normalize()
}

func (p *textPatch) ApplyChecked(v *Text) error {
	p.Apply(v)
	return nil
}

func (p *textPatch) ApplyResolved(v *Text, r deep.ConflictResolver) error {
	*v = mergeTextRuns(*v, p.Runs)
	return nil
}

func mergeTextRuns(a, b Text) Text {
	allRuns := append(a[:0:0], a...)
	allRuns = append(allRuns, b...)

	// 1. Find all split points for each base ID (NodeID + WallTime)
	type baseID struct {
		WallTime int64
		NodeID   string
	}
	splits := make(map[baseID]map[int32]bool)

	for _, run := range allRuns {
		base := baseID{run.ID.WallTime, run.ID.NodeID}
		if splits[base] == nil {
			splits[base] = make(map[int32]bool)
		}
		splits[base][run.ID.Logical] = true
		splits[base][run.ID.Logical+int32(len(run.Value))] = true
	}

	// 2. Re-split all runs according to split points and merge into a map
	combinedMap := make(map[hlc.HLC]TextRun)
	for _, run := range allRuns {
		base := baseID{run.ID.WallTime, run.ID.NodeID}

		relevantSplits := []int32{}
		for s := range splits[base] {
			if s > run.ID.Logical && s < run.ID.Logical+int32(len(run.Value)) {
				relevantSplits = append(relevantSplits, s)
			}
		}
		sort.Slice(relevantSplits, func(i, j int) bool { return relevantSplits[i] < relevantSplits[j] })

		currentLogical := run.ID.Logical
		currentValue := run.Value
		currentPrev := run.Prev

		for _, s := range relevantSplits {
			offset := int(s - currentLogical)

			id := run.ID
			id.Logical = currentLogical

			newRun := TextRun{
				ID:      id,
				Value:   currentValue[:offset],
				Prev:    currentPrev,
				Deleted: run.Deleted,
			}
			if existing, ok := combinedMap[id]; ok {
				if newRun.Deleted {
					existing.Deleted = true
				}
				combinedMap[id] = existing
			} else {
				combinedMap[id] = newRun
			}

			currentPrev = id
			currentPrev.Logical += int32(offset - 1)
			currentValue = currentValue[offset:]
			currentLogical = s
		}

		id := run.ID
		id.Logical = currentLogical
		newRun := TextRun{
			ID:      id,
			Value:   currentValue,
			Prev:    currentPrev,
			Deleted: run.Deleted,
		}
		if existing, ok := combinedMap[id]; ok {
			if newRun.Deleted {
				existing.Deleted = true
			}
			combinedMap[id] = existing
		} else {
			combinedMap[id] = newRun
		}
	}

	// 3. Reconstruct the slice
	result := make(Text, 0, len(combinedMap))
	for _, run := range combinedMap {
		result = append(result, run)
	}

	return result.normalize()
}

func (p *textPatch) Walk(fn func(path string, op deep.OpKind, old, new any) error) error {
	return fn("", deep.OpReplace, nil, p.Runs)
}

func (p *textPatch) WithCondition(c deep.Condition[Text]) deep.Patch[Text] { return p }
func (p *textPatch) WithStrict(strict bool) deep.Patch[Text]             { return p }
func (p *textPatch) Reverse() deep.Patch[Text]                           { return p }
func (p *textPatch) ToJSONPatch() ([]byte, error)                        { return nil, nil }
func (p *textPatch) Summary() string                                     { return "Text update" }
func (p *textPatch) Release()                                            {}
func (p *textPatch) String() string                                      { return "TextPatch" }

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
		p := deep.NewPatch[T]()
		if err := json.Unmarshal(m.Patch, p); err != nil {
			return err
		}
		d.Patch = p
	}
	return nil
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
	copied, err := deep.Copy(c.Value)
	if err != nil {
		// Since View returns T, and Copy failed (which should be rare/impossible 
		// for valid T), we return the internal value directly or panic.
		// For robustness, returning zero value on error is safer than direct access.
		var zero T
		return zero
	}
	return copied
}

// Edit applies changes and returns a Delta.
func (c *CRDT[T]) Edit(fn func(*T)) Delta[T] {
	c.mu.Lock()
	defer c.mu.Unlock()

	workingCopy, err := deep.Copy(c.Value)
	if err != nil {
		return Delta[T]{}
	}
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
	err := patch.Walk(func(path string, op deep.OpKind, old, new any) error {
		if op == deep.OpRemove {
			c.Tombstones[path] = ts
		} else {
			c.Clocks[path] = ts
		}
		return nil
	})
	if err != nil {
		// In CRDT, metadata update failing is critical.
		// However, our Walk callback never returns error.
		// We'll panic if Walk fails as it indicates an internal state corruption.
		panic(fmt.Errorf("crdt metadata update failed: %w", err))
	}
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
	if _, ok := any(c.Value).(*Text); ok {
		// Special case for Text
		v := any(c.Value).(Text)
		otherV := any(other.Value).(Text)
		c.Value = any(mergeTextRuns(v, otherV)).(T)
		c.mergeMeta(other)
		return true
	}

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
