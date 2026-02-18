package deep

import (
	"encoding/gob"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/brunoga/deep/v3/cond"
)

// OpKind represents the type of operation in a patch.
type OpKind int

const (
	OpAdd OpKind = iota
	OpRemove
	OpReplace
	OpMove
	OpCopy
	OpTest
	OpLog
)

func (k OpKind) String() string {
	switch k {
	case OpAdd:
		return "add"
	case OpRemove:
		return "remove"
	case OpReplace:
		return "replace"
	case OpMove:
		return "move"
	case OpCopy:
		return "copy"
	case OpTest:
		return "test"
	case OpLog:
		return "log"
	default:
		return "unknown"
	}
}

// Patch represents a set of changes that can be applied to a value of type T.
type Patch[T any] interface {
	fmt.Stringer

	// Apply applies the patch to the value pointed to by v.
	// The value v must not be nil.
	Apply(v *T)

	// ApplyChecked applies the patch only if specific conditions are met.
	// 1. If the patch has a global Condition, it must evaluate to true.
	// 2. If Strict mode is enabled, every modification must match the 'oldVal' recorded in the patch.
	// 3. Any local per-field conditions must evaluate to true.
	ApplyChecked(v *T) error

	// ApplyResolved applies the patch using a custom ConflictResolver.
	// This is used for convergent synchronization (CRDTs).
	ApplyResolved(v *T, r ConflictResolver) error

	// Walk calls fn for every operation in the patch.
	// The path is a JSON Pointer dot-notation path (e.g. "/Field/SubField/0").
	// If fn returns an error, walking stops and that error is returned.
	Walk(fn func(path string, op OpKind, old, new any) error) error

	// WithCondition returns a new Patch with the given global condition attached.
	WithCondition(c cond.Condition[T]) Patch[T]

	// WithStrict returns a new Patch with the strict consistency check enabled or disabled.
	WithStrict(strict bool) Patch[T]

	// Reverse returns a new Patch that undoes the changes in this patch.
	Reverse() Patch[T]

	// ToJSONPatch returns an RFC 6902 compliant JSON Patch representation of this patch.
	ToJSONPatch() ([]byte, error)

	// Summary returns a human-readable summary of the changes in the patch.
	Summary() string
}

// NewPatch returns a new, empty patch for type T.
func NewPatch[T any]() Patch[T] {
	return &typedPatch[T]{}
}

// Register registers the Patch implementation for type T with the gob package.
// This is required if you want to use Gob serialization with Patch[T].
func Register[T any]() {
	gob.Register(&typedPatch[T]{})
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
	cond   cond.Condition[T]
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
	if p.cond != nil {
		ok, err := p.cond.Evaluate(v)
		if err != nil {
			return &ApplyError{errors: []error{fmt.Errorf("condition evaluation failed: %w", err)}}
		}
		if !ok {
			return &ApplyError{errors: []error{fmt.Errorf("condition failed")}}
		}
	}

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

func (p *typedPatch[T]) WithCondition(c cond.Condition[T]) Patch[T] {
	return &typedPatch[T]{
		inner:  p.inner,
		cond:   c,
		strict: p.strict,
	}
}

func (p *typedPatch[T]) WithStrict(strict bool) Patch[T] {
	return &typedPatch[T]{
		inner:  p.inner,
		cond:   p.cond,
		strict: strict,
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

func (p *typedPatch[T]) MarshalJSON() ([]byte, error) {
	inner, err := marshalDiffPatch(p.inner)
	if err != nil {
		return nil, err
	}
	c, err := cond.MarshalCondition(p.cond)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"inner":  inner,
		"cond":   c,
		"strict": p.strict,
	})
}

func (p *typedPatch[T]) UnmarshalJSON(data []byte) error {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	if innerData, ok := m["inner"]; ok && len(innerData) > 0 && string(innerData) != "null" {
		inner, err := unmarshalDiffPatch(innerData)
		if err != nil {
			return err
		}
		p.inner = inner
	}
	if condData, ok := m["cond"]; ok && len(condData) > 0 && string(condData) != "null" {
		c, err := cond.UnmarshalCondition[T](condData)
		if err != nil {
			return err
		}
		p.cond = c
	}
	if strictData, ok := m["strict"]; ok {
		json.Unmarshal(strictData, &p.strict)
	}
	return nil
}

func (p *typedPatch[T]) GobEncode() ([]byte, error) {
	inner, err := marshalDiffPatch(p.inner)
	if err != nil {
		return nil, err
	}
	c, err := cond.MarshalCondition(p.cond)
	if err != nil {
		return nil, err
	}
	// Note: We use json-like map for consistency with surrogates
	return json.Marshal(map[string]any{
		"inner":  inner,
		"cond":   c,
		"strict": p.strict,
	})
}

func (p *typedPatch[T]) GobDecode(data []byte) error {
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	if innerData, ok := m["inner"]; ok && innerData != nil {
		inner, err := convertFromSurrogate(innerData)
		if err != nil {
			return err
		}
		p.inner = inner
	}
	if condData, ok := m["cond"]; ok && condData != nil {
		c, err := cond.ConvertFromCondSurrogate[T](condData)
		if err != nil {
			return err
		}
		p.cond = c
	}
	if strict, ok := m["strict"].(bool); ok {
		p.strict = strict
	}
	return nil
}
