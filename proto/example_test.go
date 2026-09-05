package deepproto_test

import (
	"fmt"

	deepproto "github.com/brunoga/deep/proto"
	deep "github.com/brunoga/deep/v6"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
)

// The scenario this package exists for: rows stored as serialized protobuf,
// updated with conditional patches applied where the data lives. The store
// unmarshals the blob, applies the operations whose conditions still hold, and
// marshals it back — the flow that, without Register, corrupted messages and
// reported phantom conflicts.
func Example() {
	deepproto.Register()

	// A row, as it sits in the database: proto bytes.
	original, _ := structpb.NewStruct(map[string]any{
		"title": "Kettle",
		"price": 1999.0,
	})
	blob, _ := proto.Marshal(original)

	// A client's patch, e.g. decoded from a request body. The path addresses
	// the message by its protojson field names; the condition is the client's
	// stated assumption.
	patch := deep.Patch[*structpb.Struct]{Operations: []deep.Operation{{
		Kind: deep.OpReplace,
		Path: "/fields/price/numberValue",
		Old:  1999.0,
		New:  2499.0,
	}}, Strict: true}

	// The write, inside the transaction: load, apply, store.
	row := &structpb.Struct{}
	if err := proto.Unmarshal(blob, row); err != nil {
		panic(err)
	}
	res, err := deep.ApplyWithResult(&row, patch)
	if err != nil {
		fmt.Println("rejected:", err)
		return
	}
	fmt.Println("applied:", res.AllApplied())

	blob, _ = proto.Marshal(row)

	// The next reader sees the committed change.
	next := &structpb.Struct{}
	_ = proto.Unmarshal(blob, next)
	fmt.Println("price:", next.Fields["price"].GetNumberValue())

	// A writer whose assumption no longer holds is rejected — the strict
	// check compares against what the row now says.
	stale := patch // still expects 1999
	if _, err := deep.ApplyWithResult(&next, stale); err != nil {
		fmt.Println("stale writer rejected")
	}

	// Output:
	// applied: true
	// price: 2499
	// stale writer rejected
}
