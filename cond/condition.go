package cond

import (
	"encoding/json"

	"github.com/brunoga/deep/v4/internal/core"
)

// Condition represents a logical check against a value of type T.
type Condition[T any] interface {
	// Evaluate evaluates the condition against the given value.
	Evaluate(v *T) (bool, error)

	// MarshalJSON returns the JSON representation of the condition.
	MarshalJSON() ([]byte, error)

	InternalCondition
}

// InternalCondition is an internal interface for efficient evaluation without reflection.
type InternalCondition interface {
	EvaluateAny(v any) (bool, error)
	Paths() []string
	WithRelativePath(prefix string) InternalCondition
}

// typedCondition wraps a InternalCondition to satisfy Condition[T].
type typedCondition[T any] struct {
	inner InternalCondition
}

func (c *typedCondition[T]) Evaluate(v *T) (bool, error) {
	return c.inner.EvaluateAny(v)
}

func (c *typedCondition[T]) EvaluateAny(v any) (bool, error) {
	return c.inner.EvaluateAny(v)
}

func (c *typedCondition[T]) Paths() []string {
	return c.inner.Paths()
}

func (c *typedCondition[T]) WithRelativePath(prefix string) InternalCondition {
	return c.inner.WithRelativePath(prefix)
}

func (c *typedCondition[T]) MarshalJSON() ([]byte, error) {
	s, err := MarshalConditionAny(c.inner)
	if err != nil {
		return nil, err
	}
	return json.Marshal(s)
}

// Eq returns a condition that checks if the value at the path is equal to the given value.
func Eq[T any](p string, val any) Condition[T] {
	return &typedCondition[T]{
		inner: &rawCompareCondition{Path: core.DeepPath(p), Val: val, Op: "=="},
	}
}

// EqFold returns a condition that checks if the value at the path is equal to the given value (case-insensitive).
func EqFold[T any](p string, val any) Condition[T] {
	return &typedCondition[T]{
		inner: &rawCompareCondition{Path: core.DeepPath(p), Val: val, Op: "==", IgnoreCase: true},
	}
}

// Ne returns a condition that checks if the value at the path is not equal to the given value.
func Ne[T any](p string, val any) Condition[T] {
	return &typedCondition[T]{
		inner: &rawCompareCondition{Path: core.DeepPath(p), Val: val, Op: "!="},
	}
}

// NeFold returns a condition that checks if the value at the path is not equal to the given value (case-insensitive).
func NeFold[T any](p string, val any) Condition[T] {
	return &typedCondition[T]{
		inner: &rawCompareCondition{Path: core.DeepPath(p), Val: val, Op: "!=", IgnoreCase: true},
	}
}

// Greater returns a condition that checks if the value at the path is greater than the given value.
func Greater[T any](p string, val any) Condition[T] {
	return &typedCondition[T]{
		inner: &rawCompareCondition{Path: core.DeepPath(p), Val: val, Op: ">"},
	}
}

// Less returns a condition that checks if the value at the path is less than the given value.
func Less[T any](p string, val any) Condition[T] {
	return &typedCondition[T]{
		inner: &rawCompareCondition{Path: core.DeepPath(p), Val: val, Op: "<"},
	}
}

// GreaterEqual returns a condition that checks if the value at the path is greater than or equal to the given value.
func GreaterEqual[T any](p string, val any) Condition[T] {
	return &typedCondition[T]{
		inner: &rawCompareCondition{Path: core.DeepPath(p), Val: val, Op: ">="},
	}
}

// LessEqual returns a condition that checks if the value at the path is less than or equal to the given value.
func LessEqual[T any](p string, val any) Condition[T] {
	return &typedCondition[T]{
		inner: &rawCompareCondition{Path: core.DeepPath(p), Val: val, Op: "<="},
	}
}

// Defined returns a condition that checks if the value at the path is defined.
func Defined[T any](p string) Condition[T] {
	return &typedCondition[T]{
		inner: &rawDefinedCondition{Path: core.DeepPath(p)},
	}
}

// Undefined returns a condition that checks if the value at the path is undefined.
func Undefined[T any](p string) Condition[T] {
	return &typedCondition[T]{
		inner: &rawUndefinedCondition{Path: core.DeepPath(p)},
	}
}

// Matches returns a condition that checks if the string value at the path matches the given regex pattern.
func Matches[T any](p, pattern string) Condition[T] {
	return &typedCondition[T]{
		inner: &rawStringCondition{Path: core.DeepPath(p), Val: pattern, Op: "matches"},
	}
}

// MatchesFold returns a condition that checks if the string value at the path matches the given regex pattern (case-insensitive).
func MatchesFold[T any](p, pattern string) Condition[T] {
	return &typedCondition[T]{
		inner: &rawStringCondition{Path: core.DeepPath(p), Val: pattern, Op: "matches", IgnoreCase: true},
	}
}

// StartsWith returns a condition that checks if the string value at the path starts with the given prefix.
func StartsWith[T any](p, val string) Condition[T] {
	return &typedCondition[T]{
		inner: &rawStringCondition{Path: core.DeepPath(p), Val: val, Op: "starts"},
	}
}

// StartsWithFold returns a condition that checks if the string value at the path starts with the given prefix (case-insensitive).
func StartsWithFold[T any](p, val string) Condition[T] {
	return &typedCondition[T]{
		inner: &rawStringCondition{Path: core.DeepPath(p), Val: val, Op: "starts", IgnoreCase: true},
	}
}

// EndsWith returns a condition that checks if the string value at the path ends with the given suffix.
func EndsWith[T any](p, val string) Condition[T] {
	return &typedCondition[T]{
		inner: &rawStringCondition{Path: core.DeepPath(p), Val: val, Op: "ends"},
	}
}

// EndsWithFold returns a condition that checks if the string value at the path ends with the given suffix (case-insensitive).
func EndsWithFold[T any](p, val string) Condition[T] {
	return &typedCondition[T]{
		inner: &rawStringCondition{Path: core.DeepPath(p), Val: val, Op: "ends", IgnoreCase: true},
	}
}

// Contains returns a condition that checks if the string value at the path contains the given substring.
func Contains[T any](p, val string) Condition[T] {
	return &typedCondition[T]{
		inner: &rawStringCondition{Path: core.DeepPath(p), Val: val, Op: "contains"},
	}
}

// ContainsFold returns a condition that checks if the string value at the path contains the given substring (case-insensitive).
func ContainsFold[T any](p, val string) Condition[T] {
	return &typedCondition[T]{
		inner: &rawStringCondition{Path: core.DeepPath(p), Val: val, Op: "contains", IgnoreCase: true},
	}
}

// In returns a condition that checks if the value at the path is one of the given values.
func In[T any](p string, values ...any) Condition[T] {
	return &typedCondition[T]{
		inner: &rawInCondition{Path: core.DeepPath(p), Values: values},
	}
}

// InFold returns a condition that checks if the value at the path is one of the given values (case-insensitive).
func InFold[T any](p string, values ...any) Condition[T] {
	return &typedCondition[T]{
		inner: &rawInCondition{Path: core.DeepPath(p), Values: values, IgnoreCase: true},
	}
}

// Type returns a condition that checks if the value at the path has the given type.
func Type[T any](p, typeName string) Condition[T] {
	return &typedCondition[T]{
		inner: &rawTypeCondition{Path: core.DeepPath(p), TypeName: typeName},
	}
}

// Log returns a condition that logs the given message during evaluation.
func Log[T any](message string) Condition[T] {
	return &typedCondition[T]{
		inner: &rawLogCondition{Message: message},
	}
}

// And returns a condition that represents a logical AND of multiple conditions.
func And[T any](conds ...Condition[T]) Condition[T] {
	var innerConds []InternalCondition
	for _, c := range conds {
		if t, ok := c.(*typedCondition[T]); ok {
			innerConds = append(innerConds, t.inner)
		} else {
			innerConds = append(innerConds, c)
		}
	}
	return &typedCondition[T]{
		inner: &rawAndCondition{Conditions: innerConds},
	}
}

// Or returns a condition that represents a logical OR of multiple conditions.
func Or[T any](conds ...Condition[T]) Condition[T] {
	var innerConds []InternalCondition
	for _, c := range conds {
		if t, ok := c.(*typedCondition[T]); ok {
			innerConds = append(innerConds, t.inner)
		} else {
			innerConds = append(innerConds, c)
		}
	}
	return &typedCondition[T]{
		inner: &rawOrCondition{Conditions: innerConds},
	}
}

// Not returns a condition that represents a logical NOT of a condition.
func Not[T any](c Condition[T]) Condition[T] {
	var inner InternalCondition
	if t, ok := c.(*typedCondition[T]); ok {
		inner = t.inner
	} else {
		inner = c
	}
	return &typedCondition[T]{
		inner: &rawNotCondition{C: inner},
	}
}

// EqualField returns a condition that checks if the value at path1 is equal to the value at path2.
func EqualField[T any](path1, path2 string) Condition[T] {
	return &typedCondition[T]{
		inner: &rawCompareFieldCondition{Path1: core.DeepPath(path1), Path2: core.DeepPath(path2), Op: "=="},
	}
}

// NotEqualField returns a condition that checks if the value at path1 is not equal to the value at path2.
func NotEqualField[T any](path1, path2 string) Condition[T] {
	return &typedCondition[T]{
		inner: &rawCompareFieldCondition{Path1: core.DeepPath(path1), Path2: core.DeepPath(path2), Op: "!="},
	}
}

// GreaterField returns a condition that checks if the value at path1 is greater than the value at path2.
func GreaterField[T any](path1, path2 string) Condition[T] {
	return &typedCondition[T]{
		inner: &rawCompareFieldCondition{Path1: core.DeepPath(path1), Path2: core.DeepPath(path2), Op: ">"},
	}
}

// LessField returns a condition that checks if the value at path1 is less than the value at path2.
func LessField[T any](path1, path2 string) Condition[T] {
	return &typedCondition[T]{
		inner: &rawCompareFieldCondition{Path1: core.DeepPath(path1), Path2: core.DeepPath(path2), Op: "<"},
	}
}

// GreaterEqualField returns a condition that checks if the value at path1 is greater than or equal to the value at path2.
func GreaterEqualField[T any](path1, path2 string) Condition[T] {
	return &typedCondition[T]{
		inner: &rawCompareFieldCondition{Path1: core.DeepPath(path1), Path2: core.DeepPath(path2), Op: ">="},
	}
}

// LessEqualField returns a condition that checks if the value at path1 is less than or equal to the value at path2.
func LessEqualField[T any](path1, path2 string) Condition[T] {
	return &typedCondition[T]{
		inner: &rawCompareFieldCondition{Path1: core.DeepPath(path1), Path2: core.DeepPath(path2), Op: "<="},
	}
}

// EqualFieldFold returns a condition that checks if the value at path1 is equal to the value at path2 (case-insensitive).
func EqualFieldFold[T any](path1, path2 string) Condition[T] {
	return &typedCondition[T]{
		inner: &rawCompareFieldCondition{Path1: core.DeepPath(path1), Path2: core.DeepPath(path2), Op: "==", IgnoreCase: true},
	}
}

// NotEqualFieldFold returns a condition that checks if the value at path1 is not equal to the value at path2 (case-insensitive).
func NotEqualFieldFold[T any](path1, path2 string) Condition[T] {
	return &typedCondition[T]{
		inner: &rawCompareFieldCondition{Path1: core.DeepPath(path1), Path2: core.DeepPath(path2), Op: "!=", IgnoreCase: true},
	}
}
