package deepproto_test

import (
	"testing"

	"github.com/brunoga/deep/proto/internal/testpb"
	deep "github.com/brunoga/deep/v6"
	"github.com/brunoga/deep/v6/condition"
	"google.golang.org/protobuf/proto"
)

// The applier and resolver take paths and payloads from the wire — from
// whoever sent the patch — and navigate protoreflect with them. Like the
// binary decoders, they are fuzzed as the network-facing code they are: any
// input may be rejected, none may panic.

func fuzzTarget() *testpb.Catalog {
	return &testpb.Catalog{
		Name:  "shop",
		Items: []*testpb.Item{{Id: "a", Qty: 1}, {Id: "b", Qty: 2}},
		Stock: map[string]int32{"a": 5},
		Tags:  []string{"x", "y"},
	}
}

func FuzzApplyToMessage(f *testing.F) {
	f.Add("/name", `"new"`, "replace")
	f.Add("/items/a/qty", `7`, "replace")
	f.Add("/stock/zz", `9`, "add")
	f.Add("/tags/0", `"t"`, "replace")
	f.Add("/", `{"name":"whole"}`, "replace")
	f.Add("/items/0/../..", `null`, "remove")
	f.Fuzz(func(t *testing.T, path, payload, kind string) {
		target := fuzzTarget()
		op := deep.Operation{
			Kind: deep.OpKind(kind),
			Path: path,
			New:  deep.RawValue{JSON: []byte(payload)},
		}
		p := deep.Patch[*testpb.Catalog]{Operations: []deep.Operation{op}}
		_ = deep.Apply(&target, p) // errors fine; panics not
		// Whatever happened, the message must still be usable.
		if _, err := proto.Marshal(target); err != nil {
			t.Fatalf("message corrupted by %s %q: %v", kind, path, err)
		}
	})
}

func FuzzResolveInMessage(f *testing.F) {
	f.Add("/items/a/qty")
	f.Add("/stock/a")
	f.Add("/tags/1")
	f.Add("/name")
	f.Add("//~1~0//")
	f.Fuzz(func(t *testing.T, path string) {
		target := fuzzTarget()
		cond := &condition.Condition{Op: condition.Exists, Path: "/cat" + path}
		type holder struct {
			Cat *testpb.Catalog `json:"cat"`
			X   int             `json:"x"`
		}
		h := holder{Cat: target}
		p := deep.Patch[holder]{Operations: []deep.Operation{{
			Kind: deep.OpReplace, Path: "/x", New: 1, If: cond,
		}}}
		_, _ = deep.ApplyWithResult(&h, p) // must not panic
	})
}

func TestInvalidUTF8IsRefusedNotWritten(t *testing.T) {
	// The corruption the fuzzer found: an operation carrying invalid UTF-8
	// into a string map key or value used to succeed and leave a message
	// proto.Marshal refuses.
	target := fuzzTarget()
	p := deep.Patch[*testpb.Catalog]{Operations: []deep.Operation{
		{Kind: deep.OpAdd, Path: "/stock/\x81", New: deep.RawValue{JSON: []byte("9")}},
	}}
	if err := deep.Apply(&target, p); err == nil {
		t.Error("an invalid-UTF-8 map key should be refused")
	}
	if _, err := proto.Marshal(target); err != nil {
		t.Fatalf("message corrupted anyway: %v", err)
	}

	p = deep.Patch[*testpb.Catalog]{Operations: []deep.Operation{
		{Kind: deep.OpReplace, Path: "/name", New: "\x81"},
	}}
	if err := deep.Apply(&target, p); err == nil {
		t.Error("an invalid-UTF-8 string value should be refused")
	}
	if _, err := proto.Marshal(target); err != nil {
		t.Fatalf("message corrupted anyway: %v", err)
	}
}
