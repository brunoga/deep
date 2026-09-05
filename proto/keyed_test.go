package deepproto_test

import (
	"encoding/json"
	"testing"

	deepproto "github.com/brunoga/deep/proto"
	"github.com/brunoga/deep/proto/internal/testpb"
	"github.com/brunoga/deep/proto/wire"
	deep "github.com/brunoga/deep/v6"
	"github.com/brunoga/deep/v6/condition"
	"google.golang.org/protobuf/proto"
)

func init() {
	deepproto.RegisterListKey("deep.testpb.Catalog.items", "id")
}

func catalog(items ...*testpb.Item) *testpb.Catalog {
	return &testpb.Catalog{Name: "shop", Items: items}
}

func item(id string, qty int32) *testpb.Item {
	return &testpb.Item{Id: id, Qty: qty}
}

func TestKeyedListIgnoresOrder(t *testing.T) {
	a := catalog(item("a", 1), item("b", 2), item("c", 3))
	b := catalog(item("c", 3), item("a", 1), item("b", 2))

	p := deep.MustDiff(a, b)
	if len(p.Operations) != 0 {
		t.Errorf("reordering a keyed list produced %d operations: %v", len(p.Operations), p)
	}
}

func TestKeyedListDiffsByElement(t *testing.T) {
	a := catalog(item("a", 1), item("b", 2), item("c", 3))
	b := catalog(item("a", 1), item("b", 9), item("d", 4))

	p := deep.MustDiff(a, b)
	for _, op := range p.Operations {
		t.Logf("op: %s %s", op.Kind, op.Path)
	}
	// remove c, change b's qty in place, add d — three operations, none of
	// them a whole-list replace.
	if len(p.Operations) != 3 {
		t.Fatalf("got %d operations, want 3", len(p.Operations))
	}

	// Applying to a target holding the same elements in a different order
	// still lands, because elements are addressed by key.
	target := catalog(item("c", 3), item("b", 2), item("a", 1))
	if err := deep.Apply(&target, p); err != nil {
		t.Fatalf("apply to reordered target: %v", err)
	}
	byID := map[string]int32{}
	for _, it := range target.Items {
		byID[it.Id] = it.Qty
	}
	if len(byID) != 3 || byID["a"] != 1 || byID["b"] != 9 || byID["d"] != 4 {
		t.Errorf("after apply: %v", target.Items)
	}
}

func TestKeyedListSurvivesTheWire(t *testing.T) {
	a := catalog(item("a", 1))
	b := catalog(item("a", 1), item("b", 2))
	p := deep.MustDiff(a, b)

	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var wirePatch deep.Patch[*testpb.Catalog]
	if err := json.Unmarshal(data, &wirePatch); err != nil {
		t.Fatal(err)
	}
	got := deep.Clone(a)
	if err := deep.Apply(&got, wirePatch); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !proto.Equal(got, b) {
		t.Errorf("wire round trip missed: %v", got.Items)
	}
}

func TestKeyedListConditionsAndStrict(t *testing.T) {
	c := catalog(item("a", 5))

	// A condition addressed by key.
	p := deep.Patch[*testpb.Catalog]{Operations: []deep.Operation{{
		Kind: deep.OpReplace, Path: "/name", New: "restocked",
		If: &condition.Condition{Op: condition.Ge, Path: "/items/a/qty", Value: 5},
	}}}
	res, err := deep.ApplyWithResult(&c, p)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !res.AllApplied() || c.GetName() != "restocked" {
		t.Fatalf("keyed condition did not hold: %s", res)
	}

	// A strict sub-field write addressed by key.
	sp := deep.Patch[*testpb.Catalog]{
		Strict: true,
		Operations: []deep.Operation{{
			Kind: deep.OpReplace, Path: "/items/a/qty", Old: int32(5), New: int32(4),
		}},
	}
	if err := deep.Apply(&c, sp); err != nil {
		t.Fatalf("strict keyed apply: %v", err)
	}
	if c.Items[0].Qty != 4 {
		t.Errorf("qty = %d, want 4", c.Items[0].Qty)
	}
	// The same patch again: Old no longer matches.
	if err := deep.Apply(&c, sp); err == nil {
		t.Error("strict keyed apply passed against drifted state")
	}
}

// ── the protobuf envelope ────────────────────────────────────────────────────

func TestPatchProtoEnvelopeRoundTrip(t *testing.T) {
	a := catalog(item("a", 1), item("b", 2))
	b := catalog(item("a", 7), item("c", 3))

	p := deep.MustDiff(a, b)
	p.Strict = true
	p.Guard = &condition.Condition{Op: condition.Eq, Path: "/name", Value: "shop"}

	env, err := deepproto.ToProto(p)
	if err != nil {
		t.Fatalf("ToProto: %v", err)
	}
	// The envelope is an ordinary message: it survives proto.Marshal, which is
	// the point — a patch inside a gRPC request as a typed field.
	data, err := proto.Marshal(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	env2 := &wire.Patch{}
	if err := proto.Unmarshal(data, env2); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}

	back, err := deepproto.FromProto[*testpb.Catalog](env2)
	if err != nil {
		t.Fatalf("FromProto: %v", err)
	}
	if !back.Strict || back.Guard == nil || back.Guard.Op != condition.Eq {
		t.Fatalf("patch metadata lost: strict=%v guard=%+v", back.Strict, back.Guard)
	}

	got := deep.Clone(a)
	if err := deep.Apply(&got, back); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !proto.Equal(got, b) {
		t.Errorf("envelope round trip missed the target: %v", got.Items)
	}
}
