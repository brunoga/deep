package engine

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// OpKind is the type of an operation in a patch. It is a string, and the
// string is the wire form: a serialized operation says "replace", not a number
// whose meaning depends on the order the kinds were declared in. That also
// makes the zero value invalid rather than silently meaning add.
type OpKind string

const (
	OpAdd     OpKind = "add"
	OpRemove  OpKind = "remove"
	OpReplace OpKind = "replace"
	OpMove    OpKind = "move"
	OpCopy    OpKind = "copy"
	OpLog     OpKind = "log"
	// OpAlias makes Path hold the same object From resolves to — sharing,
	// where OpCopy makes an independent deep copy. Diff emits it for the
	// second and later routes to a value that is referenced more than once, so
	// applying the patch rebuilds the sharing the new value has.
	OpAlias OpKind = "alias"
)

func (k OpKind) String() string {
	if k == "" {
		return "invalid"
	}
	return string(k)
}

// UnmarshalJSON accepts the string form, and also the small integers v5 wrote —
// a patch stored before the change should still be readable, even though v6
// will never write one like it.
func (k *OpKind) UnmarshalJSON(data []byte) error {
	if len(data) > 0 && data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		*k = OpKind(s)
		return nil
	}
	var n int
	if err := json.Unmarshal(data, &n); err != nil {
		return fmt.Errorf("operation kind must be a string or a v5 integer: %s", data)
	}
	v5 := []OpKind{OpAdd, OpRemove, OpReplace, OpMove, OpCopy, OpLog, OpAlias}
	if n < 0 || n >= len(v5) {
		return fmt.Errorf("unknown v5 operation kind %d", n)
	}
	*k = v5[n]
	return nil
}

// Patch represents a set of changes that can be applied to a value of type T.
type Patch[T any] interface {
	fmt.Stringer

	// Apply applies the patch to the value pointed to by v.
	// The value v must not be nil.
	Apply(v *T)

	// ApplyChecked applies the patch only if specific conditions are met.
	// If Strict mode is enabled, every modification must match the 'oldVal' recorded in the patch.
	ApplyChecked(v *T) error

	// ApplyResolved applies the patch using a custom ConflictResolver.
	// This is used for convergent synchronization (CRDTs).
	ApplyResolved(v *T, r ConflictResolver) error

	// Walk calls fn for every operation in the patch.
	// The path is a JSON Pointer dot-notation path (e.g. "/Field/SubField/0").
	// If fn returns an error, walking stops and that error is returned.
	Walk(fn func(path string, op OpKind, old, new any) error) error

	// AsStrict returns a new Patch with strict mode enabled.
	AsStrict() Patch[T]

	// Reverse returns a new Patch that undoes the changes in this patch.
	Reverse() Patch[T]

	// ToJSONPatch returns an RFC 6902 compliant JSON Patch representation of this patch.
	ToJSONPatch() ([]byte, error)

	// Summary returns a human-readable summary of the changes in the patch.
	Summary() string
}

// ApplyError represents one or more errors that occurred during patch application.
type ApplyError struct {
	errors []error
}

func (e *ApplyError) Error() string {
	if len(e.errors) == 1 {
		return e.errors[0].Error()
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%d errors during apply:\n", len(e.errors)))
	for _, err := range e.errors {
		b.WriteString("- " + err.Error() + "\n")
	}
	return b.String()
}

func (e *ApplyError) Unwrap() []error {
	return e.errors
}

func (e *ApplyError) Errors() []error {
	return e.errors
}

type typedPatch[T any] struct {
	inner  diffPatch
	strict bool
}

type patchUnwrapper interface {
	unwrap() diffPatch
}

func (p *typedPatch[T]) unwrap() diffPatch {
	return p.inner
}

func (p *typedPatch[T]) Apply(v *T) {
	if p.inner == nil {
		return
	}
	rv := reflect.ValueOf(v).Elem()
	p.inner.apply(reflect.ValueOf(v), rv, "/")
}

func (p *typedPatch[T]) ApplyChecked(v *T) error {
	if p.inner == nil {
		return nil
	}

	rv := reflect.ValueOf(v).Elem()
	err := p.inner.applyChecked(reflect.ValueOf(v), rv, p.strict, "/")
	if err != nil {
		if ae, ok := err.(*ApplyError); ok {
			return ae
		}
		return &ApplyError{errors: []error{err}}
	}
	return nil
}

func (p *typedPatch[T]) ApplyResolved(v *T, r ConflictResolver) error {
	if p.inner == nil {
		return nil
	}

	rv := reflect.ValueOf(v).Elem()
	return p.inner.applyResolved(reflect.ValueOf(v), rv, "/", r)
}

func (p *typedPatch[T]) Walk(fn func(path string, op OpKind, old, new any) error) error {
	if p.inner == nil {
		return nil
	}

	return p.inner.walk("", func(path string, op OpKind, old, new any) error {
		fullPath := path
		if fullPath == "" {
			fullPath = "/"
		} else if fullPath[0] != '/' {
			fullPath = "/" + fullPath
		}

		return fn(fullPath, op, old, new)
	})
}

func (p *typedPatch[T]) AsStrict() Patch[T] {
	return &typedPatch[T]{
		inner:  p.inner,
		strict: true,
	}
}

func (p *typedPatch[T]) Reverse() Patch[T] {
	if p.inner == nil {
		return &typedPatch[T]{}
	}
	return &typedPatch[T]{
		inner:  p.inner.reverse(),
		strict: p.strict,
	}
}

func (p *typedPatch[T]) ToJSONPatch() ([]byte, error) {
	if p.inner == nil {
		return json.Marshal([]any{})
	}
	// We pass empty string because toJSONPatch prepends "/" when needed
	// and handles root as "/".
	return json.Marshal(p.inner.toJSONPatch(""))
}

func (p *typedPatch[T]) Summary() string {
	if p.inner == nil {
		return "No changes."
	}
	return p.inner.summary("/")
}

func (p *typedPatch[T]) String() string {
	if p.inner == nil {
		return "<nil>"
	}
	return p.inner.format(0)
}
