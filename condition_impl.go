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
	paths() []deepPath
	withRelativeParts(prefix []pathPart) internalConditionImpl
}

type rawDefinedCondition struct {
	Path deepPath
}

func (c *rawDefinedCondition) evaluateAny(v any) (bool, error) {
	rv := toReflectValue(v)
	target, err := c.Path.resolve(rv)
	if err != nil {
		return false, nil
	}
	return target.IsValid(), nil
}

func (c *rawDefinedCondition) paths() []deepPath { return []deepPath{c.Path} }

func (c *rawDefinedCondition) withRelativeParts(prefix []pathPart) internalConditionImpl {
	return &rawDefinedCondition{Path: c.Path.stripParts(prefix)}
}

type rawUndefinedCondition struct {
	Path deepPath
}

func (c *rawUndefinedCondition) evaluateAny(v any) (bool, error) {
	rv := toReflectValue(v)
	target, err := c.Path.resolve(rv)
	if err != nil {
		return true, nil
	}
	return !target.IsValid(), nil
}

func (c *rawUndefinedCondition) paths() []deepPath { return []deepPath{c.Path} }

func (c *rawUndefinedCondition) withRelativeParts(prefix []pathPart) internalConditionImpl {
	return &rawUndefinedCondition{Path: c.Path.stripParts(prefix)}
}

type rawTypeCondition struct {
	Path     deepPath
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
		return (k == reflect.Pointer || k == reflect.Interface || k == reflect.Slice || k == reflect.Map) && target.IsNil(), nil
	case "undefined":
		return false, nil
	default:
		return false, fmt.Errorf("unknown type: %s", c.TypeName)
	}
}

func (c *rawTypeCondition) paths() []deepPath { return []deepPath{c.Path} }

func (c *rawTypeCondition) withRelativeParts(prefix []pathPart) internalConditionImpl {
	return &rawTypeCondition{Path: c.Path.stripParts(prefix), TypeName: c.TypeName}
}

type rawStringCondition struct {
	Path       deepPath
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

func (c *rawStringCondition) paths() []deepPath { return []deepPath{c.Path} }

func (c *rawStringCondition) withRelativeParts(prefix []pathPart) internalConditionImpl {
	return &rawStringCondition{
		Path:       c.Path.stripParts(prefix),
		Val:        c.Val,
		Op:         c.Op,
		IgnoreCase: c.IgnoreCase,
	}
}

type rawInCondition struct {
	Path       deepPath
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

func (c *rawInCondition) paths() []deepPath { return []deepPath{c.Path} }

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

func (c *rawLogCondition) paths() []deepPath { return nil }

func (c *rawLogCondition) withRelativeParts(prefix []pathPart) internalConditionImpl {
	return c
}

type rawCompareCondition struct {
	Path       deepPath
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

func (c *rawCompareCondition) paths() []deepPath {
	return []deepPath{c.Path}
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
	Path1      deepPath
	Path2      deepPath
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

func (c *rawCompareFieldCondition) paths() []deepPath {
	return []deepPath{c.Path1, c.Path2}
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

func (c *rawAndCondition) paths() []deepPath {
	var res []deepPath
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

func (c *rawOrCondition) paths() []deepPath {
	var res []deepPath
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

func (c *rawNotCondition) paths() []deepPath {
	return c.C.paths()
}

func (c *rawNotCondition) withRelativeParts(prefix []pathPart) internalConditionImpl {
	return &rawNotCondition{C: c.C.withRelativeParts(prefix)}
}

// compareCondition represents a comparison between a path and a literal value.
type compareCondition[T any] struct {
	Path       deepPath
	Val        any
	Op         string
	IgnoreCase bool
}

func (c compareCondition[T]) Evaluate(v *T) (bool, error) {
	return c.evaluateAny(v)
}

func (c compareCondition[T]) evaluateAny(v any) (bool, error) {
	raw := &rawCompareCondition{Path: c.Path, Val: c.Val, Op: c.Op, IgnoreCase: c.IgnoreCase}
	return raw.evaluateAny(v)
}

func (c compareCondition[T]) MarshalJSON() ([]byte, error) {
	s, err := marshalConditionAny(c)
	if err != nil {
		return nil, err
	}
	return json.Marshal(s)
}

// Eq returns a condition that checks if the value at the path is equal to the given value.
func Eq[T any](p string, val any) Condition[T] {
	return compareCondition[T]{Path: deepPath(p), Val: val, Op: "=="}
}

// EqFold returns a condition that checks if the value at the path is equal to the given value (case-insensitive).
func EqFold[T any](p string, val any) Condition[T] {
	return compareCondition[T]{Path: deepPath(p), Val: val, Op: "==", IgnoreCase: true}
}

// Ne returns a condition that checks if the value at the path is not equal to the given value.
func Ne[T any](p string, val any) Condition[T] {
	return compareCondition[T]{Path: deepPath(p), Val: val, Op: "!="}
}

// NeFold returns a condition that checks if the value at the path is not equal to the given value (case-insensitive).
func NeFold[T any](p string, val any) Condition[T] {
	return compareCondition[T]{Path: deepPath(p), Val: val, Op: "!=", IgnoreCase: true}
}

// Greater returns a condition that checks if the value at the path is greater than the given value.
func Greater[T any](p string, val any) Condition[T] {
	return compareCondition[T]{Path: deepPath(p), Val: val, Op: ">"}
}

// Less returns a condition that checks if the value at the path is less than the given value.
func Less[T any](p string, val any) Condition[T] {
	return compareCondition[T]{Path: deepPath(p), Val: val, Op: "<"}
}

// GreaterEqual returns a condition that checks if the value at the path is greater than or equal to the given value.
func GreaterEqual[T any](p string, val any) Condition[T] {
	return compareCondition[T]{Path: deepPath(p), Val: val, Op: ">="}
}

// LessEqual returns a condition that checks if the value at the path is less than or equal to the given value.
func LessEqual[T any](p string, val any) Condition[T] {
	return compareCondition[T]{Path: deepPath(p), Val: val, Op: "<="}
}

// compareFieldCondition represents a comparison between two paths.
type compareFieldCondition[T any] struct {
	Path1      deepPath
	Path2      deepPath
	Op         string
	IgnoreCase bool
}

func (c compareFieldCondition[T]) Evaluate(v *T) (bool, error) {
	return c.evaluateAny(v)
}

func (c compareFieldCondition[T]) evaluateAny(v any) (bool, error) {
	raw := &rawCompareFieldCondition{Path1: c.Path1, Path2: c.Path2, Op: c.Op, IgnoreCase: c.IgnoreCase}
	return raw.evaluateAny(v)
}

func (c compareFieldCondition[T]) MarshalJSON() ([]byte, error) {
	s, err := marshalConditionAny(c)
	if err != nil {
		return nil, err
	}
	return json.Marshal(s)
}

// EqualField returns a condition that checks if the value at path1 is equal to the value at path2.
func EqualField[T any](path1, path2 string) Condition[T] {
	return compareFieldCondition[T]{Path1: deepPath(path1), Path2: deepPath(path2), Op: "=="}
}

// EqualFieldFold returns a condition that checks if the value at path1 is equal to the value at path2 (case-insensitive).
func EqualFieldFold[T any](path1, path2 string) Condition[T] {
	return compareFieldCondition[T]{Path1: deepPath(path1), Path2: deepPath(path2), Op: "==", IgnoreCase: true}
}

// NotEqualField returns a condition that checks if the value at path1 is not equal to the value at path2.
func NotEqualField[T any](path1, path2 string) Condition[T] {
	return compareFieldCondition[T]{Path1: deepPath(path1), Path2: deepPath(path2), Op: "!="}
}

// NotEqualFieldFold returns a condition that checks if the value at path1 is not equal to the value at path2 (case-insensitive).
func NotEqualFieldFold[T any](path1, path2 string) Condition[T] {
	return compareFieldCondition[T]{Path1: deepPath(path1), Path2: deepPath(path2), Op: "!=", IgnoreCase: true}
}

// GreaterField returns a condition that checks if the value at path1 is greater than the value at path2.
func GreaterField[T any](path1, path2 string) Condition[T] {
	return compareFieldCondition[T]{Path1: deepPath(path1), Path2: deepPath(path2), Op: ">"}
}

// LessField returns a condition that checks if the value at path1 is less than the value at path2.
func LessField[T any](path1, path2 string) Condition[T] {
	return compareFieldCondition[T]{Path1: deepPath(path1), Path2: deepPath(path2), Op: "<"}
}

// GreaterEqualField returns a condition that checks if the value at path1 is greater than or equal to the value at path2.
func GreaterEqualField[T any](path1, path2 string) Condition[T] {
	return compareFieldCondition[T]{Path1: deepPath(path1), Path2: deepPath(path2), Op: ">="}
}

// LessEqualField returns a condition that checks if the value at path1 is less than or equal to the value at path2.
func LessEqualField[T any](path1, path2 string) Condition[T] {
	return compareFieldCondition[T]{Path1: deepPath(path1), Path2: deepPath(path2), Op: "<="}
}

// andCondition represents a logical AND of multiple conditions.
type andCondition[T any] struct {
	Conditions []Condition[T]
}

func (c andCondition[T]) Evaluate(v *T) (bool, error) {
	return c.evaluateAny(v)
}

func (c andCondition[T]) evaluateAny(v any) (bool, error) {
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

func (c andCondition[T]) MarshalJSON() ([]byte, error) {
	s, err := marshalConditionAny(c)
	if err != nil {
		return nil, err
	}
	return json.Marshal(s)
}

// And returns a condition that represents a logical AND of multiple conditions.
func And[T any](conds ...Condition[T]) Condition[T] {
	return andCondition[T]{Conditions: conds}
}

// orCondition represents a logical OR of multiple conditions.
type orCondition[T any] struct {
	Conditions []Condition[T]
}

func (c orCondition[T]) Evaluate(v *T) (bool, error) {
	return c.evaluateAny(v)
}

func (c orCondition[T]) evaluateAny(v any) (bool, error) {
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

func (c orCondition[T]) MarshalJSON() ([]byte, error) {
	s, err := marshalConditionAny(c)
	if err != nil {
		return nil, err
	}
	return json.Marshal(s)
}

// Or returns a condition that represents a logical OR of multiple conditions.
func Or[T any](conds ...Condition[T]) Condition[T] {
	return orCondition[T]{Conditions: conds}
}

// notCondition represents a logical NOT of a condition.
type notCondition[T any] struct {
	C Condition[T]
}

func (c notCondition[T]) Evaluate(v *T) (bool, error) {
	return c.evaluateAny(v)
}

func (c notCondition[T]) evaluateAny(v any) (bool, error) {
	ok, err := c.C.evaluateAny(v)
	if err != nil {
		return false, err
	}
	return !ok, nil
}

func (c notCondition[T]) MarshalJSON() ([]byte, error) {
	s, err := marshalConditionAny(c)
	if err != nil {
		return nil, err
	}
	return json.Marshal(s)
}

// Not returns a condition that represents a logical NOT of a condition.
func Not[T any](c Condition[T]) Condition[T] {
	return notCondition[T]{C: c}
}

// definedCondition checks if a path is defined (non-zero).
type definedCondition[T any] struct {
	Path deepPath
}

func (c definedCondition[T]) Evaluate(v *T) (bool, error) {
	return c.evaluateAny(v)
}

func (c definedCondition[T]) evaluateAny(v any) (bool, error) {
	raw := &rawDefinedCondition{Path: c.Path}
	return raw.evaluateAny(v)
}

func (c definedCondition[T]) MarshalJSON() ([]byte, error) {
	s, err := marshalConditionAny(c)
	if err != nil {
		return nil, err
	}
	return json.Marshal(s)
}

// Defined returns a condition that checks if the value at the path is defined.
func Defined[T any](p string) Condition[T] {
	return definedCondition[T]{Path: deepPath(p)}
}

// undefinedCondition checks if a path is undefined (zero value).
type undefinedCondition[T any] struct {
	Path deepPath
}

func (c undefinedCondition[T]) Evaluate(v *T) (bool, error) {
	return c.evaluateAny(v)
}

func (c undefinedCondition[T]) evaluateAny(v any) (bool, error) {
	raw := &rawUndefinedCondition{Path: c.Path}
	return raw.evaluateAny(v)
}

func (c undefinedCondition[T]) MarshalJSON() ([]byte, error) {
	s, err := marshalConditionAny(c)
	if err != nil {
		return nil, err
	}
	return json.Marshal(s)
}

// Undefined returns a condition that checks if the value at the path is undefined.
func Undefined[T any](p string) Condition[T] {
	return undefinedCondition[T]{Path: deepPath(p)}
}

// typeCondition checks if the value at a path has a specific type.
type typeCondition[T any] struct {
	Path     deepPath
	TypeName string
}

func (c typeCondition[T]) Evaluate(v *T) (bool, error) {
	return c.evaluateAny(v)
}

func (c typeCondition[T]) evaluateAny(v any) (bool, error) {
	raw := &rawTypeCondition{Path: c.Path, TypeName: c.TypeName}
	return raw.evaluateAny(v)
}

func (c typeCondition[T]) MarshalJSON() ([]byte, error) {
	s, err := marshalConditionAny(c)
	if err != nil {
		return nil, err
	}
	return json.Marshal(s)
}

// Type returns a condition that checks if the value at the path has the given type.
func Type[T any](p, typeName string) Condition[T] {
	return typeCondition[T]{Path: deepPath(p), TypeName: typeName}
}

// stringCondition checks a string value at a path against a pattern.
type stringCondition[T any] struct {
	Path       deepPath
	Val        string
	Op         string
	IgnoreCase bool
}

func (c stringCondition[T]) Evaluate(v *T) (bool, error) {
	return c.evaluateAny(v)
}

func (c stringCondition[T]) evaluateAny(v any) (bool, error) {
	raw := &rawStringCondition{Path: c.Path, Val: c.Val, Op: c.Op, IgnoreCase: c.IgnoreCase}
	return raw.evaluateAny(v)
}

func (c stringCondition[T]) MarshalJSON() ([]byte, error) {
	s, err := marshalConditionAny(c)
	if err != nil {
		return nil, err
	}
	return json.Marshal(s)
}

// Contains returns a condition that checks if the string value at the path contains the given substring.
func Contains[T any](p, val string) Condition[T] {
	return stringCondition[T]{Path: deepPath(p), Val: val, Op: "contains"}
}

// ContainsFold returns a condition that checks if the string value at the path contains the given substring (case-insensitive).
func ContainsFold[T any](p, val string) Condition[T] {
	return stringCondition[T]{Path: deepPath(p), Val: val, Op: "contains", IgnoreCase: true}
}

// StartsWith returns a condition that checks if the string value at the path starts with the given prefix.
func StartsWith[T any](p, val string) Condition[T] {
	return stringCondition[T]{Path: deepPath(p), Val: val, Op: "starts"}
}

// StartsWithFold returns a condition that checks if the string value at the path starts with the given prefix (case-insensitive).
func StartsWithFold[T any](p, val string) Condition[T] {
	return stringCondition[T]{Path: deepPath(p), Val: val, Op: "starts", IgnoreCase: true}
}

// EndsWith returns a condition that checks if the string value at the path ends with the given suffix.
func EndsWith[T any](p, val string) Condition[T] {
	return stringCondition[T]{Path: deepPath(p), Val: val, Op: "ends"}
}

// EndsWithFold returns a condition that checks if the string value at the path ends with the given suffix (case-insensitive).
func EndsWithFold[T any](p, val string) Condition[T] {
	return stringCondition[T]{Path: deepPath(p), Val: val, Op: "ends", IgnoreCase: true}
}

// Matches returns a condition that checks if the string value at the path matches the given regex pattern.
func Matches[T any](p, pattern string) Condition[T] {
	return stringCondition[T]{Path: deepPath(p), Val: pattern, Op: "matches"}
}

// MatchesFold returns a condition that checks if the string value at the path matches the given regex pattern (case-insensitive).
func MatchesFold[T any](p, pattern string) Condition[T] {
	return stringCondition[T]{Path: deepPath(p), Val: pattern, Op: "matches", IgnoreCase: true}
}

// inCondition checks if the value at a path is one of the given values.
type inCondition[T any] struct {
	Path       deepPath
	Values     []any
	IgnoreCase bool
}

func (c inCondition[T]) Evaluate(v *T) (bool, error) {
	return c.evaluateAny(v)
}

func (c inCondition[T]) evaluateAny(v any) (bool, error) {
	raw := &rawInCondition{Path: c.Path, Values: c.Values, IgnoreCase: c.IgnoreCase}
	return raw.evaluateAny(v)
}

func (c inCondition[T]) MarshalJSON() ([]byte, error) {
	s, err := marshalConditionAny(c)
	if err != nil {
		return nil, err
	}
	return json.Marshal(s)
}

// In returns a condition that checks if the value at the path is one of the given values.
func In[T any](p string, values ...any) Condition[T] {
	return inCondition[T]{Path: deepPath(p), Values: values}
}

// InFold returns a condition that checks if the value at the path is one of the given values (case-insensitive).
func InFold[T any](p string, values ...any) Condition[T] {
	return inCondition[T]{Path: deepPath(p), Values: values, IgnoreCase: true}
}

// logCondition logs a message during evaluation.
type logCondition[T any] struct {
	Message string
}

func (c logCondition[T]) Evaluate(v *T) (bool, error) {
	return c.evaluateAny(v)
}

func (c logCondition[T]) evaluateAny(v any) (bool, error) {
	raw := &rawLogCondition{Message: c.Message}
	return raw.evaluateAny(v)
}

func (c logCondition[T]) MarshalJSON() ([]byte, error) {
	s, err := marshalConditionAny(c)
	if err != nil {
		return nil, err
	}
	return json.Marshal(s)
}

// Log returns a condition that logs the given message during evaluation.
func Log[T any](message string) Condition[T] {
	return logCondition[T]{Message: message}
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
