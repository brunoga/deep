package engine

import (
	"encoding/gob"
	"encoding/json"
	"fmt"
	"reflect"
	"sync"

	"github.com/brunoga/deep/v5/cond"
	"github.com/brunoga/deep/v5/internal/core"
)

type patchSurrogate struct {
	Kind string `json:"k" gob:"k"`
	Data any    `json:"d,omitempty" gob:"d,omitempty"`
}

func makeSurrogate(kind string, data map[string]any, p diffPatch) (*patchSurrogate, error) {
	c, ifC, unlessC := p.conditions()
	cData, err := cond.ConditionToSerializable(c)
	if err != nil {
		return nil, err
	}
	if cData != nil {
		data["c"] = cData
	}
	ifCData, err := cond.ConditionToSerializable(ifC)
	if err != nil {
		return nil, err
	}
	if ifCData != nil {
		data["if"] = ifCData
	}
	unlessCData, err := cond.ConditionToSerializable(unlessC)
	if err != nil {
		return nil, err
	}
	if unlessCData != nil {
		data["un"] = unlessCData
	}
	return &patchSurrogate{Kind: kind, Data: data}, nil
}

var (
	customPatchTypes = make(map[string]reflect.Type)
	muCustom         sync.RWMutex
)

// RegisterCustomPatch registers a custom patch implementation for serialization.
// The provided patch instance must implement interface { PatchKind() string }.
func RegisterCustomPatch(p any) {
	pk, ok := p.(interface{ PatchKind() string })
	if !ok {
		panic(fmt.Sprintf("RegisterCustomPatch: type %T does not implement PatchKind()", p))
	}
	kind := pk.PatchKind()
	muCustom.Lock()
	defer muCustom.Unlock()
	customPatchTypes[kind] = reflect.TypeOf(p)
}

// PatchToSerializable returns a serializable representation of the patch.
// This is intended for use by pluggable encoders (e.g. gRPC, custom binary formats).
func PatchToSerializable(p any) (any, error) {
	if p == nil {
		return nil, nil
	}

	var dp diffPatch
	if typed, ok := p.(patchUnwrapper); ok {
		dp = typed.unwrap()
	} else if direct, ok := p.(diffPatch); ok {
		dp = direct
	} else {
		return nil, fmt.Errorf("invalid patch type: %T", p)
	}

	return marshalDiffPatch(dp)
}

func marshalDiffPatch(p diffPatch) (any, error) {
	if p == nil {
		return nil, nil
	}
	switch v := p.(type) {
	case *valuePatch:
		return makeSurrogate("value", map[string]any{
			"o": core.ValueToInterface(v.oldVal),
			"n": core.ValueToInterface(v.newVal),
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
			added = append(added, map[string]any{"k": k, "v": core.ValueToInterface(val)})
		}
		removed := make([]map[string]any, 0, len(v.removed))
		for k, val := range v.removed {
			removed = append(removed, map[string]any{"k": k, "v": core.ValueToInterface(val)})
		}
		modified := make([]map[string]any, 0, len(v.modified))
		for k, patch := range v.modified {
			p, err := marshalDiffPatch(patch)
			if err != nil {
				return nil, err
			}
			modified = append(modified, map[string]any{"k": k, "p": p})
		}
		orig := make([]map[string]any, 0, len(v.originalKeys))
		for k, v := range v.originalKeys {
			orig = append(orig, map[string]any{"k": k, "v": v})
		}
		return makeSurrogate("map", map[string]any{
			"a": added,
			"r": removed,
			"m": modified,
			"o": orig,
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
				"v": core.ValueToInterface(op.Val),
				"p": p,
				"y": op.Key,
				"r": op.PrevKey,
			})
		}
		return makeSurrogate("slice", map[string]any{
			"o": ops,
		}, v)
	case *testPatch:
		return makeSurrogate("test", map[string]any{
			"e": core.ValueToInterface(v.expected),
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
	case *customDiffPatch:
		if pk, ok := v.patch.(interface{ PatchKind() string }); ok {
			return makeSurrogate("custom", map[string]any{
				"k": pk.PatchKind(),
				"v": v.patch,
			}, v)
		}
		return nil, fmt.Errorf("unknown patch type: %T (does not implement PatchKind())", v.patch)
	}
	return nil, fmt.Errorf("unknown patch type: %T", p)
}

func unmarshalCondFromMap(d map[string]any, key string) (any, error) {
	if cData, ok := d[key]; ok && cData != nil {
		// We use ConditionFromSerializable for 'any' since diffPatch stores 'any' (InternalCondition)
		c, err := cond.ConditionFromSerializable[any](cData)
		if err != nil {
			return nil, err
		}
		return c, nil
	}
	return nil, nil
}

func unmarshalBasePatch(d map[string]any) (basePatch, error) {
	c, err := unmarshalCondFromMap(d, "c")
	if err != nil {
		return basePatch{}, err
	}
	ifCond, err := unmarshalCondFromMap(d, "if")
	if err != nil {
		return basePatch{}, err
	}
	unlessCond, err := unmarshalCondFromMap(d, "un")
	if err != nil {
		return basePatch{}, err
	}
	return basePatch{
		cond:       c,
		ifCond:     ifCond,
		unlessCond: unlessCond,
	}, nil
}

// PatchFromSerializable reconstructs a patch from its serializable representation.
func PatchFromSerializable(s any) (any, error) {
	if s == nil {
		return nil, nil
	}
	return convertFromSurrogate(s)
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

	d := data.(map[string]any)
	base, err := unmarshalBasePatch(d)
	if err != nil {
		return nil, err
	}

	switch kind {
	case "value":
		return &valuePatch{
			oldVal:    core.InterfaceToValue(d["o"]),
			newVal:    core.InterfaceToValue(d["n"]),
			basePatch: base,
		}, nil
	case "ptr":
		elem, err := convertFromSurrogate(d["p"])
		if err != nil {
			return nil, err
		}
		return &ptrPatch{
			elemPatch: elem,
			basePatch: base,
		}, nil
	case "interface":
		elem, err := convertFromSurrogate(d["p"])
		if err != nil {
			return nil, err
		}
		return &interfacePatch{
			elemPatch: elem,
			basePatch: base,
		}, nil
	case "struct":
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
			fields:    fields,
			basePatch: base,
		}, nil
	case "array":
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
			indices:   indices,
			basePatch: base,
		}, nil
	case "map":
		added := make(map[any]reflect.Value)
		if a := d["a"]; a != nil {
			if slice, ok := a.([]any); ok {
				for _, entry := range slice {
					e := entry.(map[string]any)
					added[e["k"]] = core.InterfaceToValue(e["v"])
				}
			} else if slice, ok := a.([]map[string]any); ok {
				for _, e := range slice {
					added[e["k"]] = core.InterfaceToValue(e["v"])
				}
			}
		}
		removed := make(map[any]reflect.Value)
		if r := d["r"]; r != nil {
			if slice, ok := r.([]any); ok {
				for _, entry := range slice {
					e := entry.(map[string]any)
					removed[e["k"]] = core.InterfaceToValue(e["v"])
				}
			} else if slice, ok := r.([]map[string]any); ok {
				for _, e := range slice {
					removed[e["k"]] = core.InterfaceToValue(e["v"])
				}
			}
		}
		modified := make(map[any]diffPatch)
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
		originalKeys := make(map[any]any)
		if o := d["o"]; o != nil {
			if slice, ok := o.([]any); ok {
				for _, entry := range slice {
					e := entry.(map[string]any)
					originalKeys[e["k"]] = e["v"]
				}
			} else if slice, ok := o.([]map[string]any); ok {
				for _, e := range slice {
					originalKeys[e["k"]] = e["v"]
				}
			}
		}
		return &mapPatch{
			added:        added,
			removed:      removed,
			modified:     modified,
			originalKeys: originalKeys,
			basePatch:    base,
		}, nil
	case "slice":
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
				Kind:    OpKind(int(kind)),
				Index:   int(index),
				Val:     core.InterfaceToValue(o["v"]),
				Patch:   p,
				Key:     o["y"],
				PrevKey: o["r"],
			})
		}
		return &slicePatch{
			ops:       ops,
			basePatch: base,
		}, nil
	case "test":
		return &testPatch{
			expected:  core.InterfaceToValue(d["e"]),
			basePatch: base,
		}, nil
	case "copy":
		return &copyPatch{
			from:      d["f"].(string),
			basePatch: base,
		}, nil
	case "move":
		return &movePatch{
			from:      d["f"].(string),
			basePatch: base,
		}, nil
	case "log":
		return &logPatch{
			message:   d["m"].(string),
			basePatch: base,
		}, nil
	case "custom":
		kind := d["k"].(string)
		muCustom.RLock()
		typ, ok := customPatchTypes[kind]
		muCustom.RUnlock()
		if !ok {
			return nil, fmt.Errorf("unknown custom patch kind: %s", kind)
		}

		// Create a new instance of the patch type.
		// We expect typ to be a pointer type (e.g. *textPatch).
		patchPtr := reflect.New(typ.Elem()).Interface()

		// Unmarshal the data into the new instance.
		vData, err := json.Marshal(d["v"])
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(vData, patchPtr); err != nil {
			return nil, err
		}

		return &customDiffPatch{
			patch:     patchPtr,
			basePatch: base,
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
