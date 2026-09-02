package deep

import (
	"reflect"
	"sync"

	icore "github.com/brunoga/deep/v5/internal/core"
)

// CloneMemo records the copy made for each value during a single deep copy, so
// that a value reached more than once is copied once and every reference to it
// in the result points at that one copy.
//
// Generated Clone methods create and thread a memo when values of the type can
// hold two references to the same value — through a cycle, or simply through
// two routes to one pointer. Types where that cannot happen never allocate one.
//
// The memo shares its identity space with the reflection engine: fields that
// generated code hands to CloneShared record their copies in the same map, so a
// value referenced from both sides is still copied exactly once.
//
// A memo belongs to one copy: it is not safe for concurrent use.
type CloneMemo struct {
	m icore.PointersMap
}

var cloneMemoPool = sync.Pool{
	New: func() any { return &CloneMemo{m: make(icore.PointersMap, 8)} },
}

// NewCloneMemo returns an empty memo. Pass it to Release when the copy is done
// so it can be reused.
func NewCloneMemo() *CloneMemo {
	return cloneMemoPool.Get().(*CloneMemo)
}

// Release returns c for reuse. The copies it recorded stay valid — only the
// bookkeeping is discarded — but it must not be called while a copy using c is
// still running.
func (c *CloneMemo) Release() {
	if c == nil {
		return
	}
	clear(c.m)
	cloneMemoPool.Put(c)
}

// Load returns the copy already made for the pointer src, if there is one.
//
// Identity is the pointer's address together with its type: pointers of
// different types that share an address — a struct and its first field — do not
// collide.
func (c *CloneMemo) Load(src any) (any, bool) {
	if c == nil {
		return nil, false
	}
	rv := reflect.ValueOf(src)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return nil, false
	}
	dst, ok := c.m[icore.PointersMapKey{Ptr: rv.Pointer(), Typ: rv.Type()}]
	if !ok {
		return nil, false
	}
	return dst.Interface(), true
}

// Store records dst as the copy of the pointer src. It must be called before
// descending into src's fields: that is what lets a reference back to src, from
// anywhere below it, resolve to dst instead of starting the copy over.
func (c *CloneMemo) Store(src, dst any) {
	if c == nil {
		return
	}
	rv := reflect.ValueOf(src)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return
	}
	c.m[icore.PointersMapKey{Ptr: rv.Pointer(), Typ: rv.Type()}] = reflect.ValueOf(dst)
}

// CloneShared deep-copies v through the reflection engine, recording into (and
// honouring) memo. Generated Clone methods use it for the fields they cannot
// copy themselves — types from other packages, interfaces, generics — so that a
// value shared between such a field and the rest of the struct is still copied
// once, wherever it was reached first.
//
// With a nil memo it behaves like Clone.
func CloneShared[T any](v T, memo *CloneMemo) T {
	if memo == nil {
		return Clone(v)
	}
	res, err := icore.CopyShared(v, memo.m)
	if err != nil {
		// Mirror Clone's contract: unsupported values degrade to the zero
		// value rather than failing the whole copy.
		var zero T
		return zero
	}
	if res == nil {
		var zero T
		return zero
	}
	return res.(T)
}

// VisitSet records the pairs of values a recursive comparison has already
// started on, so that following a cycle stops when it repeats rather than
// running forever, and a value reachable by many routes is compared once
// instead of once per route.
//
// Generated Equal methods create and thread a set when values of the type can
// reach the same value twice. Types where that cannot happen never allocate
// one.
//
// A set belongs to one comparison: it is not safe for concurrent use.
type VisitSet struct {
	m map[visitPair]bool
}

type visitPair struct{ a, b any }

var visitSetPool = sync.Pool{
	New: func() any { return &VisitSet{m: make(map[visitPair]bool, 8)} },
}

// NewVisitSet returns an empty set. Pass it to Release when the comparison is
// done so it can be reused.
func NewVisitSet() *VisitSet {
	return visitSetPool.Get().(*VisitSet)
}

// Release returns v for reuse. It must not be called while a comparison using v
// is still running.
func (v *VisitSet) Release() {
	if v == nil {
		return
	}
	clear(v.m)
	visitSetPool.Put(v)
}

// Enter records the pair (a, b) and reports whether it is new. A false result
// means the pair has been reached before and the caller should treat the two as
// matching rather than descend again: once a pair has been compared the answer
// is settled — had it differed, the comparison would already have stopped — and
// for a pair still being compared further up a cycle, the two differ only if
// something else on the cycle differs, which that comparison will find.
func (v *VisitSet) Enter(a, b any) bool {
	if v == nil {
		return true
	}
	p := visitPair{a, b}
	if v.m[p] {
		return false
	}
	v.m[p] = true
	return true
}

// DiffMemo tracks the pairs of values a generated Diff has visited, and turns
// repeat visits into alias operations.
//
// A pair is diffed once, at the first path that reaches it — shared structure
// can be reachable by exponentially many paths, so diffing every route is not
// an option. Each later route that reaches an already-diffed, changed pair
// records one OpAlias operation instead: "make this path point at the same
// object as the first path". Applied in order, the aliases rebuild the sharing
// the new value has, whatever the target looked like before.
//
// A memo belongs to one Diff call: it is not safe for concurrent use.
type DiffMemo struct {
	m       map[visitPair]*diffVisit
	aliases []Operation
}

type diffVisit struct {
	path      string
	done      bool
	changed   bool
	aliasMark int
}

var diffMemoPool = sync.Pool{
	New: func() any { return &DiffMemo{m: make(map[visitPair]*diffVisit, 8)} },
}

// NewDiffMemo returns an empty memo. Pass it to Release when the diff is done
// so it can be reused.
func NewDiffMemo() *DiffMemo {
	return diffMemoPool.Get().(*DiffMemo)
}

// Release returns d for reuse. It must not be called while a diff using d is
// still running. The operations AliasOperations returned stay valid: appending
// them copies the values.
func (d *DiffMemo) Release() {
	if d == nil {
		return
	}
	clear(d.m)
	d.aliases = d.aliases[:0]
	diffMemoPool.Put(d)
}

// Enter reports whether the pair (a, b), reached at path, is new.
//
// True means the caller should diff the pair, then call Leave. False means the
// pair is already handled: if a completed visit found changes, Enter has
// recorded an alias operation for this path — reporting the changes again here
// would repeat them once per route, and there can be exponentially many routes.
// A pair still in progress is a cycle, and is left to the comparison already
// under way.
func (d *DiffMemo) Enter(a, b any, path string) bool {
	if d == nil {
		return true
	}
	p := visitPair{a, b}
	if v, ok := d.m[p]; ok {
		if v.done && v.changed {
			d.aliases = append(d.aliases, Operation{
				Kind: OpAlias,
				Path: path,
				From: v.path,
				Old:  a,
			})
		}
		return false
	}
	d.m[p] = &diffVisit{path: path, aliasMark: len(d.aliases)}
	return true
}

// Leave completes the visit Enter opened for (a, b). ops is the number of
// operations the pair's diff produced; the pair also counts as changed when
// aliases were recorded below it, since those are changes too — just ones that
// live in this memo rather than in the caller's patch.
func (d *DiffMemo) Leave(a, b any, ops int) {
	if d == nil {
		return
	}
	if v, ok := d.m[visitPair{a, b}]; ok {
		v.done = true
		v.changed = ops > 0 || len(d.aliases) > v.aliasMark
	}
}

// AliasOperations returns the alias operations recorded so far, in the order
// the routes were reached. Generated Diff appends them after its own
// operations; an alias shares the object its From path holds, so it lands
// correctly whether the operations before it mutated that object in place or
// replaced it.
func (d *DiffMemo) AliasOperations() []Operation {
	if d == nil {
		return nil
	}
	return d.aliases
}
