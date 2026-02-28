package cond

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"

	"github.com/brunoga/deep/v5/internal/core"
)

type rawDefinedCondition struct {
	Path core.DeepPath
}

func (c *rawDefinedCondition) EvaluateAny(v any) (bool, error) {
	rv := core.ToReflectValue(v)
	target, err := c.Path.Resolve(rv)
	if err != nil {
		return false, nil
	}
	return target.IsValid(), nil
}

func (c *rawDefinedCondition) Paths() []string { return []string{string(c.Path)} }

func (c *rawDefinedCondition) WithRelativePath(prefix string) InternalCondition {
	// Re-parse internally
	// pathParts := core.ParsePath(string(c.Path)) // Unused
	prefixParts := core.ParsePath(prefix)

	newPath := c.Path.StripParts(prefixParts)
	return &rawDefinedCondition{Path: newPath}
}

type rawUndefinedCondition struct {
	Path core.DeepPath
}

func (c *rawUndefinedCondition) EvaluateAny(v any) (bool, error) {
	rv := core.ToReflectValue(v)
	target, err := c.Path.Resolve(rv)
	if err != nil {
		return true, nil
	}
	return !target.IsValid(), nil
}

func (c *rawUndefinedCondition) Paths() []string { return []string{string(c.Path)} }

func (c *rawUndefinedCondition) WithRelativePath(prefix string) InternalCondition {
	// pathParts := core.ParsePath(string(c.Path)) // Unused
	prefixParts := core.ParsePath(prefix)
	newPath := c.Path.StripParts(prefixParts)

	return &rawUndefinedCondition{Path: newPath}
}

type rawTypeCondition struct {
	Path     core.DeepPath
	TypeName string
}

func (c *rawTypeCondition) EvaluateAny(v any) (bool, error) {
	rv := core.ToReflectValue(v)
	target, err := c.Path.Resolve(rv)
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

func (c *rawTypeCondition) Paths() []string { return []string{string(c.Path)} }

func (c *rawTypeCondition) WithRelativePath(prefix string) InternalCondition {
	prefixParts := core.ParsePath(prefix)
	return &rawTypeCondition{Path: c.Path.StripParts(prefixParts), TypeName: c.TypeName}
}

type rawStringCondition struct {
	Path       core.DeepPath
	Val        string
	Op         string
	IgnoreCase bool
}

func (c *rawStringCondition) EvaluateAny(v any) (bool, error) {
	rv := core.ToReflectValue(v)
	target, err := c.Path.Resolve(rv)
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

func (c *rawStringCondition) Paths() []string { return []string{string(c.Path)} }

func (c *rawStringCondition) WithRelativePath(prefix string) InternalCondition {
	prefixParts := core.ParsePath(prefix)
	return &rawStringCondition{
		Path:       c.Path.StripParts(prefixParts),
		Val:        c.Val,
		Op:         c.Op,
		IgnoreCase: c.IgnoreCase,
	}
}

type rawInCondition struct {
	Path       core.DeepPath
	Values     []any
	IgnoreCase bool
}

func (c *rawInCondition) EvaluateAny(v any) (bool, error) {
	rv := core.ToReflectValue(v)
	target, err := c.Path.Resolve(rv)
	if err != nil {
		return false, nil
	}
	for _, val := range c.Values {
		match, err := core.CompareValues(target, reflect.ValueOf(val), "==", c.IgnoreCase)
		if err != nil {
			return false, err
		}
		if match {
			return true, nil
		}
	}
	return false, nil
}

func (c *rawInCondition) Paths() []string { return []string{string(c.Path)} }

func (c *rawInCondition) WithRelativePath(prefix string) InternalCondition {
	prefixParts := core.ParsePath(prefix)
	return &rawInCondition{
		Path:       c.Path.StripParts(prefixParts),
		Values:     c.Values,
		IgnoreCase: c.IgnoreCase,
	}
}

type rawLogCondition struct {
	Message string
}

func (c *rawLogCondition) EvaluateAny(v any) (bool, error) {
	fmt.Printf("DEEP LOG CONDITION: %s (value: %v)\n", c.Message, v)
	return true, nil
}

func (c *rawLogCondition) Paths() []string { return nil }

func (c *rawLogCondition) WithRelativePath(prefix string) InternalCondition {
	return c
}

type rawCompareCondition struct {
	Path       core.DeepPath
	Val        any
	Op         string
	IgnoreCase bool
}

func (c *rawCompareCondition) EvaluateAny(v any) (bool, error) {
	rv := core.ToReflectValue(v)
	target, err := c.Path.Resolve(rv)
	if err != nil {
		return false, err
	}
	return core.CompareValues(target, reflect.ValueOf(c.Val), c.Op, c.IgnoreCase)
}

func (c *rawCompareCondition) Paths() []string {
	return []string{string(c.Path)}
}

func (c *rawCompareCondition) WithRelativePath(prefix string) InternalCondition {
	prefixParts := core.ParsePath(prefix)
	return &rawCompareCondition{
		Path:       c.Path.StripParts(prefixParts),
		Val:        c.Val,
		Op:         c.Op,
		IgnoreCase: c.IgnoreCase,
	}
}

type rawCompareFieldCondition struct {
	Path1      core.DeepPath
	Path2      core.DeepPath
	Op         string
	IgnoreCase bool
}

func (c *rawCompareFieldCondition) EvaluateAny(v any) (bool, error) {
	rv := core.ToReflectValue(v)
	target1, err := c.Path1.Resolve(rv)
	if err != nil {
		return false, err
	}
	target2, err := c.Path2.Resolve(rv)
	if err != nil {
		return false, err
	}
	return core.CompareValues(target1, target2, c.Op, c.IgnoreCase)
}

func (c *rawCompareFieldCondition) Paths() []string {
	return []string{string(c.Path1), string(c.Path2)}
}

func (c *rawCompareFieldCondition) WithRelativePath(prefix string) InternalCondition {
	prefixParts := core.ParsePath(prefix)
	return &rawCompareFieldCondition{
		Path1:      c.Path1.StripParts(prefixParts),
		Path2:      c.Path2.StripParts(prefixParts),
		Op:         c.Op,
		IgnoreCase: c.IgnoreCase,
	}
}

type rawAndCondition struct {
	Conditions []InternalCondition
}

func (c *rawAndCondition) EvaluateAny(v any) (bool, error) {
	for _, sub := range c.Conditions {
		ok, err := sub.EvaluateAny(v)
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
	}
	return true, nil
}

func (c *rawAndCondition) Paths() []string {
	var res []string
	for _, sub := range c.Conditions {
		res = append(res, sub.Paths()...)
	}
	return res
}

func (c *rawAndCondition) WithRelativePath(prefix string) InternalCondition {
	res := &rawAndCondition{Conditions: make([]InternalCondition, len(c.Conditions))}
	for i, sub := range c.Conditions {
		res.Conditions[i] = sub.WithRelativePath(prefix)
	}
	return res
}

type rawOrCondition struct {
	Conditions []InternalCondition
}

func (c *rawOrCondition) EvaluateAny(v any) (bool, error) {
	for _, sub := range c.Conditions {
		ok, err := sub.EvaluateAny(v)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

func (c *rawOrCondition) Paths() []string {
	var res []string
	for _, sub := range c.Conditions {
		res = append(res, sub.Paths()...)
	}
	return res
}

func (c *rawOrCondition) WithRelativePath(prefix string) InternalCondition {
	res := &rawOrCondition{Conditions: make([]InternalCondition, len(c.Conditions))}
	for i, sub := range c.Conditions {
		res.Conditions[i] = sub.WithRelativePath(prefix)
	}
	return res
}

type rawNotCondition struct {
	C InternalCondition
}

func (c *rawNotCondition) EvaluateAny(v any) (bool, error) {
	ok, err := c.C.EvaluateAny(v)
	if err != nil {
		return false, err
	}
	return !ok, nil
}

func (c *rawNotCondition) Paths() []string {
	return c.C.Paths()
}

func (c *rawNotCondition) WithRelativePath(prefix string) InternalCondition {
	return &rawNotCondition{C: c.C.WithRelativePath(prefix)}
}
