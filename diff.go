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
		// Skip OpTest as v5 handles tests via conditions
		if op == engine.OpTest {
			return nil
		}

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

// Builder allows for type-safe manual patch construction.
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

// If attaches a condition to the most recently added operation.
// It is a no-op if no operations have been added yet.
func (b *Builder[T]) If(c *Condition) *Builder[T] {
	if len(b.ops) > 0 {
		b.ops[len(b.ops)-1].If = c
	}
	return b
}

// Unless attaches a negative condition to the most recently added operation.
// It is a no-op if no operations have been added yet.
func (b *Builder[T]) Unless(c *Condition) *Builder[T] {
	if len(b.ops) > 0 {
		b.ops[len(b.ops)-1].Unless = c
	}
	return b
}

// Set adds a replace operation. For compile-time type checking, prefer the
// package-level Set[T, V] function.
func (b *Builder[T]) Set(p fmt.Stringer, val any) *Builder[T] {
	b.ops = append(b.ops, Operation{
		Kind: OpReplace,
		Path: p.String(),
		New:  val,
	})
	return b
}

// Add adds an insert operation. For compile-time type checking, prefer the
// package-level Add[T, V] function.
func (b *Builder[T]) Add(p fmt.Stringer, val any) *Builder[T] {
	b.ops = append(b.ops, Operation{
		Kind: OpAdd,
		Path: p.String(),
		New:  val,
	})
	return b
}

// Remove adds a delete operation. For compile-time type checking, prefer the
// package-level Remove[T, V] function.
func (b *Builder[T]) Remove(p fmt.Stringer) *Builder[T] {
	b.ops = append(b.ops, Operation{
		Kind: OpRemove,
		Path: p.String(),
	})
	return b
}

// Set adds a type-safe set operation to the builder.
func Set[T, V any](b *Builder[T], p Path[T, V], val V) *Builder[T] {
	return b.Set(p, val)
}

// Add adds a type-safe add operation to the builder.
func Add[T, V any](b *Builder[T], p Path[T, V], val V) *Builder[T] {
	return b.Add(p, val)
}

// Remove adds a type-safe remove operation to the builder.
func Remove[T, V any](b *Builder[T], p Path[T, V]) *Builder[T] {
	return b.Remove(p)
}

// Move adds a move operation that relocates the value at from to the destination path.
func (b *Builder[T]) Move(from, to fmt.Stringer) *Builder[T] {
	b.ops = append(b.ops, Operation{
		Kind: OpMove,
		Path: to.String(),
		Old:  from.String(),
	})
	return b
}

// Copy adds a copy operation that duplicates the value at from to the destination path.
func (b *Builder[T]) Copy(from, to fmt.Stringer) *Builder[T] {
	b.ops = append(b.ops, Operation{
		Kind: OpCopy,
		Path: to.String(),
		Old:  from.String(),
	})
	return b
}

// Log adds a log operation to the builder.
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

// Log creates a condition that logs a message.
func Log[T, V any](p Path[T, V], msg string) *Condition {
	return &Condition{Path: p.String(), Op: "log", Value: msg}
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
