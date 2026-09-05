package deepproto_test

import (
	"encoding/json"
	"testing"

	"github.com/brunoga/deep/proto/internal/genmodel"
	deep "github.com/brunoga/deep/v6"
	"google.golang.org/protobuf/proto"
)

// The end-to-end proof for gen.DiffOpaque: a deep-gen'd struct holding a
// protobuf message gives the message the family's fine-grained diff, where
// the generated code previously replaced the whole message.
func TestGeneratedStructGivesProtoFieldsFineGrainedDiffs(t *testing.T) {
	a := genmodel.Doc{Name: "d", Meta: mustStruct(t, map[string]any{"x": 1.0, "y": 2.0, "z": 3.0})}
	b := genmodel.Doc{Name: "d", Meta: mustStruct(t, map[string]any{"x": 1.0, "y": 9.0, "z": 3.0})}

	p := a.Diff(&b) // the generated method, not reflection
	for _, op := range p.Operations {
		t.Logf("generated op: %s %s", op.Kind, op.Path)
	}
	if len(p.Operations) != 1 {
		t.Fatalf("got %d operations for a one-field change, want 1", len(p.Operations))
	}
	if p.Operations[0].Path != "/meta/fields/y/numberValue" {
		t.Errorf("path = %q — the family's fine-grained path was expected", p.Operations[0].Path)
	}

	got := deep.Clone(a)
	if err := deep.Apply(&got, p); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !deep.Equal(got, b) {
		t.Error("generated diff+apply did not reach the target")
	}
	if _, err := proto.Marshal(got.Meta); err != nil {
		t.Errorf("patched message no longer marshals: %v", err)
	}

	// And through the wire.
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var wirePatch deep.Patch[genmodel.Doc]
	if err := json.Unmarshal(data, &wirePatch); err != nil {
		t.Fatal(err)
	}
	got2 := deep.Clone(a)
	if err := deep.Apply(&got2, wirePatch); err != nil {
		t.Fatalf("wire apply: %v", err)
	}
	if !deep.Equal(got2, b) {
		t.Error("wire round trip missed the target")
	}
}

func TestGeneratedStructEqualCloneWithProtoFields(t *testing.T) {
	a := genmodel.Doc{Meta: mustStruct(t, map[string]any{"k": 1.0})}
	if _, err := proto.Marshal(a.Meta); err != nil { // populate internal state
		t.Fatal(err)
	}
	c := deep.Clone(a)
	if !deep.Equal(a, c) {
		t.Error("clone does not equal source")
	}
	if _, err := proto.Marshal(c.Meta); err != nil {
		t.Errorf("cloned message rejected by the proto runtime: %v", err)
	}
}
