package core

import (
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/brunoga/deep/v6/internal/unsafe"
)

func ConvertValue(v reflect.Value, targetType reflect.Type) reflect.Value {
	if !v.IsValid() {
		return reflect.Zero(targetType)
	}

	if v.Type() == targetType {
		return v
	}

	// A value still in its encoded form decodes straight into the target type,
	// which is the point of keeping it encoded: the decoder produces an int
	// for an int field and a struct for a struct field, instead of this
	// function guessing its way back from float64 and map[string]any.
	if v.Type() == rawValueType {
		raw := v.Interface().(RawValue)
		// A family's values may use a wire form encoding/json cannot read — a
		// protobuf Timestamp is an RFC 3339 string under protojson — so the
		// family decodes its own.
		if FamiliesRegistered() {
			if fam, ok := FamilyFor(targetType); ok && fam.Unmarshal != nil {
				out, err := fam.Unmarshal(raw.JSON, targetType)
				if err != nil {
					return reflect.Value{}
				}
				rv := reflect.ValueOf(out)
				if rv.IsValid() && rv.Type().AssignableTo(targetType) {
					return rv
				}
				return reflect.Value{}
			}
		}
		if decoded, err := raw.Decode(targetType); err == nil {
			return decoded
		}
		return reflect.Value{}
	}

	if v.Type().AssignableTo(targetType) {
		return v
	}

	// Before the general conversion: Go considers a string convertible to
	// []byte, and would reinterpret the text as its own bytes. JSON has no
	// byte-slice type and writes []byte as a base64 string, so a decoded
	// operation carries exactly that string where the field wants bytes.
	// Decoding it the way it was encoded recovers the value.
	if v.Kind() == reflect.String && targetType.Kind() == reflect.Slice &&
		targetType.Elem().Kind() == reflect.Uint8 {
		if data, err := json.Marshal(v.String()); err == nil {
			decoded := reflect.New(targetType)
			if err := json.Unmarshal(data, decoded.Interface()); err == nil {
				return decoded.Elem()
			}
		}
	}

	if v.Type().ConvertibleTo(targetType) {
		return v.Convert(targetType)
	}

	// Handle pointer wrapping
	if targetType.Kind() == reflect.Pointer && v.Type().AssignableTo(targetType.Elem()) {
		ptr := reflect.New(targetType.Elem())
		ptr.Elem().Set(v)
		return ptr
	}

	// Handle JSON/Gob numbers
	if v.Kind() == reflect.Float64 {
		switch targetType.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			return reflect.ValueOf(int64(v.Float())).Convert(targetType)
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			return reflect.ValueOf(uint64(v.Float())).Convert(targetType)
		case reflect.Float32:
			return reflect.ValueOf(float32(v.Float())).Convert(targetType)
		}
	}

	// Values that have been through JSON arrive as the generic shapes the
	// decoder produces — map[string]any for an object, []any for an array — and
	// none of the conversions above apply to them. Re-encoding and decoding
	// into the target type recovers the original value. This is what lets an
	// operation survive a round-trip through the wire: Operation.Old and
	// Operation.New are `any`, so a patch that was serialized and read back
	// carries decoded shapes rather than the types it was built from.
	if isJSONShape(v.Kind()) && isJSONDecodable(targetType.Kind()) {
		if data, err := json.Marshal(v.Interface()); err == nil {
			decoded := reflect.New(targetType)
			if err := json.Unmarshal(data, decoded.Interface()); err == nil {
				return decoded.Elem()
			}
		}
	}

	return v
}

// isJSONShape reports whether k is one of the kinds a JSON decoder produces for
// a composite value.
func isJSONShape(k reflect.Kind) bool {
	return k == reflect.Map || k == reflect.Slice || k == reflect.Array || k == reflect.Interface
}

// isJSONDecodable reports whether a composite value can be rebuilt into k by
// decoding JSON into it.
func isJSONDecodable(k reflect.Kind) bool {
	// Pointer is included so that an `add` of a pointer field survives the
	// wire: a *Meta arrives as map[string]any, and json.Unmarshal into a
	// **Meta allocates the pointee the same way it would for a value.
	return k == reflect.Struct || k == reflect.Slice || k == reflect.Array ||
		k == reflect.Map || k == reflect.Pointer
}

// ConvertValueChecked is ConvertValue with a verdict: it reports an error
// instead of handing back a value the caller would panic on when setting.
// Patch values routinely come from untrusted input (a JSON document from a
// peer), so a type mismatch has to be an error, never a panic.
func ConvertValueChecked(v reflect.Value, targetType reflect.Type) (reflect.Value, error) {
	converted := ConvertValue(v, targetType)
	if !converted.IsValid() {
		return reflect.Zero(targetType), nil
	}
	if !converted.Type().AssignableTo(targetType) {
		return reflect.Value{}, fmt.Errorf("cannot assign %s to %s", converted.Type(), targetType)
	}
	return converted, nil
}

func SetValue(v, newVal reflect.Value) {
	if !newVal.IsValid() {
		if v.CanSet() {
			v.Set(reflect.Zero(v.Type()))
		}
		return
	}

	// Navigate through pointers if needed
	target := v
	for target.Kind() == reflect.Pointer && target.Type() != newVal.Type() {
		if target.IsNil() {
			target.Set(reflect.New(target.Type().Elem()))
		}
		target = target.Elem()
	}

	converted := ConvertValue(newVal, target.Type())
	target.Set(converted)
}

func ValueToInterface(v reflect.Value) any {
	if !v.IsValid() {
		return nil
	}
	if !v.CanInterface() {
		unsafe.DisableRO(&v)
	}
	return v.Interface()
}

func ExtractKey(v reflect.Value, fieldIdx int) any {
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return nil
	}
	return v.Field(fieldIdx).Interface()
}

// RawValue holds a value that arrived over the wire and has not been decoded
// yet, because the right type to decode it into is not known until the
// operation carrying it reaches its target field.
//
// This is what removes a whole class of coercion bugs. Decoding an untyped
// value produces whatever the decoder's defaults are — every JSON number a
// float64, every object a map[string]any, every []byte a base64 string — and
// the library then had to guess its way back to the field's real type,
// verified conversion by verified conversion, each one a place to be wrong.
// Decoding into the field's actual type instead makes the decoder itself do
// the right thing by construction.
type RawValue struct {
	JSON []byte
}

// MarshalJSON emits the still-encoded bytes as they are, so a re-serialized
// operation is byte-identical to the one that arrived.
func (r RawValue) MarshalJSON() ([]byte, error) {
	if len(r.JSON) == 0 {
		return []byte("null"), nil
	}
	return r.JSON, nil
}

// UnmarshalJSON captures the encoded bytes without interpreting them.
func (r *RawValue) UnmarshalJSON(data []byte) error {
	r.JSON = append(r.JSON[:0], data...)
	return nil
}

// Decode unmarshals the value into t, returning the decoded value.
func (r RawValue) Decode(t reflect.Type) (reflect.Value, error) {
	out := reflect.New(t)
	if err := json.Unmarshal(r.JSON, out.Interface()); err != nil {
		return reflect.Value{}, err
	}
	return out.Elem(), nil
}

var rawValueType = reflect.TypeOf(RawValue{})
