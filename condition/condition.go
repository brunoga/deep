package condition

import (
	"fmt"
	"reflect"
	"regexp"

	icore "github.com/brunoga/deep/v5/internal/core"
)

// Condition operator constants.
const (
	Eq      = "=="
	Ne      = "!="
	Gt      = ">"
	Lt      = "<"
	Ge      = ">="
	Le      = "<="
	Exists  = "exists"
	In      = "in"
	Matches = "matches"
	Type    = "type"
	And     = "and"
	Or      = "or"
	Not     = "not"
)

// Condition represents a serializable predicate for conditional application.
type Condition struct {
	Path  string       `json:"p,omitempty"`
	Op    string       `json:"o"` // see operator constants above
	Value any          `json:"v,omitempty"`
	Sub   []*Condition `json:"apply,omitempty"` // Sub-conditions for logical operators (and, or, not)
}

// Evaluate evaluates a condition against a root value.
func Evaluate(root reflect.Value, c *Condition) (bool, error) {
	if c == nil {
		return true, nil
	}

	if c.Op == And {
		for _, sub := range c.Sub {
			ok, err := Evaluate(root, sub)
			if err != nil || !ok {
				return false, err
			}
		}
		return true, nil
	}
	if c.Op == Or {
		for _, sub := range c.Sub {
			ok, err := Evaluate(root, sub)
			if err == nil && ok {
				return true, nil
			}
		}
		return false, nil
	}
	if c.Op == Not {
		if len(c.Sub) > 0 {
			ok, err := Evaluate(root, c.Sub[0])
			if err != nil {
				return false, err
			}
			return !ok, nil
		}
	}

	val, err := icore.DeepPath(c.Path).Resolve(root)
	if err != nil {
		if c.Op == Exists {
			return false, nil
		}
		return false, err
	}

	if c.Op == Exists {
		return val.IsValid(), nil
	}

	if c.Op == Matches {
		pattern, ok := c.Value.(string)
		if !ok {
			return false, fmt.Errorf("matches requires string pattern")
		}
		matched, err := regexp.MatchString(pattern, fmt.Sprintf("%v", val.Interface()))
		if err != nil {
			return false, fmt.Errorf("invalid regex pattern: %w", err)
		}
		return matched, nil
	}

	if c.Op == In {
		v := reflect.ValueOf(c.Value)
		if v.Kind() != reflect.Slice && v.Kind() != reflect.Array {
			return false, fmt.Errorf("in requires slice or array")
		}
		for i := 0; i < v.Len(); i++ {
			if icore.Equal(val.Interface(), v.Index(i).Interface()) {
				return true, nil
			}
		}
		return false, nil
	}

	if c.Op == Type {
		typeName, ok := c.Value.(string)
		if !ok {
			return false, fmt.Errorf("type requires string value")
		}
		return CheckType(val.Interface(), typeName), nil
	}

	return icore.CompareValues(val, reflect.ValueOf(c.Value), c.Op, false)
}

// ToPredicate returns a JSON-serializable map representing the condition in
// the JSON Patch predicate wire format. This is the inverse of [FromPredicate].
func (c *Condition) ToPredicate() map[string]any {
	if c == nil {
		return nil
	}

	op := c.Op
	switch op {
	case Eq:
		op = "test"
	case Ne:
		// Not equal is a 'not' predicate in some extensions
		return map[string]any{
			"op": "not",
			"apply": []map[string]any{
				{"op": "test", "path": c.Path, "value": c.Value},
			},
		}
	case Gt:
		op = "more"
	case Ge:
		op = "more-or-equal"
	case Lt:
		op = "less"
	case Le:
		op = "less-or-equal"
	case Exists:
		op = "defined"
	case In:
		op = "contains"
	case "log":
		op = "log"
	case Matches:
		op = "matches"
	case Type:
		op = "type"
	case And, Or, Not:
		res := map[string]any{
			"op": op,
		}
		var apply []map[string]any
		for _, sub := range c.Sub {
			apply = append(apply, sub.ToPredicate())
		}
		res["apply"] = apply
		return res
	}

	return map[string]any{
		"op":    op,
		"path":  c.Path,
		"value": c.Value,
	}
}

// FromPredicate parses a JSON Patch predicate wire-format map into a
// [Condition]. This is the inverse of [Condition.ToPredicate].
func FromPredicate(m map[string]any) *Condition {
	if m == nil {
		return nil
	}
	op, _ := m["op"].(string)
	path, _ := m["path"].(string)
	value := m["value"]

	switch op {
	case "test":
		return &Condition{Path: path, Op: Eq, Value: value}
	case "not":
		// Could be encoded != or a logical not.
		// If it wraps a single test on the same path, treat as !=.
		if apply, ok := m["apply"].([]any); ok && len(apply) == 1 {
			if inner, ok := apply[0].(map[string]any); ok {
				if inner["op"] == "test" {
					innerPath, _ := inner["path"].(string)
					return &Condition{Path: innerPath, Op: Ne, Value: inner["value"]}
				}
			}
		}
		return &Condition{Op: Not, Sub: parseApply(m["apply"])}
	case "more":
		return &Condition{Path: path, Op: Gt, Value: value}
	case "more-or-equal":
		return &Condition{Path: path, Op: Ge, Value: value}
	case "less":
		return &Condition{Path: path, Op: Lt, Value: value}
	case "less-or-equal":
		return &Condition{Path: path, Op: Le, Value: value}
	case "defined":
		return &Condition{Path: path, Op: Exists}
	case "contains":
		return &Condition{Path: path, Op: In, Value: value}
	case And, Or:
		return &Condition{Op: op, Sub: parseApply(m["apply"])}
	default:
		// log, matches, type — same op name, pass through
		return &Condition{Path: path, Op: op, Value: value}
	}
}

func parseApply(raw any) []*Condition {
	items, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]*Condition, 0, len(items))
	for _, item := range items {
		if m, ok := item.(map[string]any); ok {
			if c := FromPredicate(m); c != nil {
				out = append(out, c)
			}
		}
	}
	return out
}

// CheckType reports whether v matches the given type name.
func CheckType(v any, typeName string) bool {
	rv := reflect.ValueOf(v)
	switch typeName {
	case "string":
		return rv.Kind() == reflect.String
	case "number":
		k := rv.Kind()
		return (k >= reflect.Int && k <= reflect.Int64) ||
			(k >= reflect.Uint && k <= reflect.Uintptr) ||
			(k == reflect.Float32 || k == reflect.Float64)
	case "boolean":
		return rv.Kind() == reflect.Bool
	case "object":
		return rv.Kind() == reflect.Struct || rv.Kind() == reflect.Map
	case "array":
		return rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array
	case "null":
		if !rv.IsValid() {
			return true
		}
		return (rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface || rv.Kind() == reflect.Slice || rv.Kind() == reflect.Map) && rv.IsNil()
	}
	return false
}
