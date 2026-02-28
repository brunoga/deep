package v5

import (
	"fmt"
	"github.com/brunoga/deep/v5/internal/engine"
)

// Diff compares two values and returns a pure data Patch.
// In v5, this would delegate to generated code if available.
func Diff[T any](a, b T) Patch[T] {
	// 1. Try generated optimized path
	if differ, ok := any(&a).(interface {
		Diff(*T) Patch[T]
	}); ok {
		return differ.Diff(&b)
	}

	// 2. Fallback to v4 reflection engine
	p, err := engine.Diff(a, b)
	if err != nil || p == nil {
		return Patch[T]{}
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

	return res
}

// Edit provides a fluent, type-safe builder for creating patches.
func Edit[T any](target *T) *Builder[T] {
	return &Builder[T]{target: target}
}

// Builder allows for type-safe manual patch construction.
type Builder[T any] struct {
	target *T
	global *Condition
	ops    []Operation
}

// Where adds a global condition to the patch.
func (b *Builder[T]) Where(c *Condition) *Builder[T] {
	b.global = c
	return b
}

// If adds a condition to the last operation.
func (b *Builder[T]) If(c *Condition) *Builder[T] {
	if len(b.ops) > 0 {
		b.ops[len(b.ops)-1].If = c
	}
	return b
}

// Unless adds a negative condition to the last operation.
func (b *Builder[T]) Unless(c *Condition) *Builder[T] {
	if len(b.ops) > 0 {
		b.ops[len(b.ops)-1].Unless = c
	}
	return b
}

// Set adds a set operation to the builder (method for fluent chaining).
func (b *Builder[T]) Set(p fmt.Stringer, val any) *Builder[T] {
	b.ops = append(b.ops, Operation{
		Kind: OpReplace,
		Path: p.String(),
		New:  val,
	})
	return b
}

// Add adds an add operation to the builder (method for fluent chaining).
func (b *Builder[T]) Add(p fmt.Stringer, val any) *Builder[T] {
	b.ops = append(b.ops, Operation{
		Kind: OpAdd,
		Path: p.String(),
		New:  val,
	})
	return b
}

// Remove adds a remove operation to the builder (method for fluent chaining).
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

// Log adds a log operation to the builder.
func (b *Builder[T]) Log(msg string) *Builder[T] {
	b.ops = append(b.ops, Operation{
		Kind: OpLog,
		Path: "/",
		New:  msg,
	})
	return b
}

func (b *Builder[T]) Build() Patch[T] {
	return Patch[T]{
		Condition:  b.global,
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

// Lt creates a less-than condition.
func Lt[T, V any](p Path[T, V], val V) *Condition {
	return &Condition{Path: p.String(), Op: "<", Value: val}
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
	return &Condition{Op: "and", Apply: conds}
}

// Or combines multiple conditions with logical OR.
func Or(conds ...*Condition) *Condition {
	return &Condition{Op: "or", Apply: conds}
}

// Not inverts a condition.
func Not(c *Condition) *Condition {
	return &Condition{Op: "not", Apply: []*Condition{c}}
}
