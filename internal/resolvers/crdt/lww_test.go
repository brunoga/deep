package crdt

import (
	"reflect"
	"testing"

	"github.com/brunoga/deep/v5/crdt/hlc"
	"github.com/brunoga/deep/v5/internal/engine"
)

func TestLWWResolver(t *testing.T) {
	clocks := map[string]hlc.HLC{
		"f1": {WallTime: 100, Logical: 0, NodeID: "A"},
	}
	tombstones := make(map[string]hlc.HLC)

	resolver := &LWWResolver{
		Clocks:     clocks,
		Tombstones: tombstones,
		OpTime:     hlc.HLC{WallTime: 101, Logical: 0, NodeID: "B"},
	}

	proposed := reflect.ValueOf("new")

	// Newer op should be accepted
	resolved, ok := resolver.Resolve("f1", engine.OpReplace, nil, nil, reflect.Value{}, proposed)
	if !ok {
		t.Error("Should accept newer operation")
	}
	if resolved.Interface() != "new" {
		t.Error("Resolved value mismatch")
	}
	if clocks["f1"].WallTime != 101 {
		t.Error("Clock should have been updated")
	}

	// Older op should be rejected
	resolver.OpTime = hlc.HLC{WallTime: 99, Logical: 0, NodeID: "C"}
	_, ok = resolver.Resolve("f1", engine.OpReplace, nil, nil, reflect.Value{}, proposed)
	if ok {
		t.Error("Should reject older operation")
	}
}

func TestStateResolver(t *testing.T) {
	localClocks := map[string]hlc.HLC{"f1": {WallTime: 100, Logical: 0, NodeID: "A"}}
	remoteClocks := map[string]hlc.HLC{"f1": {WallTime: 101, Logical: 0, NodeID: "B"}}

	resolver := &StateResolver{
		LocalClocks:  localClocks,
		RemoteClocks: remoteClocks,
	}

	proposed := reflect.ValueOf("remote")

	resolved, ok := resolver.Resolve("f1", engine.OpReplace, nil, nil, reflect.Value{}, proposed)
	if !ok {
		t.Error("Remote should win (newer)")
	}
	if resolved.Interface() != "remote" {
		t.Error("Resolved value mismatch")
	}

	resolver.RemoteClocks["f1"] = hlc.HLC{WallTime: 99, Logical: 0, NodeID: "B"}
	_, ok = resolver.Resolve("f1", engine.OpReplace, nil, nil, reflect.Value{}, proposed)
	if ok {
		t.Error("Local should win (remote is older)")
	}
}
