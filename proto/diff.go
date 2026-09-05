package deepproto

import (
	"bytes"
	"encoding/json"
	"fmt"

	deep "github.com/brunoga/deep/v6"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// diffMessages returns the operations turning message a into message b, with
// paths relative to the message and segments named the way protojson names
// them. It is the family Diff handler.
func diffMessages(a, b any) ([]deep.Operation, error) {
	ma, mb := a.(proto.Message), b.(proto.Message)

	// A typed-nil message is an empty message of its type for diffing
	// purposes; protoreflect gives us exactly that view.
	ra, rb := ma.ProtoReflect(), mb.ProtoReflect()
	if ra.Descriptor().FullName() != rb.Descriptor().FullName() {
		return nil, fmt.Errorf("deepproto: cannot diff %s against %s",
			ra.Descriptor().FullName(), rb.Descriptor().FullName())
	}

	// Unknown fields are bytes this build's schema cannot name a path into.
	// If they differ, per-field operations cannot describe the change; the
	// whole message travels instead.
	if !bytes.Equal(ra.GetUnknown(), rb.GetUnknown()) {
		return []deep.Operation{{Kind: deep.OpReplace, Path: "/", Old: ma, New: mb}}, nil
	}

	var ops []deep.Operation
	if err := diffFields("", ra, rb, &ops); err != nil {
		return nil, err
	}
	return ops, nil
}

// diffFields walks the union of populated fields on both sides.
func diffFields(prefix string, a, b protoreflect.Message, ops *[]deep.Operation) error {
	fields := a.Descriptor().Fields()
	for i := 0; i < fields.Len(); i++ {
		fd := fields.Get(i)
		path := prefix + "/" + deep.EscapePathKey(fd.JSONName())
		inA, inB := a.Has(fd), b.Has(fd)

		switch {
		case !inA && !inB:
			// A oneof's unset cases, defaults — nothing on either side.
		case inA && !inB:
			*ops = append(*ops, deep.Operation{Kind: deep.OpRemove, Path: path, Old: goValue(fd, a.Get(fd))})
		case !inA && inB:
			*ops = append(*ops, deep.Operation{Kind: deep.OpAdd, Path: path, New: goValue(fd, b.Get(fd))})
		default:
			if err := diffField(path, fd, a.Get(fd), b.Get(fd), ops); err != nil {
				return err
			}
		}
	}
	return nil
}

func diffField(path string, fd protoreflect.FieldDescriptor, va, vb protoreflect.Value, ops *[]deep.Operation) error {
	switch {
	case fd.IsMap():
		return diffMap(path, fd, va.Map(), vb.Map(), ops)
	case fd.IsList():
		return diffList(path, fd, va.List(), vb.List(), ops)
	case fd.Message() != nil:
		if !bytes.Equal(va.Message().GetUnknown(), vb.Message().GetUnknown()) {
			*ops = append(*ops, deep.Operation{Kind: deep.OpReplace, Path: path,
				Old: goValue(fd, va), New: goValue(fd, vb)})
			return nil
		}
		return diffFields(path, va.Message(), vb.Message(), ops)
	default:
		if !scalarEqual(fd, va, vb) {
			*ops = append(*ops, deep.Operation{Kind: deep.OpReplace, Path: path,
				Old: goValue(fd, va), New: goValue(fd, vb)})
		}
		return nil
	}
}

func diffList(path string, fd protoreflect.FieldDescriptor, la, lb protoreflect.List, ops *[]deep.Operation) error {
	// A registered key makes the list order-insensitive and element-addressed.
	if kd, ok := listKeyFor(fd); ok {
		return diffKeyedList(path, fd, kd, la, lb, ops)
	}
	// Unkeyed: the same shape the generic engine gives unkeyed slices — a
	// length change replaces the whole list, equal lengths compare per index.
	if la.Len() != lb.Len() {
		*ops = append(*ops, deep.Operation{Kind: deep.OpReplace, Path: path,
			Old: listValue(fd, la), New: listValue(fd, lb)})
		return nil
	}
	for i := 0; i < la.Len(); i++ {
		ipath := fmt.Sprintf("%s/%d", path, i)
		ea, eb := la.Get(i), lb.Get(i)
		if fd.Message() != nil {
			if err := diffFields(ipath, ea.Message(), eb.Message(), ops); err != nil {
				return err
			}
			continue
		}
		if !scalarEqual(fd, ea, eb) {
			*ops = append(*ops, deep.Operation{Kind: deep.OpReplace, Path: ipath,
				Old: elemValue(fd, ea), New: elemValue(fd, eb)})
		}
	}
	return nil
}

func diffMap(path string, fd protoreflect.FieldDescriptor, ma, mb protoreflect.Map, ops *[]deep.Operation) error {
	vd := fd.MapValue()
	var err error
	ma.Range(func(k protoreflect.MapKey, va protoreflect.Value) bool {
		kpath := path + "/" + deep.EscapePathKey(k.String())
		if !mb.Has(k) {
			*ops = append(*ops, deep.Operation{Kind: deep.OpRemove, Path: kpath, Old: elemValue(vd, va)})
			return true
		}
		vb := mb.Get(k)
		if vd.Message() != nil {
			err = diffFields(kpath, va.Message(), vb.Message(), ops)
			return err == nil
		}
		if !scalarEqual(vd, va, vb) {
			*ops = append(*ops, deep.Operation{Kind: deep.OpReplace, Path: kpath,
				Old: elemValue(vd, va), New: elemValue(vd, vb)})
		}
		return true
	})
	if err != nil {
		return err
	}
	mb.Range(func(k protoreflect.MapKey, vb protoreflect.Value) bool {
		if !ma.Has(k) {
			kpath := path + "/" + deep.EscapePathKey(k.String())
			*ops = append(*ops, deep.Operation{Kind: deep.OpAdd, Path: kpath, New: elemValue(vd, vb)})
		}
		return true
	})
	return nil
}

func scalarEqual(fd protoreflect.FieldDescriptor, a, b protoreflect.Value) bool {
	if fd.Kind() == protoreflect.BytesKind {
		return bytes.Equal(a.Bytes(), b.Bytes())
	}
	if fd.Message() != nil {
		return proto.Equal(a.Message().Interface(), b.Message().Interface())
	}
	return a.Interface() == b.Interface()
}

// goValue renders a field value as the Go value an operation carries: a
// proto.Message for message fields — so the family codec puts it on the wire
// as protojson — and the plain Go scalar otherwise. Lists and maps become
// their Go container forms.
func goValue(fd protoreflect.FieldDescriptor, v protoreflect.Value) any {
	switch {
	case fd.IsMap():
		return mapValue(fd, v.Map())
	case fd.IsList():
		return listValue(fd, v.List())
	default:
		return elemValue(fd, v)
	}
}

func elemValue(fd protoreflect.FieldDescriptor, v protoreflect.Value) any {
	if fd.Message() != nil {
		return v.Message().Interface()
	}
	if fd.Kind() == protoreflect.EnumKind {
		return int32(v.Enum())
	}
	return v.Interface()
}

func listValue(fd protoreflect.FieldDescriptor, l protoreflect.List) any {
	// Message elements are rendered to protojson here, once, so every consumer
	// sees one canonical form: the wire carries these bytes as they are, and
	// an in-process apply decodes them the same way a decoded patch would.
	// encoding/json marshalling a message's Go struct produces a shape
	// protojson refuses, which is exactly the mistake this avoids.
	if fd.Message() != nil {
		parts := make([]json.RawMessage, l.Len())
		for i := range parts {
			b, err := marshalCompact(l.Get(i).Message().Interface())
			if err != nil {
				return nil
			}
			parts[i] = b
		}
		data, err := json.Marshal(parts)
		if err != nil {
			return nil
		}
		return deep.RawValue{JSON: data}
	}
	out := make([]any, l.Len())
	for i := range out {
		out[i] = elemValue(fd, l.Get(i))
	}
	return out
}

func mapValue(fd protoreflect.FieldDescriptor, m protoreflect.Map) any {
	vd := fd.MapValue()
	if vd.Message() != nil {
		parts := make(map[string]json.RawMessage, m.Len())
		var fail bool
		m.Range(func(k protoreflect.MapKey, v protoreflect.Value) bool {
			b, err := marshalCompact(v.Message().Interface())
			if err != nil {
				fail = true
				return false
			}
			parts[k.String()] = b
			return true
		})
		if fail {
			return nil
		}
		data, err := json.Marshal(parts)
		if err != nil {
			return nil
		}
		return deep.RawValue{JSON: data}
	}
	out := make(map[string]any, m.Len())
	m.Range(func(k protoreflect.MapKey, v protoreflect.Value) bool {
		out[k.String()] = elemValue(vd, v)
		return true
	})
	return out
}

// marshalCompact is protojson with its deliberate whitespace instability
// removed, the same way the family codec does it.
func marshalCompact(m proto.Message) (json.RawMessage, error) {
	data, err := protojson.Marshal(m)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, data); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
