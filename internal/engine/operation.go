package engine

import (
	"encoding/json"
	"fmt"

	"github.com/brunoga/deep/v6/condition"
	icore "github.com/brunoga/deep/v6/internal/core"
)

// Operation represents a single change within a Patch.
//
// Field semantics by Kind:
//   - OpAdd:     Path = target;        New = added value.
//   - OpRemove:  Path = target;        Old = removed value (prior).
//   - OpReplace: Path = target;        Old = prior value;          New = replacement.
//   - OpMove:    Path = destination;   From = source path;         Old = displaced value at Path (optional).
//   - OpCopy:    Path = destination;   From = source path;         Old = displaced value at Path (optional).
//   - OpLog:     Path = scope;         New = log message.
//
// Old for OpMove/OpCopy was previously the source-path string; that role now
// belongs to From, freeing Old to carry the prior destination value
// (necessary for full Reverse fidelity when the destination was non-empty).
type Operation struct {
	Kind   OpKind               `json:"k"`
	Path   string               `json:"p"`
	From   string               `json:"f,omitempty"`
	Old    any                  `json:"o,omitempty"`
	New    any                  `json:"n,omitempty"`
	If     *condition.Condition `json:"if,omitempty"`
	Unless *condition.Condition `json:"un,omitempty"`
	// Strict is stamped from Patch.Strict at apply time; not serialized.
	Strict bool `json:"-"`
}

// RawValue is a value that arrived over the wire and has not been decoded yet.
// See [icore.RawValue].
type RawValue = icore.RawValue

// opWire is Operation's serialized form. Old and New travel as raw bytes:
// decoding them here would have to guess at types the operation does not know —
// it is the target field, reached at apply time, that knows them.
type opWire struct {
	Kind   OpKind               `json:"k"`
	Path   string               `json:"p"`
	From   string               `json:"f,omitempty"`
	Old    json.RawMessage      `json:"o,omitempty"`
	New    json.RawMessage      `json:"n,omitempty"`
	If     *condition.Condition `json:"if,omitempty"`
	Unless *condition.Condition `json:"un,omitempty"`
}

func encodeAny(v any) (json.RawMessage, error) {
	if v == nil {
		return nil, nil
	}
	// A value that is still encoded goes out as it came in, byte for byte.
	if raw, ok := v.(RawValue); ok {
		return raw.JSON, nil
	}
	// A family's values use the family's own wire form — a protobuf message
	// encodes as protojson, which encoding/json cannot produce for it.
	wire, err := familyWireValue(v)
	if err != nil {
		return nil, err
	}
	if raw, ok := wire.(RawValue); ok {
		return raw.JSON, nil
	}
	return json.Marshal(v)
}

// MarshalJSON writes the operation with Old and New in their encoded form —
// which, for values that arrived encoded, is the very bytes they arrived as.
func (op Operation) MarshalJSON() ([]byte, error) {
	w := opWire{Kind: op.Kind, Path: op.Path, From: op.From, If: op.If, Unless: op.Unless}
	var err error
	if w.Old, err = encodeAny(op.Old); err != nil {
		return nil, fmt.Errorf("operation at %s: encoding Old: %w", op.Path, err)
	}
	if w.New, err = encodeAny(op.New); err != nil {
		return nil, fmt.Errorf("operation at %s: encoding New: %w", op.Path, err)
	}
	return json.Marshal(w)
}

// UnmarshalJSON reads an operation, keeping Old and New encoded as [RawValue]
// rather than decoding them into whatever the decoder's untyped defaults are.
// They are decoded at apply time, against the type of the field the operation
// actually addresses.
func (op *Operation) UnmarshalJSON(data []byte) error {
	var w opWire
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	*op = Operation{Kind: w.Kind, Path: w.Path, From: w.From, If: w.If, Unless: w.Unless}
	if len(w.Old) > 0 && string(w.Old) != "null" {
		op.Old = RawValue{JSON: w.Old}
	}
	if len(w.New) > 0 && string(w.New) != "null" {
		op.New = RawValue{JSON: w.New}
	}
	return nil
}

// GobEncode carries the operation as its JSON form. Gob's own encoding of an
// `any` field needs every concrete type registered up front, cannot encode a
// nil pointer inside an interface, and does not distinguish a nil slice from
// an empty one; the JSON form has none of those constraints, and decodes at
// apply time against the target field's real type like any other wire arrival.
func (op Operation) GobEncode() ([]byte, error) { return op.MarshalJSON() }

// GobDecode is the inverse of GobEncode.
func (op *Operation) GobDecode(data []byte) error { return op.UnmarshalJSON(data) }
