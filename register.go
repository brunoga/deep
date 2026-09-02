package deep

import (
	"reflect"

	icore "github.com/brunoga/deep/v5/internal/core"
	"github.com/brunoga/deep/v5/internal/engine"
)

// Custom behaviour for a single type.
//
// A type can already carry its own behaviour by implementing a method: Equal,
// Clone, Diff or Copy. Registration is for the cases where that is not
// possible — a type from another module, or one whose method set you do not
// control — and for overriding what a type does say about itself.
//
// Registration is global and applies to every subsequent operation in the
// process. Register during initialisation, before the first concurrent use:
// registering is safe against a diff running in parallel, but two goroutines
// registering the same type race with each other in the sense that the last
// one wins, which is rarely what either intended.
//
// These affect the reflection engine. Generated code has its own comparison
// and copying inlined and will not consult the registry for a type it handles
// itself, so a registration for such a type applies only where it is reached
// through reflection. Registering behaviour for a type you also generate code
// for is a way to make the two paths disagree; prefer a method, which both
// honour.

// RegisterCustomEqual installs fn as the equality test for T.
func RegisterCustomEqual[T any](fn func(a, b T) bool) {
	var t T
	icore.RegisterCustomEqual(reflect.TypeOf(t), reflect.ValueOf(
		func(a, b T) bool { return fn(a, b) },
	))
}

// RegisterCustomClone installs fn as the deep copy for T.
//
// fn must return a value that shares no mutable state with its argument;
// returning the argument makes Clone a shallow copy for that type, with no
// warning.
func RegisterCustomClone[T any](fn func(src T) (T, error)) {
	var t T
	icore.RegisterCustomCopy(reflect.TypeOf(t), reflect.ValueOf(
		func(src T) (T, error) { return fn(src) },
	))
}

// RegisterCustomDiff installs fn as the diff for T.
//
// The patch fn returns is spliced into the enclosing patch at the path where
// the value was reached, so its operation paths are relative to the value
// rather than to the root. Returning an empty patch means no difference.
func RegisterCustomDiff[T any](fn func(a, b T) (Patch[T], error)) {
	engine.RegisterCustomDiff(func(a, b T) (engine.Patch[T], error) {
		p, err := fn(a, b)
		if err != nil {
			return nil, err
		}
		return patchAdapter[T]{p}, nil
	})
}

// patchAdapter presents a deep.Patch as the engine's internal patch interface,
// which is what RegisterCustomDiff hands back to the differ.
type patchAdapter[T any] struct {
	p Patch[T]
}

func (a patchAdapter[T]) String() string { return a.p.String() }

func (a patchAdapter[T]) Apply(v *T) { _ = Apply(v, a.p) }

func (a patchAdapter[T]) ApplyChecked(v *T) error { return Apply(v, a.p) }

func (a patchAdapter[T]) ApplyResolved(v *T, r engine.ConflictResolver) error {
	return Apply(v, a.p)
}

func (a patchAdapter[T]) Walk(fn func(path string, op OpKind, old, new any) error) error {
	for _, o := range a.p.Operations {
		if err := fn(o.Path, o.Kind, o.Old, o.New); err != nil {
			return err
		}
	}
	return nil
}

func (a patchAdapter[T]) AsStrict() engine.Patch[T] {
	strict := a.p
	strict.Strict = true
	return patchAdapter[T]{strict}
}

func (a patchAdapter[T]) Reverse() engine.Patch[T] {
	return patchAdapter[T]{a.p.Reverse()}
}

func (a patchAdapter[T]) ToJSONPatch() ([]byte, error) { return a.p.ToJSONPatch() }

func (a patchAdapter[T]) Summary() string { return a.p.String() }
