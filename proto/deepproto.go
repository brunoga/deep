// Package deepproto makes deep operate correctly on protocol buffer messages.
//
// Pointing the generic machinery at protoc-generated structs is not merely
// suboptimal, it is wrong: a message's Go struct carries the proto runtime's
// internal state, so field-by-field equality reports two equal messages
// unequal the moment one has been marshaled, a diff emits operations for the
// runtime's bookkeeping, and a reflection-made clone crashes the runtime on
// its next Marshal.
//
// Register installs a [deep.TypeFamily] claiming every proto.Message type.
// Inside that boundary the proto runtime's own machinery is used — proto.Equal,
// proto.Clone, protoreflect for diffing and applying, protojson on the wire —
// and outside it nothing changes: messages sit in ordinary structs, patches
// carry ordinary operations, and paths address message fields by their
// protojson names.
//
//	func main() {
//		deepproto.Register()
//		...
//	}
package deepproto

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"sync"

	deep "github.com/brunoga/deep/v6"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

var protoMessageType = reflect.TypeOf((*proto.Message)(nil)).Elem()

var registerOnce sync.Once

// Register installs the protobuf type family. Call it once, during
// initialisation, before the first deep operation touches a message. Calling
// it again is a no-op.
func Register() {
	registerOnce.Do(func() {
		deep.RegisterTypeFamily(deep.TypeFamily{
			Name: "protobuf",
			// Generated messages implement proto.Message on their pointer
			// type, which is also the only form they are held in — the
			// generated struct embeds a no-copy marker precisely so values do
			// not travel by value.
			Match: func(t reflect.Type) bool {
				return t.Kind() == reflect.Pointer && t.Implements(protoMessageType)
			},
			Equal: func(a, b any) bool {
				return proto.Equal(a.(proto.Message), b.(proto.Message))
			},
			Clone: func(v any) any {
				return proto.Clone(v.(proto.Message))
			},
			Diff:    diffMessages,
			Apply:   applyToMessage,
			Resolve: resolveInMessage,
			Marshal: func(v any) ([]byte, error) {
				data, err := protojson.Marshal(v.(proto.Message))
				if err != nil {
					return nil, err
				}
				// protojson output is deliberately unstable — it varies its
				// whitespace so nobody depends on the exact bytes. A patch is
				// exactly the place bytes should be reproducible, and
				// compacting removes the only instability.
				var buf bytes.Buffer
				if err := json.Compact(&buf, data); err != nil {
					return nil, err
				}
				return buf.Bytes(), nil
			},
			Unmarshal: func(data []byte, t reflect.Type) (any, error) {
				if t.Kind() != reflect.Pointer {
					return nil, fmt.Errorf("deepproto: cannot decode into non-pointer %v", t)
				}
				msg, ok := reflect.New(t.Elem()).Interface().(proto.Message)
				if !ok {
					return nil, fmt.Errorf("deepproto: %v is not a proto.Message", t)
				}
				if err := protojson.Unmarshal(data, msg); err != nil {
					return nil, err
				}
				return msg, nil
			},
		})
	})
}
