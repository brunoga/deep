package cond

import (
	"encoding/gob"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/brunoga/deep/v5/internal/core"
)

func init() {
	gob.Register(&condSurrogate{})
}

type condSurrogate struct {
	Kind string `json:"k" gob:"k"`
	Data any    `json:"d,omitempty" gob:"d,omitempty"`
}

// ConditionToSerializable returns a serializable representation of the condition.
func ConditionToSerializable(c any) (any, error) {
	if c == nil {
		return nil, nil
	}

	if t, ok := c.(interface{ unwrap() InternalCondition }); ok {
		return ConditionToSerializable(t.unwrap())
	}

	return MarshalConditionAny(c)
}

func MarshalCondition[T any](c Condition[T]) (any, error) {
	if t, ok := c.(*typedCondition[T]); ok {
		return MarshalConditionAny(t.inner)
	}
	return MarshalConditionAny(c)
}

func MarshalConditionAny(c any) (any, error) {
	if c == nil {
		return nil, nil
	}

	// Use reflection to extract the underlying fields regardless of T.
	v := reflect.ValueOf(c)
	if v.Kind() == reflect.Pointer {
		v = v.Elem()
	}

	typeName := v.Type().Name()
	if strings.HasPrefix(typeName, "rawCompareCondition") {
		op := v.FieldByName("Op").String()
		kind := "compare"
		if op == "==" {
			kind = "equal"
		} else if op == "!=" {
			kind = "not_equal"
		}
		return &condSurrogate{
			Kind: kind,
			Data: map[string]any{
				"p":  string(v.FieldByName("Path").Interface().(core.DeepPath)),
				"v":  v.FieldByName("Val").Interface(),
				"o":  op,
				"ic": v.FieldByName("IgnoreCase").Bool(),
			},
		}, nil
	}
	if strings.HasPrefix(typeName, "rawCompareFieldCondition") {
		op := v.FieldByName("Op").String()
		kind := "compare_field"
		if op == "==" {
			kind = "equal_field"
		} else if op == "!=" {
			kind = "not_equal_field"
		}
		return &condSurrogate{
			Kind: kind,
			Data: map[string]any{
				"p1": string(v.FieldByName("Path1").Interface().(core.DeepPath)),
				"p2": string(v.FieldByName("Path2").Interface().(core.DeepPath)),
				"o":  op,
				"ic": v.FieldByName("IgnoreCase").Bool(),
			},
		}, nil
	}
	if strings.HasPrefix(typeName, "rawDefinedCondition") {
		return &condSurrogate{
			Kind: "defined",
			Data: map[string]any{
				"p": string(v.FieldByName("Path").Interface().(core.DeepPath)),
			},
		}, nil
	}
	if strings.HasPrefix(typeName, "rawUndefinedCondition") {
		return &condSurrogate{
			Kind: "undefined",
			Data: map[string]any{
				"p": string(v.FieldByName("Path").Interface().(core.DeepPath)),
			},
		}, nil
	}
	if strings.HasPrefix(typeName, "rawTypeCondition") {
		return &condSurrogate{
			Kind: "type",
			Data: map[string]any{
				"p": string(v.FieldByName("Path").Interface().(core.DeepPath)),
				"t": v.FieldByName("TypeName").String(),
			},
		}, nil
	}
	if strings.HasPrefix(typeName, "rawStringCondition") {
		return &condSurrogate{
			Kind: "string",
			Data: map[string]any{
				"p":  string(v.FieldByName("Path").Interface().(core.DeepPath)),
				"v":  v.FieldByName("Val").String(),
				"o":  v.FieldByName("Op").String(),
				"ic": v.FieldByName("IgnoreCase").Bool(),
			},
		}, nil
	}
	if strings.HasPrefix(typeName, "rawInCondition") {
		return &condSurrogate{
			Kind: "in",
			Data: map[string]any{
				"p":  string(v.FieldByName("Path").Interface().(core.DeepPath)),
				"v":  v.FieldByName("Values").Interface(),
				"ic": v.FieldByName("IgnoreCase").Bool(),
			},
		}, nil
	}
	if strings.HasPrefix(typeName, "rawLogCondition") {
		return &condSurrogate{
			Kind: "log",
			Data: map[string]any{
				"m": v.FieldByName("Message").String(),
			},
		}, nil
	}
	if strings.HasPrefix(typeName, "rawAndCondition") {
		condsVal := v.FieldByName("Conditions")
		conds := make([]any, 0, condsVal.Len())
		for i := 0; i < condsVal.Len(); i++ {
			s, err := MarshalConditionAny(condsVal.Index(i).Interface())
			if err != nil {
				return nil, err
			}
			conds = append(conds, s)
		}
		return &condSurrogate{
			Kind: "and",
			Data: conds,
		}, nil
	}
	if strings.HasPrefix(typeName, "rawOrCondition") {
		condsVal := v.FieldByName("Conditions")
		conds := make([]any, 0, condsVal.Len())
		for i := 0; i < condsVal.Len(); i++ {
			s, err := MarshalConditionAny(condsVal.Index(i).Interface())
			if err != nil {
				return nil, err
			}
			conds = append(conds, s)
		}
		return &condSurrogate{
			Kind: "or",
			Data: conds,
		}, nil
	}
	if strings.HasPrefix(typeName, "rawNotCondition") {
		sub, err := MarshalConditionAny(v.FieldByName("C").Interface())
		if err != nil {
			return nil, err
		}
		return &condSurrogate{
			Kind: "not",
			Data: sub,
		}, nil
	}

	return nil, fmt.Errorf("unknown condition type: %T", c)
}

func UnmarshalCondition[T any](data []byte) (Condition[T], error) {
	var s condSurrogate
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return UnmarshalConditionSurrogate[T](&s)
}

// ConditionFromSerializable reconstructs a condition from its serializable representation.
func ConditionFromSerializable[T any](s any) (Condition[T], error) {
	return UnmarshalConditionSurrogate[T](s)
}

func UnmarshalConditionSurrogate[T any](s any) (Condition[T], error) {
	if s == nil {
		return nil, nil
	}

	var kind string
	var data any

	switch v := s.(type) {
	case *condSurrogate:
		kind = v.Kind
		data = v.Data
	case map[string]any:
		kind = v["k"].(string)
		data = v["d"]
	default:
		return nil, fmt.Errorf("invalid condition surrogate type: %T", s)
	}

	var inner InternalCondition

	switch kind {
	case "equal":
		d := data.(map[string]any)
		ic := getBool(d, "ic")
		inner = &rawCompareCondition{Path: core.DeepPath(d["p"].(string)), Val: d["v"], Op: "==", IgnoreCase: ic}
	case "not_equal":
		d := data.(map[string]any)
		ic := getBool(d, "ic")
		inner = &rawCompareCondition{Path: core.DeepPath(d["p"].(string)), Val: d["v"], Op: "!=", IgnoreCase: ic}
	case "compare":
		d := data.(map[string]any)
		ic := getBool(d, "ic")
		inner = &rawCompareCondition{Path: core.DeepPath(d["p"].(string)), Val: d["v"], Op: d["o"].(string), IgnoreCase: ic}
	case "equal_field":
		d := data.(map[string]any)
		ic := getBool(d, "ic")
		inner = &rawCompareFieldCondition{Path1: core.DeepPath(d["p1"].(string)), Path2: core.DeepPath(d["p2"].(string)), Op: "==", IgnoreCase: ic}
	case "not_equal_field":
		d := data.(map[string]any)
		ic := getBool(d, "ic")
		inner = &rawCompareFieldCondition{Path1: core.DeepPath(d["p1"].(string)), Path2: core.DeepPath(d["p2"].(string)), Op: "!=", IgnoreCase: ic}
	case "compare_field":
		d := data.(map[string]any)
		ic := getBool(d, "ic")
		inner = &rawCompareFieldCondition{Path1: core.DeepPath(d["p1"].(string)), Path2: core.DeepPath(d["p2"].(string)), Op: d["o"].(string), IgnoreCase: ic}
	case "defined":
		d := data.(map[string]any)
		inner = &rawDefinedCondition{Path: core.DeepPath(d["p"].(string))}
	case "undefined":
		d := data.(map[string]any)
		inner = &rawUndefinedCondition{Path: core.DeepPath(d["p"].(string))}
	case "type":
		d := data.(map[string]any)
		inner = &rawTypeCondition{Path: core.DeepPath(d["p"].(string)), TypeName: d["t"].(string)}
	case "string":
		d := data.(map[string]any)
		ic := getBool(d, "ic")
		inner = &rawStringCondition{Path: core.DeepPath(d["p"].(string)), Val: d["v"].(string), Op: d["o"].(string), IgnoreCase: ic}
	case "in":
		d := data.(map[string]any)
		ic := getBool(d, "ic")
		vals := d["v"].([]any)
		inner = &rawInCondition{Path: core.DeepPath(d["p"].(string)), Values: vals, IgnoreCase: ic}
	case "log":
		d := data.(map[string]any)
		inner = &rawLogCondition{Message: d["m"].(string)}
	case "and":
		d := data.([]any)
		conds := make([]InternalCondition, 0, len(d))
		for _, subData := range d {
			sub, err := UnmarshalConditionSurrogate[T](subData)
			if err != nil {
				return nil, err
			}
			// sub is Condition[T]. We need InternalCondition.
			if t, ok := sub.(*typedCondition[T]); ok {
				conds = append(conds, t.inner)
			} else {
				return nil, fmt.Errorf("unexpected condition type in and: %T", sub)
			}
		}
		inner = &rawAndCondition{Conditions: conds}
	case "or":
		d := data.([]any)
		conds := make([]InternalCondition, 0, len(d))
		for _, subData := range d {
			sub, err := UnmarshalConditionSurrogate[T](subData)
			if err != nil {
				return nil, err
			}
			if t, ok := sub.(*typedCondition[T]); ok {
				conds = append(conds, t.inner)
			} else {
				return nil, fmt.Errorf("unexpected condition type in or: %T", sub)
			}
		}
		inner = &rawOrCondition{Conditions: conds}
	case "not":
		sub, err := UnmarshalConditionSurrogate[T](data)
		if err != nil {
			return nil, err
		}
		if t, ok := sub.(*typedCondition[T]); ok {
			inner = &rawNotCondition{C: t.inner}
		} else {
			return nil, fmt.Errorf("unexpected condition type in not: %T", sub)
		}
	default:
		return nil, fmt.Errorf("unknown condition kind: %s", kind)
	}

	return &typedCondition[T]{inner: inner}, nil
}

func getBool(d map[string]any, key string) bool {
	if v, ok := d[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
}
