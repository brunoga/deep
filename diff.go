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
		o := Operation{
			Kind: op,
			Path: path,
			New:  new,
		}
		// Internal walk emits the source path in `old` for Move/Copy; lift it
		// into the typed From field so Old stays free for prior values.
		if op == engine.OpMove || op == engine.OpCopy {
			if s, ok := old.(string); ok {
				o.From = s
			}
		} else {
			o.Old = old
		}
		res.Operations = append(res.Operations, o)
		return nil
	})

	return res, nil
}
