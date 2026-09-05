package deepproto_test

import (
	"encoding/json"
	"math/rand"
	"strings"
	"testing"

	deepproto "github.com/brunoga/deep/proto"
	deep "github.com/brunoga/deep/v6"
	"github.com/brunoga/deep/v6/condition"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

func init() { deepproto.Register() }

// ── the three failures that motivated this package ───────────────────────────
//
// Before registration, each of these was broken: Equal reported proto.Equal-
// identical messages unequal once one had been marshaled, Diff emitted
// operations for the runtime's internal size cache, and a reflection-made
// clone crashed the proto runtime on its next Marshal.

func TestEqualIgnoresProtoInternalState(t *testing.T) {
	a := timestamppb.New(timestamppb.Now().AsTime())
	b := proto.Clone(a).(*timestamppb.Timestamp)
	if _, err := proto.Marshal(a); err != nil { // populates a's internal state
		t.Fatal(err)
	}

	if !proto.Equal(a, b) {
		t.Fatal("precondition: messages must be proto-equal")
	}
	if !deep.Equal(a, b) {
		t.Error("deep.Equal disagrees with proto.Equal about identical messages")
	}
}

func TestDiffEmitsNoInternalFields(t *testing.T) {
	a := durationpb.New(1000)
	b := proto.Clone(a).(*durationpb.Duration)
	if _, err := proto.Marshal(a); err != nil {
		t.Fatal(err)
	}

	p := deep.MustDiff(a, b)
	if len(p.Operations) != 0 {
		t.Errorf("equal messages produced %d operations: %v", len(p.Operations), p)
	}
}

func TestCloneSurvivesTheProtoRuntime(t *testing.T) {
	a := timestamppb.New(timestamppb.Now().AsTime())
	if _, err := proto.Marshal(a); err != nil {
		t.Fatal(err)
	}

	c := deep.Clone(a)
	if c == a {
		t.Fatal("clone is the original")
	}
	// This call SIGSEGVed before registration.
	if _, err := proto.Marshal(c); err != nil {
		t.Fatalf("the proto runtime rejects the clone: %v", err)
	}
	if !proto.Equal(a, c) {
		t.Error("clone does not equal its source")
	}
}

// ── messages inside ordinary structs ─────────────────────────────────────────

// listing is an application struct the way one holds proto messages: the
// message is a field among plain ones.
type listing struct {
	SKU     string                 `json:"sku"`
	Price   int                    `json:"price"`
	Details *structpb.Struct       `json:"details"`
	Seen    *timestamppb.Timestamp `json:"seen"`
}

func mustStruct(t testing.TB, m map[string]any) *structpb.Struct {
	s, err := structpb.NewStruct(m)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestDiffApplyThroughAnApplicationStruct(t *testing.T) {
	a := listing{
		SKU: "sku-1", Price: 1999,
		Details: mustStruct(t, map[string]any{"colour": "red", "stock": 12.0}),
	}
	b := listing{
		SKU: "sku-1", Price: 1999,
		Details: mustStruct(t, map[string]any{"colour": "blue", "stock": 12.0}),
	}

	p := deep.MustDiff(a, b)
	for _, op := range p.Operations {
		t.Logf("op: %s %s", op.Kind, op.Path)
	}
	if len(p.Operations) != 1 {
		t.Fatalf("got %d operations for a one-field change, want 1", len(p.Operations))
	}

	got := deep.Clone(a)
	if err := deep.Apply(&got, p); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !deep.Equal(got, b) {
		t.Errorf("diff+apply did not reach the target:\n got: %v\nwant: %v", got.Details, b.Details)
	}
	if _, err := proto.Marshal(got.Details); err != nil {
		t.Errorf("the patched message no longer marshals: %v", err)
	}
}

func TestDiffApplySurvivesTheWire(t *testing.T) {
	a := listing{Details: mustStruct(t, map[string]any{"stock": 12.0}), Seen: timestamppb.New(timestamppb.Now().AsTime())}
	b := listing{Details: mustStruct(t, map[string]any{"stock": 11.0}), Seen: timestamppb.New(a.Seen.AsTime().Add(1e9))}

	p := deep.MustDiff(a, b)
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var wire deep.Patch[listing]
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	got := deep.Clone(a)
	if err := deep.Apply(&got, wire); err != nil {
		t.Fatalf("apply decoded patch: %v", err)
	}
	if !deep.Equal(got, b) {
		t.Errorf("wire round trip did not reach the target")
	}
}

func TestMessageValuesTravelAsProtojson(t *testing.T) {
	// A Timestamp under protojson is an RFC 3339 string, which encoding/json
	// could never produce from the struct. The family codec must be the one on
	// the wire.
	ts := timestamppb.New(timestamppb.Now().AsTime())
	p := deep.Patch[listing]{Operations: []deep.Operation{
		{Kind: deep.OpReplace, Path: "/seen", New: ts},
	}}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := ts.AsTime().UTC().Format("2006-01-02T15:04:05")
	if !json.Valid(data) || !containsString(data, want) {
		t.Errorf("timestamp did not travel as protojson RFC 3339: %s", data)
	}

	var wire deep.Patch[listing]
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var target listing
	if err := deep.Apply(&target, wire); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !proto.Equal(target.Seen, ts) {
		t.Errorf("timestamp did not decode through the family codec: %v", target.Seen)
	}
}

func containsString(data []byte, s string) bool {
	return strings.Contains(string(data), s)
}

func TestStrictChecksOnMessageFields(t *testing.T) {
	a := listing{Details: mustStruct(t, map[string]any{"stock": 12.0})}
	b := listing{Details: mustStruct(t, map[string]any{"stock": 11.0})}
	p := deep.MustDiff(a, b)
	p.Strict = true

	// Against the value it came from: passes.
	good := deep.Clone(a)
	if err := deep.Apply(&good, p); err != nil {
		t.Fatalf("strict apply against the source value: %v", err)
	}

	// Against a drifted value: rejected.
	drifted := listing{Details: mustStruct(t, map[string]any{"stock": 99.0})}
	if err := deep.Apply(&drifted, p); err == nil {
		t.Error("strict apply passed against a drifted message")
	}
}

func TestOneofSwitchDiffs(t *testing.T) {
	// structpb.Value is a oneof: switching from a string to a number must diff
	// as remove-old-case + add-new-case with protojson field names.
	a := structpb.NewStringValue("hello")
	b := structpb.NewNumberValue(4.5)

	p := deep.MustDiff(a, b)
	if len(p.Operations) != 2 {
		t.Fatalf("got %d operations for a oneof switch, want 2: %v", len(p.Operations), p)
	}

	got := deep.Clone(a)
	if err := deep.Apply(&got, p); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !proto.Equal(got, b) {
		t.Errorf("oneof switch did not land: %v", got)
	}
}

func TestWrapperAndScalarKinds(t *testing.T) {
	// wrappers exercise every scalar kind as a message field.
	type bag struct {
		I64 *wrapperspb.Int64Value
		U32 *wrapperspb.UInt32Value
		F   *wrapperspb.FloatValue
		By  *wrapperspb.BytesValue
		B   *wrapperspb.BoolValue
	}
	a := bag{
		I64: wrapperspb.Int64(1 << 40), U32: wrapperspb.UInt32(7),
		F: wrapperspb.Float(1.5), By: wrapperspb.Bytes([]byte{1, 2}), B: wrapperspb.Bool(false),
	}
	b := bag{
		I64: wrapperspb.Int64(1<<40 + 1), U32: wrapperspb.UInt32(8),
		F: wrapperspb.Float(2.5), By: wrapperspb.Bytes([]byte{3}), B: wrapperspb.Bool(true),
	}

	for _, throughWire := range []bool{false, true} {
		p := deep.MustDiff(a, b)
		if throughWire {
			data, err := json.Marshal(p)
			if err != nil {
				t.Fatal(err)
			}
			var wire deep.Patch[bag]
			if err := json.Unmarshal(data, &wire); err != nil {
				t.Fatal(err)
			}
			p = wire
		}
		got := deep.Clone(a)
		if err := deep.Apply(&got, p); err != nil {
			t.Fatalf("wire=%v: apply: %v", throughWire, err)
		}
		if !deep.Equal(got, b) {
			t.Errorf("wire=%v: did not reach target", throughWire)
		}
	}
}

// ── property: diff+apply reaches the target, for generated structpb values ───

func genValue(rng *rand.Rand, depth int) *structpb.Value {
	if depth <= 0 {
		switch rng.Intn(4) {
		case 0:
			return structpb.NewNumberValue(float64(rng.Intn(100)))
		case 1:
			return structpb.NewStringValue([]string{"a", "b", "~x/y", ""}[rng.Intn(4)])
		case 2:
			return structpb.NewBoolValue(rng.Intn(2) == 0)
		default:
			return structpb.NewNullValue()
		}
	}
	switch rng.Intn(3) {
	case 0:
		fields := map[string]*structpb.Value{}
		for i := 0; i < rng.Intn(4); i++ {
			fields[[]string{"k1", "k2", "with/slash", "x"}[rng.Intn(4)]] = genValue(rng, depth-1)
		}
		return structpb.NewStructValue(&structpb.Struct{Fields: fields})
	case 1:
		var vals []*structpb.Value
		for i := 0; i < rng.Intn(4); i++ {
			vals = append(vals, genValue(rng, depth-1))
		}
		return structpb.NewListValue(&structpb.ListValue{Values: vals})
	default:
		return genValue(rng, 0)
	}
}

func TestPropertyDiffApplyOnProtoValues(t *testing.T) {
	for seed := 0; seed < 300; seed++ {
		rng := rand.New(rand.NewSource(int64(seed)))
		a := genValue(rng, 3)
		b := genValue(rng, 3)

		p, err := deep.Diff(a, b)
		if err != nil {
			t.Fatalf("seed %d: diff: %v", seed, err)
		}

		got := deep.Clone(a)
		if err := deep.Apply(&got, p); err != nil {
			t.Fatalf("seed %d: apply: %v\nops: %v", seed, err, p)
		}
		if !proto.Equal(got, b) {
			t.Fatalf("seed %d: did not reach target\n got: %v\nwant: %v\nops: %v", seed, got, b, p)
		}

		// And through the wire.
		data, err := json.Marshal(p)
		if err != nil {
			t.Fatalf("seed %d: marshal: %v", seed, err)
		}
		var wire deep.Patch[*structpb.Value]
		if err := json.Unmarshal(data, &wire); err != nil {
			t.Fatalf("seed %d: unmarshal: %v", seed, err)
		}
		got2 := deep.Clone(a)
		if err := deep.Apply(&got2, wire); err != nil {
			t.Fatalf("seed %d: wire apply: %v\nops: %s", seed, err, data)
		}
		if !proto.Equal(got2, b) {
			t.Fatalf("seed %d: wire round trip missed the target\n got: %v\nwant: %v", seed, got2, b)
		}
	}
}

func TestPropertyEqualAgreesWithProto(t *testing.T) {
	for seed := 0; seed < 300; seed++ {
		rng := rand.New(rand.NewSource(int64(seed)))
		a := genValue(rng, 3)
		b := genValue(rng, 3)
		if deep.Equal(a, b) != proto.Equal(a, b) {
			t.Fatalf("seed %d: deep.Equal=%v proto.Equal=%v\na=%v\nb=%v",
				seed, deep.Equal(a, b), proto.Equal(a, b), a, b)
		}
	}
}

func TestConditionsInsideMessages(t *testing.T) {
	// The limitation deep/proto v1.0.0 documented, now removed: a condition
	// path may cross into a message, resolved by protoreflect with protojson
	// names rather than by walking the Go struct.
	row := listing{
		SKU:     "sku-1",
		Price:   1999,
		Details: mustStruct(t, map[string]any{"stock": 12.0, "colour": "red"}),
	}

	stockIs := func(want float64) *condition.Condition {
		return &condition.Condition{
			Op: condition.Eq, Path: "/Details/fields/stock/numberValue", Value: want,
		}
	}

	// Condition holds → the plain field updates.
	p := deep.Patch[listing]{Operations: []deep.Operation{{
		Kind: deep.OpReplace, Path: "/price", New: 1499,
		If: stockIs(12),
	}}}
	res, err := deep.ApplyWithResult(&row, p)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !res.AllApplied() || row.Price != 1499 {
		t.Fatalf("condition into the message did not hold: %s", res)
	}

	// Condition no longer holds → skipped, and the result says so.
	p.Operations[0].If = stockIs(99)
	p.Operations[0].New = 999
	res, err = deep.ApplyWithResult(&row, p)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if _, skipped, _ := res.Counts(); skipped != 1 || row.Price != 1499 {
		t.Fatalf("stale condition should skip: %s price=%d", res, row.Price)
	}

	// A guard works too, and exists is false for an unset field rather than
	// an error.
	g := deep.Patch[listing]{
		Guard: &condition.Condition{Op: condition.Exists, Path: "/Details/fields/discount"},
		Operations: []deep.Operation{{
			Kind: deep.OpReplace, Path: "/price", New: 1,
		}},
	}
	if _, err := deep.ApplyWithResult(&row, g); err == nil {
		t.Error("guard on an unset message field should reject the patch")
	}
	if row.Price != 1499 {
		t.Errorf("guarded patch modified the row: price=%d", row.Price)
	}

	// The condition survives a JSON round trip like everything else.
	p.Operations[0].If = stockIs(12)
	p.Operations[0].New = 1299
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var wire deep.Patch[listing]
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
	if _, err := deep.ApplyWithResult(&row, wire); err != nil {
		t.Fatalf("wire apply: %v", err)
	}
	if row.Price != 1299 {
		t.Errorf("decoded conditional patch did not land: price=%d", row.Price)
	}
}

func TestConditionOnListAndMapPaths(t *testing.T) {
	v := structpb.NewListValue(&structpb.ListValue{Values: []*structpb.Value{
		structpb.NewStringValue("first"),
		structpb.NewNumberValue(7),
	}})
	type holder struct {
		V   *structpb.Value `json:"v"`
		Tag string          `json:"tag"`
	}
	h := holder{V: v}

	p := deep.Patch[holder]{Operations: []deep.Operation{{
		Kind: deep.OpReplace, Path: "/tag", New: "seen",
		If: &condition.Condition{
			Op: condition.Eq, Path: "/V/listValue/values/1/numberValue", Value: 7.0,
		},
	}}}
	res, err := deep.ApplyWithResult(&h, p)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !res.AllApplied() || h.Tag != "seen" {
		t.Fatalf("condition through list index did not hold: %s", res)
	}
}
