package deepproto

import (
	"encoding/json"
	"fmt"

	"github.com/brunoga/deep/proto/wire"
	deep "github.com/brunoga/deep/v6"
	"github.com/brunoga/deep/v6/condition"
	"google.golang.org/protobuf/proto"
)

// Conversion between deep.Patch and its protobuf envelope (wire/deeppatch.proto),
// for systems that carry structured payloads as protobuf: a patch inside a
// gRPC request travels as a typed message, and a non-Go peer gets a
// compiler-checked structure to read it with.
//
// Operation values are carried as JSON bytes — the exact bytes the native
// encoding would use, protojson for message values — so a value decodes
// identically whichever envelope delivered it. On the way back in they arrive
// as deep.RawValue and decode at apply time against the target field's type,
// like any other wire arrival.

// ToProto converts a patch into its protobuf envelope.
func ToProto[T any](p deep.Patch[T]) (*wire.Patch, error) {
	out := &wire.Patch{
		Strict: p.Strict,
		Guard:  conditionToProto(p.Guard),
	}
	for _, op := range p.Operations {
		w := &wire.Operation{
			Kind:   op.Kind.String(),
			Path:   op.Path,
			From:   op.From,
			If:     conditionToProto(op.If),
			Unless: conditionToProto(op.Unless),
		}
		var err error
		if w.Old, w.HasOld, err = valueToJSON(op.Old); err != nil {
			return nil, fmt.Errorf("deepproto: operation at %s: encoding Old: %w", op.Path, err)
		}
		if w.New, w.HasNew, err = valueToJSON(op.New); err != nil {
			return nil, fmt.Errorf("deepproto: operation at %s: encoding New: %w", op.Path, err)
		}
		out.Ops = append(out.Ops, w)
	}
	return out, nil
}

// FromProto converts a protobuf envelope back into a patch. Values come back
// as [deep.RawValue] and decode at apply time, against the type of the field
// each operation addresses.
func FromProto[T any](w *wire.Patch) (deep.Patch[T], error) {
	if w == nil {
		return deep.Patch[T]{}, nil
	}
	p := deep.Patch[T]{
		Strict: w.GetStrict(),
		Guard:  conditionFromProto(w.GetGuard()),
	}
	for _, op := range w.GetOps() {
		o := deep.Operation{
			Kind:   deep.OpKind(op.GetKind()),
			Path:   op.GetPath(),
			From:   op.GetFrom(),
			If:     conditionFromProto(op.GetIf()),
			Unless: conditionFromProto(op.GetUnless()),
		}
		if op.GetHasOld() {
			o.Old = deep.RawValue{JSON: op.GetOld()}
		}
		if op.GetHasNew() {
			o.New = deep.RawValue{JSON: op.GetNew()}
		}
		p.Operations = append(p.Operations, o)
	}
	return p, nil
}

// valueToJSON encodes an operation value the way the native wire format does:
// already-encoded values pass through byte for byte, protobuf messages become
// protojson, everything else encoding/json.
func valueToJSON(v any) (data []byte, present bool, err error) {
	switch p := v.(type) {
	case nil:
		return nil, false, nil
	case deep.RawValue:
		return p.JSON, true, nil
	case proto.Message:
		data, err := marshalCompact(p)
		return data, true, err
	default:
		data, err := json.Marshal(v)
		return data, true, err
	}
}

func conditionToProto(c *condition.Condition) *wire.Condition {
	if c == nil {
		return nil
	}
	out := &wire.Condition{Op: c.Op, Path: c.Path}
	if c.Value != nil {
		data, err := json.Marshal(c.Value)
		if err == nil {
			out.Value, out.HasValue = data, true
		}
	}
	for _, sub := range c.Sub {
		out.Sub = append(out.Sub, conditionToProto(sub))
	}
	return out
}

func conditionFromProto(w *wire.Condition) *condition.Condition {
	if w == nil {
		return nil
	}
	out := &condition.Condition{Op: w.GetOp(), Path: w.GetPath()}
	if w.GetHasValue() {
		// Condition values decode eagerly: conditions compare against whatever
		// a path resolves to, with verified coercion, and have no single
		// target type to defer the decode for.
		var v any
		if err := json.Unmarshal(w.GetValue(), &v); err == nil {
			out.Value = v
		}
	}
	for _, sub := range w.GetSub() {
		out.Sub = append(out.Sub, conditionFromProto(sub))
	}
	return out
}
