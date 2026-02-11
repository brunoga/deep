package deep

import (
	"encoding/gob"
	"encoding/json"
	"fmt"
	"reflect"
)

type patchSurrogate struct {
	Kind string `json:"k" gob:"k"`
	Data any    `json:"d,omitempty" gob:"d,omitempty"`
}

func makeSurrogate(kind string, data map[string]any, p diffPatch) (*patchSurrogate, error) {
	cond, ifCond, unlessCond := p.conditions()
	c, err := marshalConditionAny(cond)
	if err != nil {
		return nil, err
	}
	if c != nil {
		data["c"] = c
	}
	ic, err := marshalConditionAny(ifCond)
	if err != nil {
		return nil, err
	}
	if ic != nil {
		data["if"] = ic
	}
	uc, err := marshalConditionAny(unlessCond)
	if err != nil {
		return nil, err
	}
	if uc != nil {
		data["un"] = uc
	}
	return &patchSurrogate{Kind: kind, Data: data}, nil
}

func marshalDiffPatch(p diffPatch) (any, error) {
	if p == nil {
		return nil, nil
	}
	switch v := p.(type) {
	case *valuePatch:
		return makeSurrogate("value", map[string]any{
			"o": valueToInterface(v.oldVal),
			"n": valueToInterface(v.newVal),
		}, v)
	case *ptrPatch:
		elem, err := marshalDiffPatch(v.elemPatch)
		if err != nil {
			return nil, err
		}
		return makeSurrogate("ptr", map[string]any{
			"p": elem,
		}, v)
	case *interfacePatch:
		elem, err := marshalDiffPatch(v.elemPatch)
		if err != nil {
			return nil, err
		}
		return makeSurrogate("interface", map[string]any{
			"p": elem,
		}, v)
	case *structPatch:
		fields := make(map[string]any)
		for name, patch := range v.fields {
			p, err := marshalDiffPatch(patch)
			if err != nil {
				return nil, err
			}
			fields[name] = p
		}
		return makeSurrogate("struct", map[string]any{
			"f": fields,
		}, v)
	case *arrayPatch:
		indices := make(map[string]any)
		for idx, patch := range v.indices {
			p, err := marshalDiffPatch(patch)
			if err != nil {
				return nil, err
			}
			indices[fmt.Sprintf("%d", idx)] = p
		}
		return makeSurrogate("array", map[string]any{
			"i": indices,
		}, v)
	case *mapPatch:
		added := make([]map[string]any, 0, len(v.added))
		for k, val := range v.added {
			added = append(added, map[string]any{"k": k, "v": valueToInterface(val)})
		}
		removed := make([]map[string]any, 0, len(v.removed))
		for k, val := range v.removed {
			removed = append(removed, map[string]any{"k": k, "v": valueToInterface(val)})
		}
		modified := make([]map[string]any, 0, len(v.modified))
		for k, patch := range v.modified {
			p, err := marshalDiffPatch(patch)
			if err != nil {
				return nil, err
			}
			modified = append(modified, map[string]any{"k": k, "p": p})
		}
		return makeSurrogate("map", map[string]any{
			"a": added,
			"r": removed,
			"m": modified,
		}, v)
	case *slicePatch:
		ops := make([]map[string]any, 0, len(v.ops))
		for _, op := range v.ops {
			p, err := marshalDiffPatch(op.Patch)
			if err != nil {
				return nil, err
			}
			ops = append(ops, map[string]any{
				"k": int(op.Kind),
				"i": op.Index,
				"v": valueToInterface(op.Val),
				"p": p,
			})
		}
		return makeSurrogate("slice", map[string]any{
			"o": ops,
		}, v)
	case *testPatch:
		return makeSurrogate("test", map[string]any{
			"e": valueToInterface(v.expected),
		}, v)
	case *copyPatch:
		return makeSurrogate("copy", map[string]any{
			"f": v.from,
		}, v)
	case *movePatch:
		return makeSurrogate("move", map[string]any{
			"f": v.from,
		}, v)
	case *logPatch:
		return makeSurrogate("log", map[string]any{
			"m": v.message,
		}, v)
	}
	return nil, fmt.Errorf("unknown patch type: %T", p)
}

func unmarshalDiffPatch(data []byte) (diffPatch, error) {
	var s patchSurrogate
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return convertFromSurrogate(&s)
}

func unmarshalCondFromMap(d map[string]any, key string) any {
	if cData, ok := d[key]; ok && cData != nil {
		jsonData, _ := json.Marshal(cData)
		c, _ := unmarshalCondition[any](jsonData)
		return c
	}
	return nil
}

func convertFromSurrogate(s any) (diffPatch, error) {
	if s == nil {
		return nil, nil
	}

	var kind string
	var data any

	switch v := s.(type) {
	case *patchSurrogate:
		kind = v.Kind
		data = v.Data
	case map[string]any:
		kind = v["k"].(string)
		data = v["d"]
	default:
		return nil, fmt.Errorf("invalid surrogate type: %T", s)
	}

	switch kind {
	case "value":
		d := data.(map[string]any)
		return &valuePatch{
			oldVal: interfaceToValue(d["o"]),
			newVal: interfaceToValue(d["n"]),
			patchMetadata: patchMetadata{
				cond:       unmarshalCondFromMap(d, "c"),
				ifCond:     unmarshalCondFromMap(d, "if"),
				unlessCond: unmarshalCondFromMap(d, "un"),
			},
		}, nil
	case "ptr":
		d := data.(map[string]any)
		elem, err := convertFromSurrogate(d["p"])
		if err != nil {
			return nil, err
		}
		return &ptrPatch{
			elemPatch: elem,
			patchMetadata: patchMetadata{
				cond:       unmarshalCondFromMap(d, "c"),
				ifCond:     unmarshalCondFromMap(d, "if"),
				unlessCond: unmarshalCondFromMap(d, "un"),
			},
		}, nil
	case "interface":
		d := data.(map[string]any)
		elem, err := convertFromSurrogate(d["p"])
		if err != nil {
			return nil, err
		}
		return &interfacePatch{
			elemPatch: elem,
			patchMetadata: patchMetadata{
				cond:       unmarshalCondFromMap(d, "c"),
				ifCond:     unmarshalCondFromMap(d, "if"),
				unlessCond: unmarshalCondFromMap(d, "un"),
			},
		}, nil
	case "struct":
		d := data.(map[string]any)
		fieldsData := d["f"].(map[string]any)
		fields := make(map[string]diffPatch)
		for name, pData := range fieldsData {
			p, err := convertFromSurrogate(pData)
			if err != nil {
				return nil, err
			}
			fields[name] = p
		}
		return &structPatch{
			fields: fields,
			patchMetadata: patchMetadata{
				cond:       unmarshalCondFromMap(d, "c"),
				ifCond:     unmarshalCondFromMap(d, "if"),
				unlessCond: unmarshalCondFromMap(d, "un"),
			},
		}, nil
	case "array":
		d := data.(map[string]any)
		indicesData := d["i"].(map[string]any)
		indices := make(map[int]diffPatch)
		for idxStr, pData := range indicesData {
			var idx int
			fmt.Sscanf(idxStr, "%d", &idx)
			p, err := convertFromSurrogate(pData)
			if err != nil {
				return nil, err
			}
			indices[idx] = p
		}
		return &arrayPatch{
			indices: indices,
			patchMetadata: patchMetadata{
				cond:       unmarshalCondFromMap(d, "c"),
				ifCond:     unmarshalCondFromMap(d, "if"),
				unlessCond: unmarshalCondFromMap(d, "un"),
			},
		}, nil
	case "map":
		d := data.(map[string]any)
		added := make(map[interface{}]reflect.Value)
		if a := d["a"]; a != nil {
			if slice, ok := a.([]any); ok {
				for _, entry := range slice {
					e := entry.(map[string]any)
					added[e["k"]] = interfaceToValue(e["v"])
				}
			} else if slice, ok := a.([]map[string]any); ok {
				for _, e := range slice {
					added[e["k"]] = interfaceToValue(e["v"])
				}
			}
		}
		removed := make(map[interface{}]reflect.Value)
		if r := d["r"]; r != nil {
			if slice, ok := r.([]any); ok {
				for _, entry := range slice {
					e := entry.(map[string]any)
					removed[e["k"]] = interfaceToValue(e["v"])
				}
			} else if slice, ok := r.([]map[string]any); ok {
				for _, e := range slice {
					removed[e["k"]] = interfaceToValue(e["v"])
				}
			}
		}
		modified := make(map[interface{}]diffPatch)
		if m := d["m"]; m != nil {
			if slice, ok := m.([]any); ok {
				for _, entry := range slice {
					e := entry.(map[string]any)
					p, err := convertFromSurrogate(e["p"])
					if err != nil {
						return nil, err
					}
					modified[e["k"]] = p
				}
			} else if slice, ok := m.([]map[string]any); ok {
				for _, e := range slice {
					p, err := convertFromSurrogate(e["p"])
					if err != nil {
						return nil, err
					}
					modified[e["k"]] = p
				}
			}
		}
		return &mapPatch{
			added:    added,
			removed:  removed,
			modified: modified,
			patchMetadata: patchMetadata{
				cond:       unmarshalCondFromMap(d, "c"),
				ifCond:     unmarshalCondFromMap(d, "if"),
				unlessCond: unmarshalCondFromMap(d, "un"),
			},
		}, nil
	case "slice":
		d := data.(map[string]any)
		var opsDataRaw []any
		if raw, ok := d["o"].([]any); ok {
			opsDataRaw = raw
		} else if raw, ok := d["o"].([]map[string]any); ok {
			for _, m := range raw {
				opsDataRaw = append(opsDataRaw, m)
			}
		}

		ops := make([]sliceOp, 0, len(opsDataRaw))
		for _, oRaw := range opsDataRaw {
			var o map[string]any
			switch v := oRaw.(type) {
			case map[string]any:
				o = v
			case *patchSurrogate:
				o = v.Data.(map[string]any)
			}
			p, err := convertFromSurrogate(o["p"])
			if err != nil {
				return nil, err
			}

			var kind float64
			switch k := o["k"].(type) {
			case float64:
				kind = k
			case int:
				kind = float64(k)
			}

			var index float64
			switch i := o["i"].(type) {
			case float64:
				index = i
			case int:
				index = float64(i)
			}

			ops = append(ops, sliceOp{
				Kind:  opKind(int(kind)),
				Index: int(index),
				Val:   interfaceToValue(o["v"]),
				Patch: p,
			})
		}
		return &slicePatch{
			ops: ops,
			patchMetadata: patchMetadata{
				cond:       unmarshalCondFromMap(d, "c"),
				ifCond:     unmarshalCondFromMap(d, "if"),
				unlessCond: unmarshalCondFromMap(d, "un"),
			},
		}, nil
	case "test":
		d := data.(map[string]any)
		return &testPatch{
			expected: interfaceToValue(d["e"]),
			patchMetadata: patchMetadata{
				cond:       unmarshalCondFromMap(d, "c"),
				ifCond:     unmarshalCondFromMap(d, "if"),
				unlessCond: unmarshalCondFromMap(d, "un"),
			},
		}, nil
	case "copy":
		d := data.(map[string]any)
		return &copyPatch{
			from: d["f"].(string),
			patchMetadata: patchMetadata{
				cond:       unmarshalCondFromMap(d, "c"),
				ifCond:     unmarshalCondFromMap(d, "if"),
				unlessCond: unmarshalCondFromMap(d, "un"),
			},
		}, nil
	case "move":
		d := data.(map[string]any)
		return &movePatch{
			from: d["f"].(string),
			patchMetadata: patchMetadata{
				cond:       unmarshalCondFromMap(d, "c"),
				ifCond:     unmarshalCondFromMap(d, "if"),
				unlessCond: unmarshalCondFromMap(d, "un"),
			},
		}, nil
	case "log":
		d := data.(map[string]any)
		return &logPatch{
			message: d["m"].(string),
			patchMetadata: patchMetadata{
				cond:       unmarshalCondFromMap(d, "c"),
				ifCond:     unmarshalCondFromMap(d, "if"),
				unlessCond: unmarshalCondFromMap(d, "un"),
			},
		}, nil
	}
	return nil, fmt.Errorf("unknown patch kind: %s", kind)
}

func init() {
	gob.Register(&patchSurrogate{})
	gob.Register(map[string]any{})
	gob.Register([]any{})
	gob.Register([]map[string]any{})
}
