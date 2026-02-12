package deep

import (
	"encoding/json"
	"testing"
)

type customTestStruct struct {
	V int
}

func (c customTestStruct) Diff(other customTestStruct) (Patch[customTestStruct], error) {
	b := NewBuilder[customTestStruct]()
	b.Root().Field("V")
	node, _ := b.Root().Field("V")
	node.Set(c.V, other.V)
	return b.Build()
}

func TestCustomDiffPatch_ToJSONPatch(t *testing.T) {
	b := NewBuilder[customTestStruct]()
	node, _ := b.Root().Field("V")
	node.Set(1, 2)
	patch, _ := b.Build()

	// Manually wrap it in customDiffPatch
	custom := &customDiffPatch{
		patch: patch,
	}

	jsonBytes := custom.toJSONPatch("/root")

	var ops []map[string]any
	data, _ := json.Marshal(jsonBytes)
	json.Unmarshal(data, &ops)

	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}

	if ops[0]["path"] != "/root/V" {
		t.Errorf("expected path /root/V, got %s", ops[0]["path"])
	}
}
