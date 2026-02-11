package deep

import (
	"encoding/gob"
	"encoding/json"
	"fmt"
	"reflect"
)

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

	// WithCondition returns a new Patch with the given global condition attached.
	WithCondition(c Condition[T]) Patch[T]

	// WithStrict returns a new Patch with the strict consistency check enabled or disabled.
	WithStrict(strict bool) Patch[T]

	// Reverse returns a new Patch that undoes the changes in this patch.
	Reverse() Patch[T]

	// ToJSONPatch returns an RFC 6902 compliant JSON Patch representation of this patch.
	ToJSONPatch() ([]byte, error)
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

type typedPatch[T any] struct {
	inner  diffPatch
	cond   Condition[T]
	strict bool
}

func (p *typedPatch[T]) Apply(v *T) {
	if p.inner == nil {
		return
	}
	rv := reflect.ValueOf(v).Elem()
	p.inner.apply(reflect.ValueOf(v), rv)
}

func (p *typedPatch[T]) ApplyChecked(v *T) error {
	if p.cond != nil {
		ok, err := p.cond.Evaluate(v)
		if err != nil {
			return fmt.Errorf("condition evaluation failed: %w", err)
		}
		if !ok {
			return fmt.Errorf("condition failed")
		}
	}

	if p.inner == nil {
		return nil
	}

	rv := reflect.ValueOf(v).Elem()
	return p.inner.applyChecked(reflect.ValueOf(v), rv, p.strict)
}

func (p *typedPatch[T]) WithCondition(c Condition[T]) Patch[T] {
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
	cond, err := marshalCondition(p.cond)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"inner":  inner,
		"cond":   cond,
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
		cond, err := unmarshalCondition[T](condData)
		if err != nil {
			return err
		}
		p.cond = cond
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
	cond, err := marshalCondition(p.cond)
	if err != nil {
		return nil, err
	}
	// Note: We use json-like map for consistency with surrogates
	return json.Marshal(map[string]any{
		"inner":  inner,
		"cond":   cond,
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
		cond, err := convertFromCondSurrogate[T](condData)
		if err != nil {
			return err
		}
		p.cond = cond
	}
	if strict, ok := m["strict"].(bool); ok {
		p.strict = strict
	}
	return nil
}
