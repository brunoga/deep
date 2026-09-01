package crdt

import (
	"encoding/json"
	"log/slog"
	"sort"

	deep "github.com/brunoga/deep/v5"
	"github.com/brunoga/deep/v5/crdt/hlc"
)

// ListEntry is one element of a [List] together with the metadata that places
// it in the sequence.
type ListEntry[T any] struct {
	// ID identifies this element for all time. It is assigned once, by the
	// replica that inserted the element, and never reused. The deep:"key" tag
	// makes a List nested inside a struct diff and merge entry by entry rather
	// than as one opaque value.
	ID hlc.HLC `deep:"key" json:"id"`
	// Prev is the ID of the element this one was inserted after — the zero
	// value for an insertion at the head. Position is expressed relative to a
	// neighbour rather than as an index, so it survives concurrent edits that
	// shift indices around.
	Prev hlc.HLC `json:"p,omitempty"`
	// Deleted marks a removed element. The entry stays as a tombstone so that a
	// concurrent insertion that named it as Prev still has somewhere to attach.
	Deleted bool `json:"d,omitempty"`
	Value   T    `json:"v"`
}

// List is a convergent sequence of T.
//
// An ordinary slice inside a [CRDT] is synchronized as a single value, so
// concurrent edits resolve by last-write-wins and one writer's version of the
// whole slice wins. A List merges instead: concurrent insertions and deletions
// from different replicas all survive, and every replica converges on the same
// order.
//
// Elements are placed relative to their neighbours rather than by index, and
// concurrent insertions at the same position are ordered by ID, so replicas
// agree regardless of the order updates arrive in. Use it for collaborative
// ordered data — a task list being reordered by several people, a document
// outline, a playlist.
//
// The zero List is empty and ready to use. Like [Text], a List is a value:
// Insert and Delete return a new List rather than mutating the receiver.
type List[T any] []ListEntry[T]

// NewList returns a List holding values, in order, with IDs drawn from clock.
func NewList[T any](clock *hlc.Clock, values ...T) List[T] {
	var l List[T]
	for i, v := range values {
		l = l.Insert(i, v, clock)
	}
	return l
}

// Len returns the number of live elements.
func (l List[T]) Len() int {
	n := 0
	for _, e := range l {
		if !e.Deleted {
			n++
		}
	}
	return n
}

// Items returns the live elements in order.
func (l List[T]) Items() []T {
	ordered := l.ordered()
	out := make([]T, 0, len(ordered))
	for _, e := range ordered {
		if !e.Deleted {
			out = append(out, e.Value)
		}
	}
	return out
}

// At returns the live element at pos, and whether pos is in range.
func (l List[T]) At(pos int) (T, bool) {
	items := l.Items()
	if pos < 0 || pos >= len(items) {
		var zero T
		return zero, false
	}
	return items[pos], true
}

// Insert places value at pos, counted over live elements. A pos of 0 inserts at
// the head; a pos at or past the end appends.
func (l List[T]) Insert(pos int, value T, clock *hlc.Clock) List[T] {
	entry := ListEntry[T]{
		ID:    clock.ReserveSequence(1),
		Prev:  l.idBefore(pos),
		Value: value,
	}
	return append(append(List[T]{}, l...), entry).ordered()
}

// Delete removes count elements starting at pos, counted over live elements.
// Removed entries are kept as tombstones so concurrent insertions that named
// them still have an anchor.
func (l List[T]) Delete(pos, count int) List[T] {
	if count <= 0 {
		return l
	}
	ordered := append(List[T]{}, l.ordered()...)
	live := 0
	for i := range ordered {
		if ordered[i].Deleted {
			continue
		}
		if live >= pos && live < pos+count {
			ordered[i].Deleted = true
		}
		live++
	}
	return ordered
}

// idBefore returns the ID of the live element at pos-1, i.e. the element a new
// element at pos should be anchored after. It is the zero ID for the head.
func (l List[T]) idBefore(pos int) hlc.HLC {
	if pos <= 0 {
		return hlc.HLC{}
	}
	live := 0
	for _, e := range l.ordered() {
		if e.Deleted {
			continue
		}
		live++
		if live == pos {
			return e.ID
		}
	}
	return l.lastID()
}

// lastID returns the ID of the final element in sequence order, live or not.
func (l List[T]) lastID() hlc.HLC {
	ordered := l.ordered()
	if len(ordered) == 0 {
		return hlc.HLC{}
	}
	return ordered[len(ordered)-1].ID
}

// ordered returns the entries in sequence order.
//
// Entries form a tree: each one names the element it follows. Walking that tree
// depth-first, visiting the children of each anchor in descending ID order,
// yields the same sequence on every replica — concurrent insertions after the
// same anchor are ordered by ID, which every replica compares identically.
func (l List[T]) ordered() List[T] {
	if len(l) <= 1 {
		return l
	}
	children := make(map[hlc.HLC][]ListEntry[T], len(l))
	for _, e := range l {
		children[e.Prev] = append(children[e.Prev], e)
	}
	for _, group := range children {
		sort.Slice(group, func(i, j int) bool { return group[i].ID.After(group[j].ID) })
	}

	result := make(List[T], 0, len(l))
	seen := make(map[hlc.HLC]bool, len(l))

	// Depth-first preorder, iterative so that a long list does not recurse once
	// per element: emit an entry, then everything anchored to it, then its
	// siblings. Children are pushed in reverse so the highest ID is emitted
	// first, matching the sort above.
	var stack []ListEntry[T]
	pushChildren := func(anchor hlc.HLC) {
		group := children[anchor]
		for i := len(group) - 1; i >= 0; i-- {
			stack = append(stack, group[i])
		}
	}
	pushChildren(hlc.HLC{})
	for len(stack) > 0 {
		e := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[e.ID] {
			continue
		}
		seen[e.ID] = true
		result = append(result, e)
		pushChildren(e.ID)
	}

	// Any entry whose anchor never arrived (a delta delivered out of order)
	// would be dropped by the walk; keep it at the end so nothing is lost.
	if len(result) < len(l) {
		for _, e := range l {
			if !seen[e.ID] {
				seen[e.ID] = true
				result = append(result, e)
			}
		}
	}
	return result
}

// Compact drops entries deleted at or before before, returning the rest.
//
// A deletion leaves the entry behind as a tombstone, because a replica that has
// not heard about it may still insert next to what was deleted and needs
// something to anchor to. Dropping one is only safe once every replica has seen
// the deletion, so before must be older than anything still in flight; see
// [CRDT.Compact], which takes the same watermark.
//
// An entry that something is still anchored to is kept whatever its age:
// removing it would leave whatever follows it without a place, and re-anchoring
// would change this replica's order without changing anyone else's.
func (l List[T]) Compact(before hlc.HLC) List[T] {
	anchored := make(map[hlc.HLC]bool, len(l))
	for _, e := range l {
		anchored[e.Prev] = true
	}

	result := make(List[T], 0, len(l))
	for _, e := range l {
		if e.Deleted && !e.ID.After(before) && !anchored[e.ID] {
			continue
		}
		result = append(result, e)
	}
	if len(result) == len(l) {
		return l
	}
	return result
}

// CompactBefore implements [Compactable].
func (l List[T]) CompactBefore(before hlc.HLC) any { return l.Compact(before) }

// MergeLists combines two Lists into one containing every element either side
// has seen, in the order both sides agree on. A deletion on either side wins,
// since a tombstone records an element that was removed rather than never seen.
func MergeLists[T any](a, b List[T]) List[T] {
	combined := make(map[hlc.HLC]ListEntry[T], len(a)+len(b))
	for _, e := range a {
		combined[e.ID] = e
	}
	for _, e := range b {
		if existing, ok := combined[e.ID]; ok {
			if e.Deleted {
				existing.Deleted = true
				combined[e.ID] = existing
			}
			continue
		}
		combined[e.ID] = e
	}

	merged := make(List[T], 0, len(combined))
	for _, e := range combined {
		merged = append(merged, e)
	}
	return merged.ordered()
}

// MergeFrom implements [Convergent], so a List merges with a peer's copy
// instead of one replacing the other under last-write-wins.
func (l List[T]) MergeFrom(other any) any {
	o, ok := other.(List[T])
	if !ok {
		return l
	}
	return MergeLists(l, o)
}

// Diff reports the change from l to other as a single whole-value operation:
// the receiving side merges rather than overwrites, so the operation carries
// the sequence and lets MergeLists work out the result.
func (l List[T]) Diff(other List[T]) deep.Patch[List[T]] {
	if len(l) == len(other) {
		same := true
		for i := range l {
			if l[i].ID != other[i].ID || l[i].Deleted != other[i].Deleted {
				same = false
				break
			}
		}
		if same {
			return deep.Patch[List[T]]{}
		}
	}
	return deep.Patch[List[T]]{Operations: []deep.Operation{
		{Kind: deep.OpReplace, Path: "/", Old: l, New: other},
	}}
}

// Patch applies p to l, merging rather than overwriting.
func (l *List[T]) Patch(p deep.Patch[List[T]], logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}
	var errs []error
	for _, op := range p.Operations {
		if _, err := l.applyOperation(op, logger); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return &deep.ApplyError{Errors: errs}
	}
	return nil
}

func (l *List[T]) applyOperation(op deep.Operation, _ *slog.Logger) (bool, error) {
	if op.Path != "" && op.Path != "/" {
		return false, nil // a deeper path is not ours; let reflection handle it
	}
	switch v := op.New.(type) {
	case List[T]:
		*l = MergeLists(*l, v)
		return true, nil
	case []any:
		// The operation came back from JSON, where the sequence decoded as a
		// generic slice; re-decode it as a List before merging.
		data, err := json.Marshal(v)
		if err != nil {
			return false, err
		}
		var other List[T]
		if err := json.Unmarshal(data, &other); err != nil {
			return false, err
		}
		*l = MergeLists(*l, other)
		return true, nil
	}
	return false, nil
}
