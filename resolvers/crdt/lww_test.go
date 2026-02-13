package crdt

import (
	"reflect"
	"testing"

	"github.com/brunoga/deep/v3"
	"github.com/brunoga/deep/v3/crdt/hlc"
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

	// Newer op should be accepted
	if !resolver.Resolve("f1", deep.OpReplace, nil, nil, reflect.Value{}) {
		t.Error("Should accept newer operation")
	}
	if clocks["f1"].WallTime != 101 {
		t.Error("Clock should have been updated")
	}

	// Older op should be rejected
	resolver.OpTime = hlc.HLC{WallTime: 99, Logical: 0, NodeID: "C"}
	if resolver.Resolve("f1", deep.OpReplace, nil, nil, reflect.Value{}) {
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

	if !resolver.Resolve("f1", deep.OpReplace, nil, nil, reflect.Value{}) {
		t.Error("Remote should win (newer)")
	}

	resolver.RemoteClocks["f1"] = hlc.HLC{WallTime: 99, Logical: 0, NodeID: "B"}
	if resolver.Resolve("f1", deep.OpReplace, nil, nil, reflect.Value{}) {
		t.Error("Local should win (remote is older)")
	}
}
