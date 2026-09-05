package deepproto

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	deep "github.com/brunoga/deep/v6"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// applyToMessage applies one operation, path relative to the message, to
// target. It is the family Apply handler.
func applyToMessage(target any, op deep.Operation) error {
	msg := target.(proto.Message)
	segs := splitPath(op.Path)

	if len(segs) == 0 {
		// The whole message.
		if op.Kind != deep.OpReplace && op.Kind != deep.OpAdd {
			return fmt.Errorf("deepproto: unsupported whole-message operation %s", op.Kind)
		}
		if op.Strict && op.Old != nil {
			old, err := coerceMessage(op.Old, msg)
			if err != nil {
				return fmt.Errorf("deepproto: strict check at /: %w", err)
			}
			if !proto.Equal(msg, old) {
				return fmt.Errorf("deepproto: strict check failed at /: message has changed")
			}
		}
		next, err := coerceMessage(op.New, msg)
		if err != nil {
			return err
		}
		proto.Reset(msg)
		proto.Merge(msg, next)
		return nil
	}

	return applySegments(msg.ProtoReflect(), segs, op)
}

// applySegments walks to the operation's target and performs it.
func applySegments(m protoreflect.Message, segs []string, op deep.Operation) error {
	fd := fieldByName(m.Descriptor(), segs[0])
	if fd == nil {
		return fmt.Errorf("deepproto: %s has no field %q", m.Descriptor().FullName(), segs[0])
	}
	rest := segs[1:]

	switch {
	case fd.IsMap():
		return applyIntoMap(m, fd, rest, op)
	case fd.IsList():
		return applyIntoList(m, fd, rest, op)
	case fd.Message() != nil && len(rest) > 0:
		if !m.Has(fd) && op.Kind == deep.OpRemove {
			return nil // removing inside something absent is already done
		}
		return applySegments(m.Mutable(fd).Message(), rest, op)
	case len(rest) > 0:
		return fmt.Errorf("deepproto: path continues past scalar field %q", fd.JSONName())
	default:
		return applyToField(m, fd, op)
	}
}

// applyToField performs the operation on a leaf field of m.
func applyToField(m protoreflect.Message, fd protoreflect.FieldDescriptor, op deep.Operation) error {
	if op.Strict && op.Old != nil {
		current := protoreflect.Value{}
		if m.Has(fd) {
			current = m.Get(fd)
		}
		want, err := coerceValue(m, fd, op.Old)
		if err != nil {
			return fmt.Errorf("deepproto: strict check at %q: %w", fd.JSONName(), err)
		}
		if !current.IsValid() || !scalarEqual(fd, current, want) {
			return fmt.Errorf("deepproto: strict check failed at %q", fd.JSONName())
		}
	}

	switch op.Kind {
	case deep.OpRemove:
		m.Clear(fd)
		return nil
	case deep.OpAdd, deep.OpReplace:
		v, err := coerceValue(m, fd, op.New)
		if err != nil {
			return fmt.Errorf("deepproto: setting %q: %w", fd.JSONName(), err)
		}
		m.Set(fd, v)
		return nil
	default:
		return fmt.Errorf("deepproto: unsupported operation %s at %q", op.Kind, fd.JSONName())
	}
}

func applyIntoMap(m protoreflect.Message, fd protoreflect.FieldDescriptor, rest []string, op deep.Operation) error {
	if len(rest) == 0 {
		return applyToField(m, fd, op) // the whole map
	}
	mk, err := mapKey(fd.MapKey(), rest[0])
	if err != nil {
		return err
	}
	mp := m.Mutable(fd).Map()
	vd := fd.MapValue()

	if len(rest) == 1 {
		if op.Strict && op.Old != nil {
			if !mp.Has(mk) {
				return fmt.Errorf("deepproto: strict check failed at map key %q: absent", rest[0])
			}
			want, err := coerceElem(mp.NewValue, vd, op.Old)
			if err != nil {
				return fmt.Errorf("deepproto: strict check at map key %q: %w", rest[0], err)
			}
			if !scalarEqual(vd, mp.Get(mk), want) {
				return fmt.Errorf("deepproto: strict check failed at map key %q", rest[0])
			}
		}
		switch op.Kind {
		case deep.OpRemove:
			mp.Clear(mk)
			return nil
		case deep.OpAdd, deep.OpReplace:
			v, err := coerceElem(mp.NewValue, vd, op.New)
			if err != nil {
				return err
			}
			mp.Set(mk, v)
			return nil
		default:
			return fmt.Errorf("deepproto: unsupported map operation %s", op.Kind)
		}
	}

	if vd.Message() == nil {
		return fmt.Errorf("deepproto: path continues past scalar map value at %q", rest[0])
	}
	return applySegments(mp.Mutable(mk).Message(), rest[1:], op)
}

func applyIntoList(m protoreflect.Message, fd protoreflect.FieldDescriptor, rest []string, op deep.Operation) error {
	if len(rest) == 0 {
		return applyToField(m, fd, op) // the whole list
	}
	idx, err := strconv.Atoi(rest[0])
	if err != nil {
		return fmt.Errorf("deepproto: %q is not a list index", rest[0])
	}
	l := m.Mutable(fd).List()
	if idx < 0 || idx >= l.Len() {
		return fmt.Errorf("deepproto: index %d out of range (list has %d)", idx, l.Len())
	}

	if len(rest) == 1 {
		if op.Strict && op.Old != nil {
			want, err := coerceElem(l.NewElement, fd, op.Old)
			if err != nil {
				return fmt.Errorf("deepproto: strict check at index %d: %w", idx, err)
			}
			if !scalarEqual(fd, l.Get(idx), want) {
				return fmt.Errorf("deepproto: strict check failed at index %d", idx)
			}
		}
		if op.Kind != deep.OpReplace && op.Kind != deep.OpAdd {
			return fmt.Errorf("deepproto: unsupported list operation %s", op.Kind)
		}
		v, err := coerceElem(l.NewElement, fd, op.New)
		if err != nil {
			return err
		}
		l.Set(idx, v)
		return nil
	}

	if fd.Message() == nil {
		return fmt.Errorf("deepproto: path continues past scalar list element")
	}
	return applySegments(l.Get(idx).Message(), rest[1:], op)
}

// ── value coercion ───────────────────────────────────────────────────────────

// coerceValue turns an operation's payload into a protoreflect value for a
// field of m. The payload may be typed (an in-process patch), a RawValue (a
// patch from the wire), or a decoded generic shape.
func coerceValue(m protoreflect.Message, fd protoreflect.FieldDescriptor, v any) (protoreflect.Value, error) {
	switch {
	case fd.IsMap(), fd.IsList():
		return coerceContainer(m, fd, v)
	default:
		return coerceElem(func() protoreflect.Value { return m.NewField(fd) }, fd, v)
	}
}

// coerceElem coerces a scalar or message payload, using fresh to allocate a
// new message value when one is needed.
func coerceElem(fresh func() protoreflect.Value, fd protoreflect.FieldDescriptor, v any) (protoreflect.Value, error) {
	if fd.Message() != nil {
		target := fresh()
		msg := target.Message().Interface()
		switch p := v.(type) {
		case proto.Message:
			return protoreflect.ValueOfMessage(p.ProtoReflect()), nil
		case deep.RawValue:
			if err := protojson.Unmarshal(p.JSON, msg); err != nil {
				return protoreflect.Value{}, err
			}
			return target, nil
		default:
			// A generic shape from some other decode: re-encode and let
			// protojson read it.
			data, err := json.Marshal(v)
			if err != nil {
				return protoreflect.Value{}, err
			}
			if err := protojson.Unmarshal(data, msg); err != nil {
				return protoreflect.Value{}, err
			}
			return target, nil
		}
	}
	return scalarValue(fd, v)
}

// scalarValue converts v to the exact representation fd requires.
func scalarValue(fd protoreflect.FieldDescriptor, v any) (protoreflect.Value, error) {
	if raw, ok := v.(deep.RawValue); ok {
		var decoded any
		if err := json.Unmarshal(raw.JSON, &decoded); err != nil {
			return protoreflect.Value{}, err
		}
		v = decoded
	}

	k := fd.Kind()
	switch k {
	case protoreflect.BoolKind:
		if b, ok := deep.ValueAs[bool](v); ok {
			return protoreflect.ValueOfBool(b), nil
		}
	case protoreflect.StringKind:
		if s, ok := deep.ValueAs[string](v); ok {
			return protoreflect.ValueOfString(s), nil
		}
	case protoreflect.BytesKind:
		if b, ok := v.([]byte); ok {
			return protoreflect.ValueOfBytes(b), nil
		}
		if s, ok := v.(string); ok { // JSON carries bytes as base64
			b, err := base64.StdEncoding.DecodeString(s)
			if err != nil {
				return protoreflect.Value{}, err
			}
			return protoreflect.ValueOfBytes(b), nil
		}
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind:
		if n, ok := deep.ValueAs[int32](v); ok {
			return protoreflect.ValueOfInt32(n), nil
		}
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		if n, ok := deep.ValueAs[int64](v); ok {
			return protoreflect.ValueOfInt64(n), nil
		}
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		if n, ok := deep.ValueAs[uint32](v); ok {
			return protoreflect.ValueOfUint32(n), nil
		}
	case protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		if n, ok := deep.ValueAs[uint64](v); ok {
			return protoreflect.ValueOfUint64(n), nil
		}
	case protoreflect.FloatKind:
		if n, ok := deep.ValueAs[float32](v); ok {
			return protoreflect.ValueOfFloat32(n), nil
		}
	case protoreflect.DoubleKind:
		if n, ok := deep.ValueAs[float64](v); ok {
			return protoreflect.ValueOfFloat64(n), nil
		}
	case protoreflect.EnumKind:
		if n, ok := deep.ValueAs[int32](v); ok {
			return protoreflect.ValueOfEnum(protoreflect.EnumNumber(n)), nil
		}
		if s, ok := v.(string); ok { // protojson writes enum value names
			if ev := fd.Enum().Values().ByName(protoreflect.Name(s)); ev != nil {
				return protoreflect.ValueOfEnum(ev.Number()), nil
			}
		}
	}
	return protoreflect.Value{}, fmt.Errorf("cannot use %T as %s", v, k)
}

// coerceContainer handles a whole-map or whole-list payload.
func coerceContainer(m protoreflect.Message, fd protoreflect.FieldDescriptor, v any) (protoreflect.Value, error) {
	// A typed in-process payload builds the container element by element,
	// which handles proto.Message elements a blind json.Marshal would render
	// in the wrong shape.
	if items, ok := v.([]any); ok && fd.IsList() {
		target := m.NewField(fd)
		l := target.List()
		for _, it := range items {
			ev, err := coerceElem(l.NewElement, fd, it)
			if err != nil {
				return protoreflect.Value{}, err
			}
			l.Append(ev)
		}
		return target, nil
	}
	if items, ok := v.(map[string]any); ok && fd.IsMap() {
		target := m.NewField(fd)
		mp := target.Map()
		vd := fd.MapValue()
		for k, it := range items {
			mk, err := mapKey(fd.MapKey(), k)
			if err != nil {
				return protoreflect.Value{}, err
			}
			ev, err := coerceElem(mp.NewValue, vd, it)
			if err != nil {
				return protoreflect.Value{}, err
			}
			mp.Set(mk, ev)
		}
		return target, nil
	}

	// Encoded payloads — from the wire, or built canonically by this
	// package's differ — decode element by element, each element handed to the
	// same coercion a single-element operation gets. Decoding whole containers
	// through protojson documents does not work: a well-known type's protojson
	// form ignores field names entirely (a ListValue is a bare array).
	var data []byte
	switch p := v.(type) {
	case deep.RawValue:
		data = p.JSON
	default:
		var err error
		if data, err = json.Marshal(v); err != nil {
			return protoreflect.Value{}, err
		}
	}

	target := m.NewField(fd)
	if fd.IsList() {
		var parts []json.RawMessage
		if err := json.Unmarshal(data, &parts); err != nil {
			return protoreflect.Value{}, err
		}
		l := target.List()
		for _, part := range parts {
			ev, err := coerceElem(l.NewElement, fd, deep.RawValue{JSON: part})
			if err != nil {
				return protoreflect.Value{}, err
			}
			l.Append(ev)
		}
		return target, nil
	}
	var parts map[string]json.RawMessage
	if err := json.Unmarshal(data, &parts); err != nil {
		return protoreflect.Value{}, err
	}
	mp := target.Map()
	vd := fd.MapValue()
	for k, part := range parts {
		mk, err := mapKey(fd.MapKey(), k)
		if err != nil {
			return protoreflect.Value{}, err
		}
		ev, err := coerceElem(mp.NewValue, vd, deep.RawValue{JSON: part})
		if err != nil {
			return protoreflect.Value{}, err
		}
		mp.Set(mk, ev)
	}
	return target, nil
}

func coerceMessage(v any, like proto.Message) (proto.Message, error) {
	switch p := v.(type) {
	case proto.Message:
		return p, nil
	case deep.RawValue:
		out := like.ProtoReflect().New().Interface()
		if err := protojson.Unmarshal(p.JSON, out); err != nil {
			return nil, err
		}
		return out, nil
	default:
		data, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		out := like.ProtoReflect().New().Interface()
		if err := protojson.Unmarshal(data, out); err != nil {
			return nil, err
		}
		return out, nil
	}
}

// ── path handling ────────────────────────────────────────────────────────────

// fieldByName resolves a path segment against a message: protojson name first,
// which is what this package's differ writes, then the schema name.
func fieldByName(d protoreflect.MessageDescriptor, name string) protoreflect.FieldDescriptor {
	fields := d.Fields()
	if fd := fields.ByJSONName(name); fd != nil {
		return fd
	}
	if fd := fields.ByTextName(name); fd != nil {
		return fd
	}
	return fields.ByName(protoreflect.Name(name))
}

func mapKey(kd protoreflect.FieldDescriptor, seg string) (protoreflect.MapKey, error) {
	switch kd.Kind() {
	case protoreflect.StringKind:
		return protoreflect.ValueOfString(seg).MapKey(), nil
	case protoreflect.BoolKind:
		b, err := strconv.ParseBool(seg)
		if err != nil {
			return protoreflect.MapKey{}, err
		}
		return protoreflect.ValueOfBool(b).MapKey(), nil
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind:
		n, err := strconv.ParseInt(seg, 10, 32)
		if err != nil {
			return protoreflect.MapKey{}, err
		}
		return protoreflect.ValueOfInt32(int32(n)).MapKey(), nil
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		n, err := strconv.ParseInt(seg, 10, 64)
		if err != nil {
			return protoreflect.MapKey{}, err
		}
		return protoreflect.ValueOfInt64(n).MapKey(), nil
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		n, err := strconv.ParseUint(seg, 10, 32)
		if err != nil {
			return protoreflect.MapKey{}, err
		}
		return protoreflect.ValueOfUint32(uint32(n)).MapKey(), nil
	case protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		n, err := strconv.ParseUint(seg, 10, 64)
		if err != nil {
			return protoreflect.MapKey{}, err
		}
		return protoreflect.ValueOfUint64(n).MapKey(), nil
	}
	return protoreflect.MapKey{}, fmt.Errorf("deepproto: unsupported map key kind %s", kd.Kind())
}

// splitPath breaks a JSON Pointer into unescaped segments.
func splitPath(path string) []string {
	if path == "" || path == "/" {
		return nil
	}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for i, p := range parts {
		parts[i] = deep.UnescapePathKey(p)
	}
	return parts
}
