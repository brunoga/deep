package deep

import (
	"fmt"
	"github.com/brunoga/deep/v5/internal/engine"
)

// Diff compares two values and returns a Patch describing the changes from a to b.
// Generated types (produced by deep-gen) dispatch to a reflection-free implementation.
// For other types, Diff falls back to the reflection engine which may return an error
// for unsupported kinds (chan, func, etc.).
func Diff[T any](a, b T) (Patch[T], error) {
	// 1. Try generated optimized path (pointer receiver, pointer arg)
	if differ, ok := any(&a).(interface {
		Diff(*T) Patch[T]
	}); ok {
		return differ.Diff(&b), nil
	}

	// 2. Try hand-written Diff with value arg (e.g. crdt.Text)
	if differ, ok := any(a).(interface {
		Diff(T) Patch[T]
	}); ok {
		return differ.Diff(b), nil
	}

	// 3. Fallback to reflection engine
	p, err := engine.Diff(a, b)
	if err != nil {
		return Patch[T]{}, fmt.Errorf("deep.Diff: %w", err)
	}
	if p == nil {
		return Patch[T]{}, nil
	}

	res := Patch[T]{}
	p.Walk(func(path string, op engine.OpKind, old, new any) error {
		res.Operations = append(res.Operations, Operation{
			Kind: op,
			Path: path,
			Old:  old,
			New:  new,
		})
		return nil
	})

	return res, nil
}

// Edit returns a Builder for constructing a Patch[T]. The target argument is
// used only for type inference and is not stored; the builder produces a
// standalone Patch, not a live view of the target.
func Edit[T any](_ *T) *Builder[T] {
	return &Builder[T]{}
}

// Op is a pending patch operation. Obtain one from [Set], [Add], [Remove],
// [Move], or [Copy]; attach per-operation conditions with [Op.If] or
// [Op.Unless] before passing to [Builder.With].
type Op struct {
	op Operation
}

// If attaches a condition that must hold for this operation to be applied.
func (o Op) If(c *Condition) Op {
	o.op.If = c
	return o
}

// Unless attaches a condition that must NOT hold for this operation to be applied.
func (o Op) Unless(c *Condition) Op {
	o.op.Unless = c
	return o
}

// Set returns a type-safe replace operation.
func Set[T, V any](p Path[T, V], val V) Op {
	return Op{op: Operation{Kind: OpReplace, Path: p.String(), New: val}}
}

// Add returns a type-safe add (insert) operation.
func Add[T, V any](p Path[T, V], val V) Op {
	return Op{op: Operation{Kind: OpAdd, Path: p.String(), New: val}}
}

// Remove returns a type-safe remove operation.
func Remove[T, V any](p Path[T, V]) Op {
	return Op{op: Operation{Kind: OpRemove, Path: p.String()}}
}

// Move returns a type-safe move operation that relocates the value at from to to.
// Both paths must share the same value type V.
func Move[T, V any](from, to Path[T, V]) Op {
	return Op{op: Operation{Kind: OpMove, Path: to.String(), Old: from.String()}}
}

// Copy returns a type-safe copy operation that duplicates the value at from to to.
// Both paths must share the same value type V.
func Copy[T, V any](from, to Path[T, V]) Op {
	return Op{op: Operation{Kind: OpCopy, Path: to.String(), Old: from.String()}}
}

// Builder constructs a [Patch] via a fluent chain.
type Builder[T any] struct {
	global *Condition
	ops    []Operation
}

// Where sets the global guard condition on the patch. If Where has already been
// called, the new condition is ANDed with the existing one rather than
// replacing it — calling Where twice is equivalent to Where(And(c1, c2)).
func (b *Builder[T]) Where(c *Condition) *Builder[T] {
	if b.global == nil {
		b.global = c
	} else {
		b.global = And(b.global, c)
	}
	return b
}

// With appends one or more operations to the patch being built.
// Obtain operations from the typed constructors [Set], [Add], [Remove],
// [Move], and [Copy]; per-operation conditions can be attached with
// [Op.If] and [Op.Unless] before passing here.
func (b *Builder[T]) With(ops ...Op) *Builder[T] {
	for _, o := range ops {
		b.ops = append(b.ops, o.op)
	}
	return b
}

// Log appends a log operation.
func (b *Builder[T]) Log(msg string) *Builder[T] {
	b.ops = append(b.ops, Operation{
		Kind: OpLog,
		Path: "/",
		New:  msg,
	})
	return b
}

// Build assembles and returns the completed Patch.
func (b *Builder[T]) Build() Patch[T] {
	return Patch[T]{
		Guard:      b.global,
		Operations: b.ops,
	}
}

// Eq creates an equality condition.
func Eq[T, V any](p Path[T, V], val V) *Condition {
	return &Condition{Path: p.String(), Op: "==", Value: val}
}

// Ne creates a non-equality condition.
func Ne[T, V any](p Path[T, V], val V) *Condition {
	return &Condition{Path: p.String(), Op: "!=", Value: val}
}

// Gt creates a greater-than condition.
func Gt[T, V any](p Path[T, V], val V) *Condition {
	return &Condition{Path: p.String(), Op: ">", Value: val}
}

// Ge creates a greater-than-or-equal condition.
func Ge[T, V any](p Path[T, V], val V) *Condition {
	return &Condition{Path: p.String(), Op: ">=", Value: val}
}

// Lt creates a less-than condition.
func Lt[T, V any](p Path[T, V], val V) *Condition {
	return &Condition{Path: p.String(), Op: "<", Value: val}
}

// Le creates a less-than-or-equal condition.
func Le[T, V any](p Path[T, V], val V) *Condition {
	return &Condition{Path: p.String(), Op: "<=", Value: val}
}

// Exists creates a condition that checks if a path exists.
func Exists[T, V any](p Path[T, V]) *Condition {
	return &Condition{Path: p.String(), Op: "exists"}
}

// In creates a condition that checks if a value is in a list.
func In[T, V any](p Path[T, V], vals []V) *Condition {
	return &Condition{Path: p.String(), Op: "in", Value: vals}
}

// Matches creates a regex condition.
func Matches[T, V any](p Path[T, V], regex string) *Condition {
	return &Condition{Path: p.String(), Op: "matches", Value: regex}
}

// Type creates a type-check condition.
func Type[T, V any](p Path[T, V], typeName string) *Condition {
	return &Condition{Path: p.String(), Op: "type", Value: typeName}
}

// And combines multiple conditions with logical AND.
func And(conds ...*Condition) *Condition {
	return &Condition{Op: "and", Sub: conds}
}

// Or combines multiple conditions with logical OR.
func Or(conds ...*Condition) *Condition {
	return &Condition{Op: "or", Sub: conds}
}

// Not inverts a condition.
func Not(c *Condition) *Condition {
	return &Condition{Op: "not", Sub: []*Condition{c}}
}
