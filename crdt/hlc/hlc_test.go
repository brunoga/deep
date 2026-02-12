package hlc

import (
	"testing"
	"time"
)

func TestHLC_Compare(t *testing.T) {
	h1 := HLC{WallTime: 100, Logical: 0, NodeID: "A"}
	h2 := HLC{WallTime: 100, Logical: 1, NodeID: "A"}
	h3 := HLC{WallTime: 101, Logical: 0, NodeID: "A"}
	h4 := HLC{WallTime: 100, Logical: 0, NodeID: "B"}

	if h1.Compare(h2) != -1 {
		t.Error("h1 should be less than h2 (logical)")
	}
	if h3.Compare(h1) != 1 {
		t.Error("h3 should be greater than h1 (wall time)")
	}
	if h1.Compare(h4) != -1 {
		t.Error("h1 should be less than h4 (node ID tie-break)")
	}
}

func TestClock_Now(t *testing.T) {
	c := NewClock("node1")
	t1 := c.Now()
	t2 := c.Now()

	if !t2.After(t1) {
		t.Errorf("Clock should increment: t1=%v, t2=%v", t1, t2)
	}
}

func TestClock_Update(t *testing.T) {
	c := NewClock("node1")
	remote := HLC{WallTime: time.Now().UnixNano() + 1000000, Logical: 5, NodeID: "node2"}
	
	c.Update(remote)
	local := c.Now()

	if !local.After(remote) {
		t.Errorf("Local clock should have jumped ahead of remote: local=%v, remote=%v", local, remote)
	}
}
