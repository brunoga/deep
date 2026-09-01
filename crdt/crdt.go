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
// # Text and sequences
//
// [Text] is a convergent, ordered sequence of [TextRun] segments. It supports
// concurrent insertions and deletions across nodes and is integrated with
// [CRDT] directly — no separate registration required.
//
// [Document] holds the same runs as a Text but keeps them indexed by position,
// so an edit costs the same whether the document holds a hundred runs or ten
// thousand. The two serialize identically and converge with each other; reach
// for a Document when the document is large or the thing being edited, and a
// Text when it is a small field among others.
//
// [List] is the same idea for elements of any type: an ordinary slice inside a
// [CRDT] is synchronized as one value, so concurrent edits resolve by
// last-write-wins, whereas insertions and deletions in a List all survive.
//
// # Watching for changes
//
// [CRDT.OnChange] reports each change as it is applied, carrying the operations
// that took effect and whether they came from a local edit, a peer's delta, or
// a merge. Only operations that survived conflict resolution are reported, so
// what arrives describes what actually happened — enough to redraw the parts of
// a view that moved rather than rebuilding it.
//
// # Syncing
//
// [CRDT.ApplyDelta] carries a change from one replica to another, and
// [CRDT.Merge] reconciles two replicas wholesale.
//
// A [Document] can also sync without exchanging whole documents:
// [Document.StateVector] says how much of each writer's output a replica holds,
// [Document.Since] returns what a peer holding that is missing, and
// [Document.Apply] integrates the result. The cost is then the size of the
// change rather than the size of the document.
//
// # Reclaiming history
//
// A replica remembers when each path was written and removed so that it can
// recognize a stale update, and a sequence keeps deleted elements as tombstones
// so a concurrent insertion still has an anchor. Neither shrinks on its own.
// [CRDT.Compact] discards both, as far as a watermark the caller supplies —
// dropping the record is only safe for changes every replica has seen. Types
// that hold history of their own take part by implementing [Compactable].
//
// # Collections
//
// How a collection merges depends on how its elements are addressed:
//
//   - Map entries are addressed by key, so concurrent writes to different keys
//     both survive and a write to one key never disturbs another.
//   - A slice whose element type carries a deep:"key" tag is addressed by that
//     key, so concurrent edits to different elements both survive, and an
//     element's fields merge independently. Element order is not part of the
//     synchronized state — Diff emits nothing when a keyed slice is merely
//     reordered — so replicas keep these slices in key order. Sort on read, or
//     carry an explicit ordering field, when a particular order matters.
//   - A slice with no key tag is synchronized as one value: concurrent edits
//     resolve by last-write-wins, so one writer's version of the whole slice
//     wins. Prefer a map or a keyed slice for anything edited concurrently.
//   - [List] is a sequence that merges: concurrent insertions and deletions
//     from different replicas all survive and every replica agrees on the
//     order. Reach for it when position matters and several writers can edit
//     at once — the case an ordinary slice cannot express.
//
// Every case converges — replicas that have seen the same operations agree —
// but only the first, second and last merge concurrent edits rather than
// choosing between them.
//
// [Text] and [List] resolve concurrency themselves by implementing
// [Convergent]; a type of your own can do the same.
package crdt

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"reflect"
	"sort"
	"strings"
	"sync"

	deep "github.com/brunoga/deep/v5"
	"github.com/brunoga/deep/v5/crdt/hlc"
	icore "github.com/brunoga/deep/v5/internal/core"
)

// LWW represents a Last-Write-Wins register for type T.
// Embed LWW fields in a struct to track per-field causality.
// Use Set to update the value; it accepts the write only if ts is strictly newer.
type LWW[T any] struct {
	Value     T       `json:"v"`
	Timestamp hlc.HLC `json:"t"`
}

// Set updates the register's value and timestamp if ts is after the current
// timestamp. Returns true if the update was accepted.
func (l *LWW[T]) Set(v T, ts hlc.HLC) bool {
	if ts.After(l.Timestamp) {
		l.Value = v
		l.Timestamp = ts
		return true
	}
	return false
}

// CRDT represents a Conflict-free Replicated Data Type wrapper around type T.
type CRDT[T any] struct {
	mu         sync.RWMutex
	value      T
	clocks     map[string]hlc.HLC
	tombstones map[string]hlc.HLC
	nodeID     string
	clock      *hlc.Clock

	observerMu   sync.Mutex
	observers    map[int]func(Change[T])
	nextObserver int
}

// ChangeSource says where a change came from.
type ChangeSource int

const (
	// ChangeLocal is an edit made on this replica through [CRDT.Edit].
	ChangeLocal ChangeSource = iota
	// ChangeRemote is a delta from a peer, applied through [CRDT.ApplyDelta].
	ChangeRemote
	// ChangeMerge is the result of [CRDT.Merge] with another replica.
	ChangeMerge
)

func (s ChangeSource) String() string {
	switch s {
	case ChangeLocal:
		return "local"
	case ChangeRemote:
		return "remote"
	case ChangeMerge:
		return "merge"
	}
	return "unknown"
}

// Change describes what actually happened to a replica's value.
//
// Patch holds the operations that were applied, which for a remote delta is
// only the part that survived conflict resolution — an operation another
// replica made but this one rejected as stale does not appear. Reading it is
// how a consumer learns what changed without diffing snapshots: a user
// interface can redraw just the affected paths.
type Change[T any] struct {
	Patch  deep.Patch[T]
	Source ChangeSource
}

// OnChange registers fn to be called after every change to this replica, and
// returns a function that unregisters it.
//
// Callbacks run synchronously on the goroutine that made the change, after the
// replica's lock has been released. A callback may therefore read the replica,
// and may edit it — an edit from inside a callback announces itself in turn, so
// guard against recursing forever. A slow callback holds up whoever made the
// change.
//
// Nothing is serialized on your behalf: changes made from several goroutines
// deliver on those goroutines, so a callback can run concurrently with itself.
// Take a lock inside the callback, or hand changes to a single goroutine, if
// that matters. The Patch in each Change describes that change on its own, so
// they can be processed independently.
func (c *CRDT[T]) OnChange(fn func(Change[T])) (cancel func()) {
	c.observerMu.Lock()
	defer c.observerMu.Unlock()

	if c.observers == nil {
		c.observers = make(map[int]func(Change[T]))
	}
	id := c.nextObserver
	c.nextObserver++
	c.observers[id] = fn

	return func() {
		c.observerMu.Lock()
		defer c.observerMu.Unlock()
		delete(c.observers, id)
	}
}

// notify delivers a change to the registered observers. It must be called with
// no lock held: a callback is free to call back into the replica.
func (c *CRDT[T]) notify(change Change[T]) {
	if change.Patch.IsEmpty() {
		return
	}

	c.observerMu.Lock()
	if len(c.observers) == 0 {
		c.observerMu.Unlock()
		return
	}
	fns := make([]func(Change[T]), 0, len(c.observers))
	for _, fn := range c.observers {
		fns = append(fns, fn)
	}
	c.observerMu.Unlock()

	for _, fn := range fns {
		fn(change)
	}
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

// NewCRDT creates a new CRDT wrapper around a deep copy of initial, so later
// changes to the caller's value cannot reach inside the replica (and two
// replicas seeded from one value do not share state).
func NewCRDT[T any](initial T, nodeID string) *CRDT[T] {
	return &CRDT[T]{
		value:      deep.Clone(initial),
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
	delta := c.edit(fn)
	c.notify(Change[T]{Patch: delta.patch, Source: ChangeLocal})
	return delta
}

func (c *CRDT[T]) edit(fn func(*T)) Delta[T] {
	c.mu.Lock()
	defer c.mu.Unlock()

	workingCopy := deep.Clone(c.value)
	fn(&workingCopy)
	canonicalizeKeyedSlices(reflect.ValueOf(&workingCopy).Elem())

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
	applied, ok := c.applyDelta(delta)
	c.notify(Change[T]{Patch: applied, Source: ChangeRemote})
	return ok
}

func (c *CRDT[T]) applyDelta(delta Delta[T]) (deep.Patch[T], bool) {
	if delta.patch.IsEmpty() {
		return deep.Patch[T]{}, false
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.clock.Update(delta.Timestamp)

	// A self-merging value has its own convergence rules — always apply,
	// skipping the LWW clock filter that would discard concurrent edits.
	if _, ok := any(c.value).(Convergent); ok {
		if err := deep.Apply(&c.value, delta.patch); err != nil {
			return deep.Patch[T]{}, false
		}
		return delta.patch, true
	}
	defer canonicalizeKeyedSlices(reflect.ValueOf(&c.value).Elem())

	root := reflect.ValueOf(&c.value).Elem()

	var filtered []deep.Operation
	for _, op := range delta.patch.Operations {
		opTime := delta.Timestamp

		// A value that merges itself settles concurrency on its own terms, so
		// its operations skip the clock filter entirely. Filtering them would
		// throw away one side of a concurrent edit: both replicas write the
		// same path, and last-write-wins would keep only the later one.
		if convergentTarget(root, op.Path) {
			filtered = append(filtered, op)
			continue
		}

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
		return deep.Patch[T]{}, false
	}

	// An operation aimed at a value that merges itself is handed to that value
	// rather than applied by the generic path, which would overwrite it. This
	// is also what lets such an operation carry a description of the change
	// rather than the whole new value.
	remaining := filtered[:0:0]
	for _, op := range filtered {
		if handled, err := applyToConvergent(root, op); handled {
			if err != nil {
				return deep.Patch[T]{}, false
			}
			continue
		}
		remaining = append(remaining, op)
	}

	if len(remaining) > 0 {
		if err := deep.Apply(&c.value, deep.Patch[T]{Operations: remaining}); err != nil {
			return deep.Patch[T]{}, false
		}
	}
	return deep.Patch[T]{Operations: filtered}, true
}

// opApplier is a convergent value that can take an operation aimed at it.
type opApplier interface {
	applyOperation(op deep.Operation, logger *slog.Logger) (bool, error)
}

// convergentTarget reports whether the path names a value that merges itself.
func convergentTarget(root reflect.Value, path string) bool {
	if path == "" || path == "/" {
		return false
	}
	target, err := icore.DeepPath(path).Resolve(root)
	if err != nil || !target.IsValid() {
		return false
	}
	if isSelfMerging(target.Type()) {
		return true
	}
	return target.CanAddr() && isSelfMerging(target.Addr().Type())
}

// applyToConvergent hands op to the value it names when that value merges
// itself, reporting whether it did.
func applyToConvergent(root reflect.Value, op deep.Operation) (bool, error) {
	if op.Path == "" || op.Path == "/" {
		return false, nil
	}
	target, err := icore.DeepPath(op.Path).Resolve(root)
	if err != nil || !target.IsValid() {
		return false, nil
	}
	// The method is on the pointer, so an addressable value has to be taken by
	// address first; a pointer field already is one.
	if target.Kind() != reflect.Pointer {
		if !target.CanAddr() {
			return false, nil
		}
		target = target.Addr()
	}
	if applier, ok := target.Interface().(opApplier); ok {
		inner := op
		inner.Path = "/"
		return applier.applyOperation(inner, slog.Default())
	}

	// A type of the caller's own gets the same treatment through the public
	// interface: hand it what arrived and keep what it returns. Without this it
	// would skip the clock filter — because it settles concurrency itself — and
	// then be overwritten anyway, which is worse than either.
	value := target
	if value.Kind() == reflect.Pointer && !value.IsNil() {
		value = value.Elem()
	}
	conv, ok := value.Interface().(Convergent)
	if !ok || !value.CanSet() {
		return false, nil
	}
	if op.New == nil {
		return false, nil
	}
	incoming, err := icore.ConvertValueChecked(reflect.ValueOf(op.New), value.Type())
	if err != nil {
		return false, nil // not something this value can merge; let the generic path try
	}
	merged := reflect.ValueOf(conv.MergeFrom(incoming.Interface()))
	if !merged.IsValid() || merged.Type() != value.Type() {
		return false, nil
	}
	value.Set(merged)
	return true, nil
}

// canonicalizeKeyedSlices puts every keyed slice reachable from v into a
// deterministic order.
//
// A slice whose element type carries a deep:"key" tag is a collection of
// identified elements, not an ordered list: Diff addresses those elements by
// key and emits nothing at all when they are merely reordered, so element
// order is not part of the synchronized state. Apply, however, appends a new
// element at the end, which means the order a replica ends up with depends on
// the order deltas happened to arrive — two replicas holding exactly the same
// elements could disagree, and a CRDT must not do that.
//
// Sorting by key gives every replica the same order for the same set of
// elements. Order within a keyed slice is therefore not meaningful in a CRDT;
// keep an explicit sort field, or use a map, when a particular order matters.
func canonicalizeKeyedSlices(v reflect.Value) {
	if !v.IsValid() {
		return
	}
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if !v.IsNil() {
			canonicalizeKeyedSlices(v.Elem())
		}
	case reflect.Struct:
		if isSelfMerging(v.Type()) {
			return // the value orders itself
		}
		for i := 0; i < v.NumField(); i++ {
			f := v.Field(i)
			if !f.CanInterface() {
				continue
			}
			canonicalizeKeyedSlices(f)
		}
	case reflect.Slice, reflect.Array:
		if isSelfMerging(v.Type()) {
			return // the value orders itself
		}
		for i := 0; i < v.Len(); i++ {
			canonicalizeKeyedSlices(v.Index(i))
		}
		if v.Kind() != reflect.Slice || !v.CanSet() {
			return
		}
		keyIdx, ok := keyFieldOf(v.Type())
		if !ok {
			return
		}
		sort.SliceStable(v.Interface(), func(i, j int) bool {
			return keyString(v.Index(i), keyIdx) < keyString(v.Index(j), keyIdx)
		})
	case reflect.Map:
		for _, k := range v.MapKeys() {
			elem := v.MapIndex(k)
			// Map values are not addressable; work on a copy and write it back.
			copied := reflect.New(elem.Type()).Elem()
			copied.Set(elem)
			canonicalizeKeyedSlices(copied)
			v.SetMapIndex(k, copied)
		}
	}
}

// keyFieldOf returns the index of the deep:"key" field on a slice's element
// type, looking through a pointer element.
func keyFieldOf(sliceType reflect.Type) (int, bool) {
	elem := sliceType.Elem()
	for elem.Kind() == reflect.Pointer {
		elem = elem.Elem()
	}
	if elem.Kind() != reflect.Struct {
		return 0, false
	}
	return icore.GetKeyField(elem)
}

// keyString renders an element's key for ordering.
func keyString(elem reflect.Value, keyIdx int) string {
	return fmt.Sprintf("%v", icore.ExtractKey(elem, keyIdx))
}

// Convergent is implemented by types that resolve concurrent edits themselves
// rather than being replaced wholesale under last-write-wins. [Text] and [List]
// implement it.
//
// A [CRDT] applies operations on a Convergent value unconditionally, skipping
// the clock filter that would discard one side of a concurrent edit, and
// combines two copies by calling MergeFrom rather than choosing a winner.
// Implement it to plug a data type of your own into that machinery:
//
//	func (s MySet) MergeFrom(other any) any {
//	    o, ok := other.(MySet)
//	    if !ok {
//	        return s
//	    }
//	    return union(s, o)
//	}
//
// MergeFrom must be commutative, associative and idempotent — merging in any
// order, any number of times, has to reach the same value — and must return a
// value of the receiver's own type.
type Convergent interface {
	MergeFrom(other any) any
}

var convergentType = reflect.TypeOf((*Convergent)(nil)).Elem()

// Compactable is implemented by values that can discard history every replica
// has already seen — [Text] and [List] do. [CRDT.Compact] calls it on the
// values it finds inside a replica, so a caller compacts a whole replica in one
// go rather than reaching into its fields.
type Compactable interface {
	// CompactBefore returns the value with history at or before the watermark
	// discarded. What the value represents must not change.
	CompactBefore(before hlc.HLC) any
}

var compactableType = reflect.TypeOf((*Compactable)(nil)).Elem()

// compactValue replaces every Compactable reachable from v with its compacted
// form. It works on the replica's own value rather than through an edit: this
// discards history that is no longer needed, it does not change what the
// replica holds, so there is nothing to tell peers about.
func compactValue(v reflect.Value, before hlc.HLC) {
	if !v.IsValid() {
		return
	}
	if v.CanSet() && v.Type().Implements(compactableType) {
		if c, ok := v.Interface().(Compactable); ok {
			compacted := c.CompactBefore(before)
			if cv := reflect.ValueOf(compacted); cv.IsValid() && cv.Type() == v.Type() {
				v.Set(cv)
			}
			return
		}
	}
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if !v.IsNil() {
			compactValue(v.Elem(), before)
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if f := v.Field(i); f.CanInterface() {
				compactValue(f, before)
			}
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			compactValue(v.Index(i), before)
		}
	case reflect.Map:
		for _, k := range v.MapKeys() {
			elem := v.MapIndex(k)
			copied := reflect.New(elem.Type()).Elem()
			copied.Set(elem)
			compactValue(copied, before)
			v.SetMapIndex(k, copied)
		}
	}
}

// isSelfMerging reports whether values of t merge themselves.
func isSelfMerging(t reflect.Type) bool { return t.Implements(convergentType) }

// selfMergingAncestorPath walks up from opPath toward the root, resolving each
// prefix against root, and returns the path of the nearest ancestor that merges
// itself together with true. It returns ("", false) if the op does not belong
// to such a field, keeping traversal to O(depth) Resolve calls per op.
func selfMergingAncestorPath(root reflect.Value, opPath string) (string, bool) {
	// The operation's own path is checked first: a Convergent value that is not
	// addressed element by element — anything but a keyed slice — is named
	// directly by the operation, with no deeper path to walk up from.
	path := opPath
	for {
		if val, err := icore.DeepPath(path).Resolve(root); err == nil && val.IsValid() {
			if isSelfMerging(val.Type()) {
				return path, true
			}
			// This value merges by LWW, but it might itself sit inside one that
			// does not (a TextRun whose parent is a Text), so keep walking up.
		}
		idx := strings.LastIndexByte(path, '/')
		if idx <= 0 {
			return "", false
		}
		path = path[:idx]
	}
}

// Compact discards bookkeeping for changes at or before before, and reports how
// many entries it dropped.
//
// A replica remembers when each path was last written and when each was
// removed, so that it can tell a stale update from a new one. That record only
// grows: a path written once is remembered for good, and a long-lived replica
// ends up carrying more history than data — a map emptied of its five hundred
// keys still holds five hundred entries saying when each went.
//
// Dropping the record is only safe for changes every replica has already seen,
// because what it protects against is an old update arriving late. Pass the
// oldest timestamp still in flight anywhere in the system — in practice the
// minimum, across peers, of the last delta each has acknowledged. Passing
// something newer risks accepting an update that should have lost, or bringing
// back a value that was deleted.
//
// Compacting reaches the sequences inside the value too, dropping text and list
// entries that were deleted before the watermark. It does this to the replica's
// own copy rather than as an edit: no delta is produced, because what the
// replica represents does not change.
//
// A replica that has compacted still converges with one that has not: merging
// with a peer that still remembers restores what was dropped.
func (c *CRDT[T]) Compact(before hlc.HLC) int {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Sequences hold their own history — a deletion leaves a tombstone behind
	// so that a concurrent insertion still has something to attach to — and it
	// is collectable under the same condition.
	compactValue(reflect.ValueOf(&c.value).Elem(), before)

	dropped := 0
	for path, ts := range c.clocks {
		if !ts.After(before) {
			delete(c.clocks, path)
			dropped++
		}
	}
	for path, ts := range c.tombstones {
		if !ts.After(before) {
			delete(c.tombstones, path)
			dropped++
		}
	}
	return dropped
}

// Reverse applies the inverse of delta to this node and returns a new Delta
// representing the undo operation. The returned Delta carries a fresh HLC
// timestamp so it is causally after the original edit and will be accepted by
// ApplyDelta on any peer that has already seen the original.
//
// Calling Reverse on the returned Delta produces a redo Delta.
func (c *CRDT[T]) Reverse(delta Delta[T]) Delta[T] {
	reversed := delta.patch.Reverse()
	now := c.clock.Now()
	undoDelta := Delta[T]{patch: reversed, Timestamp: now}
	c.ApplyDelta(undoDelta)
	return undoDelta
}

// Merge performs a full state-based merge with another CRDT node.
// For each changed field the node with the strictly newer effective timestamp
// (max of write clock and tombstone) wins. Text fields are always merged
// convergently via MergeTextRuns, bypassing LWW.
func (c *CRDT[T]) Merge(other *CRDT[T]) bool {
	applied, ok := c.merge(other)
	c.notify(Change[T]{Patch: applied, Source: ChangeMerge})
	return ok
}

func (c *CRDT[T]) merge(other *CRDT[T]) (deep.Patch[T], bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, h := range other.clocks {
		c.clock.Update(h)
	}
	for _, h := range other.tombstones {
		c.clock.Update(h)
	}

	// Fast path: T itself merges.
	if v, ok := any(c.value).(Convergent); ok {
		before := deep.Clone(c.value)
		c.value = v.MergeFrom(other.value).(T)
		c.mergeMeta(other)
		applied, err := deep.Diff(before, c.value)
		if err != nil {
			applied = deep.Patch[T]{}
		}
		return applied, true
	}

	defer canonicalizeKeyedSlices(reflect.ValueOf(&c.value).Elem())

	patch, err := deep.Diff(c.value, other.value)
	if err != nil || patch.IsEmpty() {
		c.mergeMeta(other)
		return deep.Patch[T]{}, false
	}

	beforeMerge := deep.Clone(c.value)
	localRoot := reflect.ValueOf(&c.value).Elem()
	otherRoot := reflect.ValueOf(&other.value).Elem()

	// Separate Text-field ops from LWW-eligible ops. Text is convergent, so
	// we collect affected Text paths and apply MergeTextRuns after the LWW
	// apply — no full tree walk required.
	textPaths := make(map[string]struct{})
	var filtered []deep.Operation
	for _, op := range patch.Operations {
		if textPath, ok := selfMergingAncestorPath(localRoot, op.Path); ok {
			textPaths[textPath] = struct{}{}
			continue
		}

		// State-based LWW: apply each op only if the remote effective time is
		// strictly newer than the local effective time for that path.
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

	changed := len(filtered) > 0
	if changed {
		_ = deep.Apply(&c.value, deep.Patch[T]{Operations: filtered})
		// Refresh localRoot: Apply may have updated c.value in place.
		localRoot = reflect.ValueOf(&c.value).Elem()
	}

	// Convergently merge each Text field by path. Both values are resolved
	// fresh from the (already-updated) local root and the remote root.
	for textPath := range textPaths {
		localVal, err := icore.DeepPath(textPath).Resolve(localRoot)
		if err != nil || !localVal.IsValid() {
			continue
		}
		remoteVal, err := icore.DeepPath(textPath).Resolve(otherRoot)
		if err != nil || !remoteVal.IsValid() {
			continue
		}
		local, ok := localVal.Interface().(Convergent)
		if !ok {
			continue
		}
		merged := local.MergeFrom(remoteVal.Interface())
		if err := icore.DeepPath(textPath).Set(localRoot, reflect.ValueOf(merged)); err != nil {
			slog.Default().Error("crdt: merge of self-merging field failed", "path", textPath, "err", err)
			continue
		}
		changed = true
	}

	if !changed {
		return deep.Patch[T]{}, false
	}
	// Report what the merge actually did rather than what was proposed: the
	// filtered operations plus whatever the self-merging fields resolved to.
	applied, err := deep.Diff(beforeMerge, c.value)
	if err != nil {
		applied = deep.Patch[T]{Operations: filtered}
	}
	return applied, true
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
	c.clock.SetLatest(m.Latest)
	return nil
}
