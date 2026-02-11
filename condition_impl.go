package deep

import (
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strings"
)

type internalConditionImpl interface {
	evaluateAny(v any) (bool, error)
	paths() []Path
	withRelativeParts(prefix []pathPart) internalConditionImpl
}

type rawDefinedCondition struct {
	Path Path
}

func (c *rawDefinedCondition) evaluateAny(v any) (bool, error) {
	rv := toReflectValue(v)
	target, err := c.Path.resolve(rv)
	if err != nil {
		return false, nil
	}
	return target.IsValid(), nil
}

func (c *rawDefinedCondition) paths() []Path { return []Path{c.Path} }

func (c *rawDefinedCondition) withRelativeParts(prefix []pathPart) internalConditionImpl {
	return &rawDefinedCondition{Path: c.Path.stripParts(prefix)}
}

type rawUndefinedCondition struct {
	Path Path
}

func (c *rawUndefinedCondition) evaluateAny(v any) (bool, error) {
	rv := toReflectValue(v)
	target, err := c.Path.resolve(rv)
	if err != nil {
		return true, nil
	}
	return !target.IsValid(), nil
}

func (c *rawUndefinedCondition) paths() []Path { return []Path{c.Path} }

func (c *rawUndefinedCondition) withRelativeParts(prefix []pathPart) internalConditionImpl {
	return &rawUndefinedCondition{Path: c.Path.stripParts(prefix)}
}

type rawTypeCondition struct {
	Path     Path
	TypeName string
}

func (c *rawTypeCondition) evaluateAny(v any) (bool, error) {
	rv := toReflectValue(v)
	target, err := c.Path.resolve(rv)
	if err != nil {
		return c.TypeName == "undefined", nil
	}
	if !target.IsValid() {
		return c.TypeName == "null" || c.TypeName == "undefined", nil
	}

	switch c.TypeName {
	case "string":
		return target.Kind() == reflect.String, nil
	case "number":
		k := target.Kind()
		return (k >= reflect.Int && k <= reflect.Int64) ||
			(k >= reflect.Uint && k <= reflect.Uintptr) ||
			(k == reflect.Float32 || k == reflect.Float64), nil
	case "boolean":
		return target.Kind() == reflect.Bool, nil
	case "object":
		return target.Kind() == reflect.Struct || target.Kind() == reflect.Map, nil
	case "array":
		return target.Kind() == reflect.Slice || target.Kind() == reflect.Array, nil
	case "null":
		k := target.Kind()
		return (k == reflect.Ptr || k == reflect.Interface || k == reflect.Slice || k == reflect.Map) && target.IsNil(), nil
	case "undefined":
		return false, nil
	default:
		return false, fmt.Errorf("unknown type: %s", c.TypeName)
	}
}

func (c *rawTypeCondition) paths() []Path { return []Path{c.Path} }

func (c *rawTypeCondition) withRelativeParts(prefix []pathPart) internalConditionImpl {
	return &rawTypeCondition{Path: c.Path.stripParts(prefix), TypeName: c.TypeName}
}

type rawStringCondition struct {
	Path       Path
	Val        string
	Op         string
	IgnoreCase bool
}

func (c *rawStringCondition) evaluateAny(v any) (bool, error) {
	rv := toReflectValue(v)
	target, err := c.Path.resolve(rv)
	if err != nil {
		return false, nil
	}
	if !target.IsValid() || target.Kind() != reflect.String {
		return false, nil
	}
	s := target.String()
	val := c.Val
	if c.IgnoreCase && c.Op != "matches" {
		s = strings.ToLower(s)
		val = strings.ToLower(val)
	}
	switch c.Op {
	case "contains":
		return strings.Contains(s, val), nil
	case "starts":
		return strings.HasPrefix(s, val), nil
	case "ends":
		return strings.HasSuffix(s, val), nil
	case "matches":
		pattern := val
		if c.IgnoreCase {
			pattern = "(?i)" + pattern
		}
		return regexp.MatchString(pattern, s)
	}
	return false, fmt.Errorf("unknown string operator: %s", c.Op)
}

func (c *rawStringCondition) paths() []Path { return []Path{c.Path} }

func (c *rawStringCondition) withRelativeParts(prefix []pathPart) internalConditionImpl {
	return &rawStringCondition{
		Path:       c.Path.stripParts(prefix),
		Val:        c.Val,
		Op:         c.Op,
		IgnoreCase: c.IgnoreCase,
	}
}

type rawInCondition struct {
	Path       Path
	Values     []any
	IgnoreCase bool
}

func (c *rawInCondition) evaluateAny(v any) (bool, error) {
	rv := toReflectValue(v)
	target, err := c.Path.resolve(rv)
	if err != nil {
		return false, nil
	}
	for _, val := range c.Values {
		match, err := compareValues(target, reflect.ValueOf(val), "==", c.IgnoreCase)
		if err != nil {
			return false, err
		}
		if match {
			return true, nil
		}
	}
	return false, nil
}

func (c *rawInCondition) paths() []Path { return []Path{c.Path} }

func (c *rawInCondition) withRelativeParts(prefix []pathPart) internalConditionImpl {
	return &rawInCondition{
		Path:       c.Path.stripParts(prefix),
		Values:     c.Values,
		IgnoreCase: c.IgnoreCase,
	}
}

type rawLogCondition struct {
	Message string
}

func (c *rawLogCondition) evaluateAny(v any) (bool, error) {
	fmt.Printf("DEEP LOG CONDITION: %s (value: %v)\n", c.Message, v)
	return true, nil
}

func (c *rawLogCondition) paths() []Path { return nil }

func (c *rawLogCondition) withRelativeParts(prefix []pathPart) internalConditionImpl {
	return c
}

type rawCompareCondition struct {
	Path       Path
	Val        any
	Op         string
	IgnoreCase bool
}

func (c *rawCompareCondition) evaluateAny(v any) (bool, error) {
	rv := toReflectValue(v)
	target, err := c.Path.resolve(rv)
	if err != nil {
		return false, err
	}
	return compareValues(target, reflect.ValueOf(c.Val), c.Op, c.IgnoreCase)
}

func (c *rawCompareCondition) paths() []Path {
	return []Path{c.Path}
}

func (c *rawCompareCondition) withRelativeParts(prefix []pathPart) internalConditionImpl {
	return &rawCompareCondition{
		Path:       c.Path.stripParts(prefix),
		Val:        c.Val,
		Op:         c.Op,
		IgnoreCase: c.IgnoreCase,
	}
}

type rawCompareFieldCondition struct {
	Path1      Path
	Path2      Path
	Op         string
	IgnoreCase bool
}

func (c *rawCompareFieldCondition) evaluateAny(v any) (bool, error) {
	rv := toReflectValue(v)
	target1, err := c.Path1.resolve(rv)
	if err != nil {
		return false, err
	}
	target2, err := c.Path2.resolve(rv)
	if err != nil {
		return false, err
	}
	return compareValues(target1, target2, c.Op, c.IgnoreCase)
}

func (c *rawCompareFieldCondition) paths() []Path {
	return []Path{c.Path1, c.Path2}
}

func (c *rawCompareFieldCondition) withRelativeParts(prefix []pathPart) internalConditionImpl {
	return &rawCompareFieldCondition{
		Path1:      c.Path1.stripParts(prefix),
		Path2:      c.Path2.stripParts(prefix),
		Op:         c.Op,
		IgnoreCase: c.IgnoreCase,
	}
}

type rawAndCondition struct {
	Conditions []internalConditionImpl
}

func (c *rawAndCondition) evaluateAny(v any) (bool, error) {
	for _, sub := range c.Conditions {
		ok, err := sub.evaluateAny(v)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

func (c *rawAndCondition) paths() []Path {
	var res []Path
	for _, sub := range c.Conditions {
		res = append(res, sub.paths()...)
	}
	return res
}

func (c *rawAndCondition) withRelativeParts(prefix []pathPart) internalConditionImpl {
	res := &rawAndCondition{Conditions: make([]internalConditionImpl, len(c.Conditions))}
	for i, sub := range c.Conditions {
		res.Conditions[i] = sub.withRelativeParts(prefix)
	}
	return res
}

type rawOrCondition struct {
	Conditions []internalConditionImpl
}

func (c *rawOrCondition) evaluateAny(v any) (bool, error) {
	for _, sub := range c.Conditions {
		ok, err := sub.evaluateAny(v)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

func (c *rawOrCondition) paths() []Path {
	var res []Path
	for _, sub := range c.Conditions {
		res = append(res, sub.paths()...)
	}
	return res
}

func (c *rawOrCondition) withRelativeParts(prefix []pathPart) internalConditionImpl {
	res := &rawOrCondition{Conditions: make([]internalConditionImpl, len(c.Conditions))}
	for i, sub := range c.Conditions {
		res.Conditions[i] = sub.withRelativeParts(prefix)
	}
	return res
}

type rawNotCondition struct {
	C internalConditionImpl
}

func (c *rawNotCondition) evaluateAny(v any) (bool, error) {
	ok, err := c.C.evaluateAny(v)
	if err != nil {
		return false, err
	}
	return !ok, nil
}

func (c *rawNotCondition) paths() []Path {
	return c.C.paths()
}

func (c *rawNotCondition) withRelativeParts(prefix []pathPart) internalConditionImpl {
	return &rawNotCondition{C: c.C.withRelativeParts(prefix)}
}

// CompareCondition represents a comparison between a path and a literal value.
type CompareCondition[T any] struct {
	Path       Path
	Val        any
	Op         string
	IgnoreCase bool
}

func (c CompareCondition[T]) Evaluate(v *T) (bool, error) {
	return c.evaluateAny(v)
}

func (c CompareCondition[T]) evaluateAny(v any) (bool, error) {
	raw := &rawCompareCondition{Path: c.Path, Val: c.Val, Op: c.Op, IgnoreCase: c.IgnoreCase}
	return raw.evaluateAny(v)
}

func (c CompareCondition[T]) MarshalJSON() ([]byte, error) {
	s, err := marshalConditionAny(c)
	if err != nil {
		return nil, err
	}
	return json.Marshal(s)
}

// Equal returns a condition that checks if the value at the path is equal to the given value.
func Equal[T any](path string, val any) Condition[T] {
	return CompareCondition[T]{Path: Path(path), Val: val, Op: "=="}
}

// EqualFold returns a condition that checks if the value at the path is equal to the given value (case-insensitive).
func EqualFold[T any](path string, val any) Condition[T] {
	return CompareCondition[T]{Path: Path(path), Val: val, Op: "==", IgnoreCase: true}
}

// NotEqual returns a condition that checks if the value at the path is not equal to the given value.
func NotEqual[T any](path string, val any) Condition[T] {
	return CompareCondition[T]{Path: Path(path), Val: val, Op: "!="}
}

// NotEqualFold returns a condition that checks if the value at the path is not equal to the given value (case-insensitive).
func NotEqualFold[T any](path string, val any) Condition[T] {
	return CompareCondition[T]{Path: Path(path), Val: val, Op: "!=", IgnoreCase: true}
}

// Greater returns a condition that checks if the value at the path is greater than the given value.
func Greater[T any](path string, val any) Condition[T] {
	return CompareCondition[T]{Path: Path(path), Val: val, Op: ">"}
}

// Less returns a condition that checks if the value at the path is less than the given value.
func Less[T any](path string, val any) Condition[T] {
	return CompareCondition[T]{Path: Path(path), Val: val, Op: "<"}
}

// GreaterEqual returns a condition that checks if the value at the path is greater than or equal to the given value.
func GreaterEqual[T any](path string, val any) Condition[T] {
	return CompareCondition[T]{Path: Path(path), Val: val, Op: ">="}
}

// LessEqual returns a condition that checks if the value at the path is less than or equal to the given value.
func LessEqual[T any](path string, val any) Condition[T] {
	return CompareCondition[T]{Path: Path(path), Val: val, Op: "<="}
}

// CompareFieldCondition represents a comparison between two paths.
type CompareFieldCondition[T any] struct {
	Path1      Path
	Path2      Path
	Op         string
	IgnoreCase bool
}

func (c CompareFieldCondition[T]) Evaluate(v *T) (bool, error) {
	return c.evaluateAny(v)
}

func (c CompareFieldCondition[T]) evaluateAny(v any) (bool, error) {
	raw := &rawCompareFieldCondition{Path1: c.Path1, Path2: c.Path2, Op: c.Op, IgnoreCase: c.IgnoreCase}
	return raw.evaluateAny(v)
}

func (c CompareFieldCondition[T]) MarshalJSON() ([]byte, error) {
	s, err := marshalConditionAny(c)
	if err != nil {
		return nil, err
	}
	return json.Marshal(s)
}

// EqualField returns a condition that checks if the value at path1 is equal to the value at path2.
func EqualField[T any](path1, path2 string) Condition[T] {
	return CompareFieldCondition[T]{Path1: Path(path1), Path2: Path(path2), Op: "=="}
}

// EqualFieldFold returns a condition that checks if the value at path1 is equal to the value at path2 (case-insensitive).
func EqualFieldFold[T any](path1, path2 string) Condition[T] {
	return CompareFieldCondition[T]{Path1: Path(path1), Path2: Path(path2), Op: "==", IgnoreCase: true}
}

// NotEqualField returns a condition that checks if the value at path1 is not equal to the value at path2.
func NotEqualField[T any](path1, path2 string) Condition[T] {
	return CompareFieldCondition[T]{Path1: Path(path1), Path2: Path(path2), Op: "!="}
}

// NotEqualFieldFold returns a condition that checks if the value at path1 is not equal to the value at path2 (case-insensitive).
func NotEqualFieldFold[T any](path1, path2 string) Condition[T] {
	return CompareFieldCondition[T]{Path1: Path(path1), Path2: Path(path2), Op: "!=", IgnoreCase: true}
}

// GreaterField returns a condition that checks if the value at path1 is greater than the value at path2.
func GreaterField[T any](path1, path2 string) Condition[T] {
	return CompareFieldCondition[T]{Path1: Path(path1), Path2: Path(path2), Op: ">"}
}

// LessField returns a condition that checks if the value at path1 is less than the value at path2.
func LessField[T any](path1, path2 string) Condition[T] {
	return CompareFieldCondition[T]{Path1: Path(path1), Path2: Path(path2), Op: "<"}
}

// GreaterEqualField returns a condition that checks if the value at path1 is greater than or equal to the value at path2.
func GreaterEqualField[T any](path1, path2 string) Condition[T] {
	return CompareFieldCondition[T]{Path1: Path(path1), Path2: Path(path2), Op: ">="}
}

// LessEqualField returns a condition that checks if the value at path1 is less than or equal to the value at path2.
func LessEqualField[T any](path1, path2 string) Condition[T] {
	return CompareFieldCondition[T]{Path1: Path(path1), Path2: Path(path2), Op: "<="}
}

// AndCondition represents a logical AND of multiple conditions.
type AndCondition[T any] struct {
	Conditions []Condition[T]
}

func (c AndCondition[T]) Evaluate(v *T) (bool, error) {
	return c.evaluateAny(v)
}

func (c AndCondition[T]) evaluateAny(v any) (bool, error) {
	for _, sub := range c.Conditions {
		ok, err := sub.evaluateAny(v)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

func (c AndCondition[T]) MarshalJSON() ([]byte, error) {
	s, err := marshalConditionAny(c)
	if err != nil {
		return nil, err
	}
	return json.Marshal(s)
}

// And returns a condition that represents a logical AND of multiple conditions.
func And[T any](conds ...Condition[T]) Condition[T] {
	return AndCondition[T]{Conditions: conds}
}

// OrCondition represents a logical OR of multiple conditions.
type OrCondition[T any] struct {
	Conditions []Condition[T]
}

func (c OrCondition[T]) Evaluate(v *T) (bool, error) {
	return c.evaluateAny(v)
}

func (c OrCondition[T]) evaluateAny(v any) (bool, error) {
	for _, sub := range c.Conditions {
		ok, err := sub.evaluateAny(v)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

func (c OrCondition[T]) MarshalJSON() ([]byte, error) {
	s, err := marshalConditionAny(c)
	if err != nil {
		return nil, err
	}
	return json.Marshal(s)
}

// Or returns a condition that represents a logical OR of multiple conditions.
func Or[T any](conds ...Condition[T]) Condition[T] {
	return OrCondition[T]{Conditions: conds}
}

// NotCondition represents a logical NOT of a condition.
type NotCondition[T any] struct {
	C Condition[T]
}

func (c NotCondition[T]) Evaluate(v *T) (bool, error) {
	return c.evaluateAny(v)
}

func (c NotCondition[T]) evaluateAny(v any) (bool, error) {
	ok, err := c.C.evaluateAny(v)
	if err != nil {
		return false, err
	}
	return !ok, nil
}

func (c NotCondition[T]) MarshalJSON() ([]byte, error) {
	s, err := marshalConditionAny(c)
	if err != nil {
		return nil, err
	}
	return json.Marshal(s)
}

// Not returns a condition that represents a logical NOT of a condition.
func Not[T any](c Condition[T]) Condition[T] {
	return NotCondition[T]{C: c}
}

// DefinedCondition checks if a path is defined (non-zero).
type DefinedCondition[T any] struct {
	Path Path
}

func (c DefinedCondition[T]) Evaluate(v *T) (bool, error) {
	return c.evaluateAny(v)
}

func (c DefinedCondition[T]) evaluateAny(v any) (bool, error) {
	raw := &rawDefinedCondition{Path: c.Path}
	return raw.evaluateAny(v)
}

func (c DefinedCondition[T]) MarshalJSON() ([]byte, error) {
	s, err := marshalConditionAny(c)
	if err != nil {
		return nil, err
	}
	return json.Marshal(s)
}

// Defined returns a condition that checks if the value at the path is defined.
func Defined[T any](path string) Condition[T] {
	return DefinedCondition[T]{Path: Path(path)}
}

// UndefinedCondition checks if a path is undefined (zero value).
type UndefinedCondition[T any] struct {
	Path Path
}

func (c UndefinedCondition[T]) Evaluate(v *T) (bool, error) {
	return c.evaluateAny(v)
}

func (c UndefinedCondition[T]) evaluateAny(v any) (bool, error) {
	raw := &rawUndefinedCondition{Path: c.Path}
	return raw.evaluateAny(v)
}

func (c UndefinedCondition[T]) MarshalJSON() ([]byte, error) {
	s, err := marshalConditionAny(c)
	if err != nil {
		return nil, err
	}
	return json.Marshal(s)
}

// Undefined returns a condition that checks if the value at the path is undefined.
func Undefined[T any](path string) Condition[T] {
	return UndefinedCondition[T]{Path: Path(path)}
}

// TypeCondition checks if the value at a path has a specific type.
type TypeCondition[T any] struct {
	Path     Path
	TypeName string
}

func (c TypeCondition[T]) Evaluate(v *T) (bool, error) {
	return c.evaluateAny(v)
}

func (c TypeCondition[T]) evaluateAny(v any) (bool, error) {
	raw := &rawTypeCondition{Path: c.Path, TypeName: c.TypeName}
	return raw.evaluateAny(v)
}

func (c TypeCondition[T]) MarshalJSON() ([]byte, error) {
	s, err := marshalConditionAny(c)
	if err != nil {
		return nil, err
	}
	return json.Marshal(s)
}

// Type returns a condition that checks if the value at the path has the given type.
func Type[T any](path, typeName string) Condition[T] {
	return TypeCondition[T]{Path: Path(path), TypeName: typeName}
}

// StringCondition checks a string value at a path against a pattern.
type StringCondition[T any] struct {
	Path       Path
	Val        string
	Op         string
	IgnoreCase bool
}

func (c StringCondition[T]) Evaluate(v *T) (bool, error) {
	return c.evaluateAny(v)
}

func (c StringCondition[T]) evaluateAny(v any) (bool, error) {
	raw := &rawStringCondition{Path: c.Path, Val: c.Val, Op: c.Op, IgnoreCase: c.IgnoreCase}
	return raw.evaluateAny(v)
}

func (c StringCondition[T]) MarshalJSON() ([]byte, error) {
	s, err := marshalConditionAny(c)
	if err != nil {
		return nil, err
	}
	return json.Marshal(s)
}

// Contains returns a condition that checks if the string value at the path contains the given substring.
func Contains[T any](path, val string) Condition[T] {
	return StringCondition[T]{Path: Path(path), Val: val, Op: "contains"}
}

// ContainsFold returns a condition that checks if the string value at the path contains the given substring (case-insensitive).
func ContainsFold[T any](path, val string) Condition[T] {
	return StringCondition[T]{Path: Path(path), Val: val, Op: "contains", IgnoreCase: true}
}

// StartsWith returns a condition that checks if the string value at the path starts with the given prefix.
func StartsWith[T any](path, val string) Condition[T] {
	return StringCondition[T]{Path: Path(path), Val: val, Op: "starts"}
}

// StartsWithFold returns a condition that checks if the string value at the path starts with the given prefix (case-insensitive).
func StartsWithFold[T any](path, val string) Condition[T] {
	return StringCondition[T]{Path: Path(path), Val: val, Op: "starts", IgnoreCase: true}
}

// EndsWith returns a condition that checks if the string value at the path ends with the given suffix.
func EndsWith[T any](path, val string) Condition[T] {
	return StringCondition[T]{Path: Path(path), Val: val, Op: "ends"}
}

// EndsWithFold returns a condition that checks if the string value at the path ends with the given suffix (case-insensitive).
func EndsWithFold[T any](path, val string) Condition[T] {
	return StringCondition[T]{Path: Path(path), Val: val, Op: "ends", IgnoreCase: true}
}

// Matches returns a condition that checks if the string value at the path matches the given regex pattern.
func Matches[T any](path, pattern string) Condition[T] {
	return StringCondition[T]{Path: Path(path), Val: pattern, Op: "matches"}
}

// MatchesFold returns a condition that checks if the string value at the path matches the given regex pattern (case-insensitive).
func MatchesFold[T any](path, pattern string) Condition[T] {
	return StringCondition[T]{Path: Path(path), Val: pattern, Op: "matches", IgnoreCase: true}
}

// InCondition checks if the value at a path is one of the given values.
type InCondition[T any] struct {
	Path       Path
	Values     []any
	IgnoreCase bool
}

func (c InCondition[T]) Evaluate(v *T) (bool, error) {
	return c.evaluateAny(v)
}

func (c InCondition[T]) evaluateAny(v any) (bool, error) {
	raw := &rawInCondition{Path: c.Path, Values: c.Values, IgnoreCase: c.IgnoreCase}
	return raw.evaluateAny(v)
}

func (c InCondition[T]) MarshalJSON() ([]byte, error) {
	s, err := marshalConditionAny(c)
	if err != nil {
		return nil, err
	}
	return json.Marshal(s)
}

// In returns a condition that checks if the value at the path is one of the given values.
func In[T any](path string, values ...any) Condition[T] {
	return InCondition[T]{Path: Path(path), Values: values}
}

// InFold returns a condition that checks if the value at the path is one of the given values (case-insensitive).
func InFold[T any](path string, values ...any) Condition[T] {
	return InCondition[T]{Path: Path(path), Values: values, IgnoreCase: true}
}

// LogCondition logs a message during evaluation.
type LogCondition[T any] struct {
	Message string
}

func (c LogCondition[T]) Evaluate(v *T) (bool, error) {
	return c.evaluateAny(v)
}

func (c LogCondition[T]) evaluateAny(v any) (bool, error) {
	raw := &rawLogCondition{Message: c.Message}
	return raw.evaluateAny(v)
}

func (c LogCondition[T]) MarshalJSON() ([]byte, error) {
	s, err := marshalConditionAny(c)
	if err != nil {
		return nil, err
	}
	return json.Marshal(s)
}

// Log returns a condition that logs the given message during evaluation.
func Log[T any](message string) Condition[T] {
	return LogCondition[T]{Message: message}
}

// typedRawCondition wraps a internalConditionImpl to satisfy Condition[T].
type typedRawCondition[T any] struct {
	raw internalConditionImpl
}

func (c *typedRawCondition[T]) Evaluate(v *T) (bool, error) {
	return c.raw.evaluateAny(v)
}

func (c *typedRawCondition[T]) evaluateAny(v any) (bool, error) {
	return c.raw.evaluateAny(v)
}

func (c *typedRawCondition[T]) MarshalJSON() ([]byte, error) {
	s, err := marshalConditionAny(c.raw)
	if err != nil {
		return nil, err
	}
	return json.Marshal(s)
}
