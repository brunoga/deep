package deep

import (
	"reflect"

	icore "github.com/brunoga/deep/v6/internal/core"
	"github.com/brunoga/deep/v6/internal/engine"
)

// TypeFamily is custom behaviour for every type matching a predicate, where
// [RegisterCustomEqual] and friends are custom behaviour for one concrete
// type.
//
// The distinction matters for types produced by another runtime. A protobuf
// application holds hundreds of generated message types; registering each one
// is not a usable interface, and every one needs the same treatment — the
// proto runtime's own equality, its own cloning, its own wire form. A family
// says that once: any type the predicate accepts is handled by these
// functions, however many such types exist.
//
// A family is all-or-nothing about its boundary. Once a value is matched,
// nothing generic looks inside it: Equal and Clone are the family's, Diff
// produces the family's operations, and an operation whose path crosses into a
// matched value is handed to the family's Apply with the remainder of the
// path. That is the point — the inside of a protobuf message is the proto
// runtime's territory, and walking its Go struct directly reads internal
// bookkeeping and, for Clone, corrupts it.
type TypeFamily struct {
	// Name identifies the family in errors and diagnostics, and joins the
	// family's handlers together. It must be unique among registered families.
	Name string
	// Match reports whether t belongs to the family. It is consulted once per
	// type and the verdict is cached, so it may be arbitrarily selective but
	// should not depend on anything that changes at run time.
	Match func(t reflect.Type) bool
	// Equal reports whether two matched values are equal, by the family's own
	// definition. Both arguments have the matched type.
	Equal func(a, b any) bool
	// Clone returns a deep copy of a matched value.
	Clone func(v any) any
	// Diff returns the operations turning a into b, with paths relative to the
	// value — "/" or "" for the whole value — using add, remove and replace
	// only. Returning no operations means the values do not differ.
	Diff func(a, b any) ([]Operation, error)
	// Apply applies one operation to target, which is a pointer to a matched
	// value. The operation's path is relative to the value. Apply honours
	// op.Strict by verifying op.Old before writing, the way the generic
	// applier does.
	Apply func(target any, op Operation) error
	// Marshal renders a matched value into its wire (JSON) form, and Unmarshal
	// reverses it for the given matched type. They exist because a family's
	// values may have a wire form encoding/json cannot produce: a protobuf
	// Timestamp is an RFC 3339 string under protojson, not a struct of
	// seconds and nanos.
	Marshal   func(v any) ([]byte, error)
	Unmarshal func(data []byte, t reflect.Type) (any, error)
}

// RegisterTypeFamily installs a family. Families are consulted in registration
// order; the first whose Match accepts a type owns it, and per-concrete-type
// registrations ([RegisterCustomEqual], [RegisterCustomClone],
// [RegisterCustomDiff]) take precedence over any family.
//
// Registration is global and should happen during initialisation.
func RegisterTypeFamily(f TypeFamily) {
	icore.RegisterFamily(icore.Family{
		Name:      f.Name,
		Match:     f.Match,
		Equal:     f.Equal,
		Clone:     f.Clone,
		Marshal:   f.Marshal,
		Unmarshal: f.Unmarshal,
	})
	if f.Diff != nil || f.Apply != nil {
		engine.RegisterFamilyOps(f.Name, f.Diff, f.Apply)
	}
}
