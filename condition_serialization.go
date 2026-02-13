package deep

import (
	"encoding/gob"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

func init() {
	gob.Register(&condSurrogate{})
}

type condSurrogate struct {
	Kind string `json:"k" gob:"k"`
	Data any    `json:"d,omitempty" gob:"d,omitempty"`
}

func marshalCondition[T any](c Condition[T]) (any, error) {
	return marshalConditionAny(c)
}

func marshalConditionAny(c any) (any, error) {
	if c == nil {
		return nil, nil
	}

	// Use reflection to extract the underlying fields regardless of T.
	v := reflect.ValueOf(c)
	if v.Kind() == reflect.Pointer {
		v = v.Elem()
	}

	typeName := v.Type().Name()
	if strings.HasPrefix(typeName, "CompareCondition") {
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
				"p":  v.FieldByName("Path").String(),
				"v":  v.FieldByName("Val").Interface(),
				"o":  op,
				"ic": v.FieldByName("IgnoreCase").Bool(),
			},
		}, nil
	}
	if strings.HasPrefix(typeName, "CompareFieldCondition") {
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
				"p1": v.FieldByName("Path1").String(),
				"p2": v.FieldByName("Path2").String(),
				"o":  op,
				"ic": v.FieldByName("IgnoreCase").Bool(),
			},
		}, nil
	}
	if strings.HasPrefix(typeName, "DefinedCondition") {
		return &condSurrogate{
			Kind: "defined",
			Data: map[string]any{
				"p": v.FieldByName("Path").String(),
			},
		}, nil
	}
	if strings.HasPrefix(typeName, "UndefinedCondition") {
		return &condSurrogate{
			Kind: "undefined",
			Data: map[string]any{
				"p": v.FieldByName("Path").String(),
			},
		}, nil
	}
	if strings.HasPrefix(typeName, "TypeCondition") {
		return &condSurrogate{
			Kind: "type",
			Data: map[string]any{
				"p": v.FieldByName("Path").String(),
				"t": v.FieldByName("TypeName").String(),
			},
		}, nil
	}
	if strings.HasPrefix(typeName, "StringCondition") {
		return &condSurrogate{
			Kind: "string",
			Data: map[string]any{
				"p":  v.FieldByName("Path").String(),
				"v":  v.FieldByName("Val").String(),
				"o":  v.FieldByName("Op").String(),
				"ic": v.FieldByName("IgnoreCase").Bool(),
			},
		}, nil
	}
	if strings.HasPrefix(typeName, "InCondition") {
		return &condSurrogate{
			Kind: "in",
			Data: map[string]any{
				"p":  v.FieldByName("Path").String(),
				"v":  v.FieldByName("Values").Interface(),
				"ic": v.FieldByName("IgnoreCase").Bool(),
			},
		}, nil
	}
	if strings.HasPrefix(typeName, "LogCondition") {
		return &condSurrogate{
			Kind: "log",
			Data: map[string]any{
				"m": v.FieldByName("Message").String(),
			},
		}, nil
	}
	if strings.HasPrefix(typeName, "AndCondition") {
		condsVal := v.FieldByName("Conditions")
		conds := make([]any, 0, condsVal.Len())
		for i := 0; i < condsVal.Len(); i++ {
			s, err := marshalConditionAny(condsVal.Index(i).Interface())
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
	if strings.HasPrefix(typeName, "OrCondition") {
		condsVal := v.FieldByName("Conditions")
		conds := make([]any, 0, condsVal.Len())
		for i := 0; i < condsVal.Len(); i++ {
			s, err := marshalConditionAny(condsVal.Index(i).Interface())
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
	if strings.HasPrefix(typeName, "NotCondition") {
		sub, err := marshalConditionAny(v.FieldByName("C").Interface())
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

func unmarshalCondition[T any](data []byte) (Condition[T], error) {
	var s condSurrogate
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return convertFromCondSurrogate[T](&s)
}

func convertFromCondSurrogate[T any](s any) (Condition[T], error) {
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

	switch kind {
	case "equal":
		d := data.(map[string]any)
		ic := getBool(d, "ic")
		return CompareCondition[T]{Path: Path(d["p"].(string)), Val: d["v"], Op: "==", IgnoreCase: ic}, nil
	case "not_equal":
		d := data.(map[string]any)
		ic := getBool(d, "ic")
		return CompareCondition[T]{Path: Path(d["p"].(string)), Val: d["v"], Op: "!=", IgnoreCase: ic}, nil
	case "compare":
		d := data.(map[string]any)
		ic := getBool(d, "ic")
		return CompareCondition[T]{Path: Path(d["p"].(string)), Val: d["v"], Op: d["o"].(string), IgnoreCase: ic}, nil
	case "equal_field":
		d := data.(map[string]any)
		ic := getBool(d, "ic")
		return CompareFieldCondition[T]{Path1: Path(d["p1"].(string)), Path2: Path(d["p2"].(string)), Op: "==", IgnoreCase: ic}, nil
	case "not_equal_field":
		d := data.(map[string]any)
		ic := getBool(d, "ic")
		return CompareFieldCondition[T]{Path1: Path(d["p1"].(string)), Path2: Path(d["p2"].(string)), Op: "!=", IgnoreCase: ic}, nil
	case "compare_field":
		d := data.(map[string]any)
		ic := getBool(d, "ic")
		return CompareFieldCondition[T]{Path1: Path(d["p1"].(string)), Path2: Path(d["p2"].(string)), Op: d["o"].(string), IgnoreCase: ic}, nil
	case "defined":
		d := data.(map[string]any)
		return DefinedCondition[T]{Path: Path(d["p"].(string))}, nil
	case "undefined":
		d := data.(map[string]any)
		return UndefinedCondition[T]{Path: Path(d["p"].(string))}, nil
	case "type":
		d := data.(map[string]any)
		return TypeCondition[T]{Path: Path(d["p"].(string)), TypeName: d["t"].(string)}, nil
	case "string":
		d := data.(map[string]any)
		ic := getBool(d, "ic")
		return StringCondition[T]{Path: Path(d["p"].(string)), Val: d["v"].(string), Op: d["o"].(string), IgnoreCase: ic}, nil
	case "in":
		d := data.(map[string]any)
		ic := getBool(d, "ic")
		return InCondition[T]{Path: Path(d["p"].(string)), Values: d["v"].([]any), IgnoreCase: ic}, nil
	case "log":
		d := data.(map[string]any)
		return LogCondition[T]{Message: d["m"].(string)}, nil
	case "and":
		d := data.([]any)
		conds := make([]Condition[T], 0, len(d))
		for _, subData := range d {
			sub, err := convertFromCondSurrogate[T](subData)
			if err != nil {
				return nil, err
			}
			conds = append(conds, sub)
		}
		return AndCondition[T]{Conditions: conds}, nil
	case "or":
		d := data.([]any)
		conds := make([]Condition[T], 0, len(d))
		for _, subData := range d {
			sub, err := convertFromCondSurrogate[T](subData)
			if err != nil {
				return nil, err
			}
			conds = append(conds, sub)
		}
		return OrCondition[T]{Conditions: conds}, nil
	case "not":
		sub, err := convertFromCondSurrogate[T](data)
		if err != nil {
			return nil, err
		}
		return NotCondition[T]{C: sub}, nil
	}

	return nil, fmt.Errorf("unknown condition kind: %s", kind)
}

func getBool(d map[string]any, key string) bool {
	if v, ok := d[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
}
