package core

import (
	"reflect"
	"sync"
	"sync/atomic"
)

// A type family is custom behaviour for every type matching a predicate, where
// RegisterCustomEqual and friends are custom behaviour for one concrete type.
//
// The distinction matters for types produced by another runtime. A protobuf
// application holds hundreds of generated message types; registering each one
// is not a usable interface, and all of them need the same treatment — the
// proto runtime's own equality, its own cloning, its own wire form. A family
// says that once: "any type matching this predicate is handled like so."
//
// This file holds the half of a family the core packages consume — equality,
// cloning, and the value codec. The diff and apply half lives in the engine
// package, which owns the Operation type those signatures need.

// Family is the core-facing part of a registered type family.
type Family struct {
	// Name identifies the family in errors and diagnostics.
	Name string
	// Match reports whether t belongs to the family. It is consulted once per
	// type; the verdict is cached.
	Match func(t reflect.Type) bool
	// Equal reports whether two values of a matched type are equal. a and b
	// are the values as they appear (for pointer types, the pointers).
	Equal func(a, b any) bool
	// Clone returns a deep copy of a matched value.
	Clone func(v any) any
	// Marshal renders a matched value into its wire (JSON) form, and Unmarshal
	// reverses it for the given matched type. They exist because a family's
	// values may have a wire form encoding/json cannot produce — a protobuf
	// Timestamp is an RFC 3339 string under protojson, not a struct.
	Marshal   func(v any) ([]byte, error)
	Unmarshal func(data []byte, t reflect.Type) (any, error)
	// Resolve reads the value at a path inside a matched value, path relative
	// to it. It is what lets a condition look into the value — the generic
	// navigator stops at the family boundary the same way apply does, and
	// hands the rest of the path here. An error means the path holds nothing,
	// which an exists condition reports as false.
	Resolve func(v any, path string) (any, error)
}

var (
	familyMu   sync.Mutex
	familyList atomic.Pointer[[]Family]
	// familyVerdicts caches Match results per type: sync.Map because reads
	// vastly outnumber writes. The value is an int index into the family list,
	// or -1 for "no family".
	familyVerdicts sync.Map // reflect.Type -> int
)

// RegisterFamily adds a family. Families are consulted in registration order;
// the first whose Match accepts a type owns it.
//
// Registration is global and should happen during initialisation: registering
// is safe against concurrent use, but the verdict cache means a type seen
// before the registration keeps its earlier verdict.
func RegisterFamily(f Family) {
	familyMu.Lock()
	defer familyMu.Unlock()
	var next []Family
	if cur := familyList.Load(); cur != nil {
		next = append(next, *cur...)
	}
	next = append(next, f)
	familyList.Store(&next)
	// New family, stale verdicts: drop the cache rather than reason about
	// which entries it invalidates.
	familyVerdicts.Range(func(k, _ any) bool {
		familyVerdicts.Delete(k)
		return true
	})
}

// FamilyFor returns the family owning t, if any.
func FamilyFor(t reflect.Type) (*Family, bool) {
	families := familyList.Load()
	if families == nil || len(*families) == 0 {
		return nil, false
	}
	if idx, ok := familyVerdicts.Load(t); ok {
		i := idx.(int)
		if i < 0 {
			return nil, false
		}
		return &(*families)[i], true
	}
	for i := range *families {
		if (*families)[i].Match(t) {
			familyVerdicts.Store(t, i)
			return &(*families)[i], true
		}
	}
	familyVerdicts.Store(t, -1)
	return nil, false
}

// FamiliesRegistered reports whether any family exists, so hot paths can skip
// family handling entirely in the common case of none.
func FamiliesRegistered() bool {
	families := familyList.Load()
	return families != nil && len(*families) > 0
}
