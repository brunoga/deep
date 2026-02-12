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

func TestHLC_String(t *testing.T) {
	h := HLC{WallTime: 12345, Logical: 6, NodeID: "node1"}
	expected := "12345:6:node1"
	if h.String() != expected {
		t.Errorf("expected %s, got %s", expected, h.String())
	}
}

func TestHLC_CompareMore(t *testing.T) {
	h1 := HLC{WallTime: 100, Logical: 1, NodeID: "A"}
	
	cases := []struct {
		h2       HLC
		expected int
	}{
		{HLC{WallTime: 101, Logical: 1, NodeID: "A"}, -1},
		{HLC{WallTime: 99, Logical: 1, NodeID: "A"}, 1},
		{HLC{WallTime: 100, Logical: 2, NodeID: "A"}, -1},
		{HLC{WallTime: 100, Logical: 0, NodeID: "A"}, 1},
		{HLC{WallTime: 100, Logical: 1, NodeID: "B"}, -1},
		{HLC{WallTime: 100, Logical: 1, NodeID: "0"}, 1},
	}

	for _, c := range cases {
		if res := h1.Compare(c.h2); res != c.expected {
			t.Errorf("Compare %v vs %v: expected %d, got %d", h1, c.h2, c.expected, res)
		}
	}
}

func TestClock_UpdateMore(t *testing.T) {
	c := NewClock("L")
	
	// Remote time in future
	remote := HLC{WallTime: time.Now().Add(time.Hour).UnixNano(), Logical: 5, NodeID: "R"}
	c.Update(remote)
	if c.Latest.WallTime != remote.WallTime {
		t.Error("should have updated wall time")
	}
	if c.Latest.Logical != remote.Logical+1 {
		t.Errorf("expected logical %d, got %d", remote.Logical+1, c.Latest.Logical)
	}

	// Remote time same as local, but remote logical higher
	local := c.Latest
	remote2 := local
	remote2.Logical += 10
	remote2.NodeID = "R2"
	c.Update(remote2)
	if c.Latest.Logical != remote2.Logical+1 {
		t.Errorf("expected logical %d, got %d", remote2.Logical+1, c.Latest.Logical)
	}
}
