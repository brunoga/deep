// Package hlc implements a Hybrid Logical Clock (HLC) for distributed
// causality tracking.
//
// An [HLC] timestamp combines a physical wall-clock component with a logical
// counter and a node identifier, providing a total ordering of events across
// nodes that is consistent with real time. When two events share the same wall
// time, the logical counter breaks ties; when both are equal, the node ID
// provides a deterministic tiebreaker.
//
// Use [NewClock] to create a per-node clock, [Clock.Now] to generate a new
// timestamp, and [Clock.Update] to advance the clock when receiving a remote
// timestamp (ensuring the local clock is always at least as recent as any
// observed remote event).
package hlc

import (
	"fmt"
	"math"
	"sync"
	"time"
)

// HLC represents a Hybrid Logical Clock timestamp.
type HLC struct {
	WallTime int64  `json:"w"` // Physical time (unix nanoseconds)
	Logical  int32  `json:"l"` // Logical counter
	NodeID   string `json:"n"` // Unique node identifier
}

// Compare returns -1 if h < other, 1 if h > other, and 0 if equal.
func (h HLC) Compare(other HLC) int {
	if h.WallTime < other.WallTime {
		return -1
	}
	if h.WallTime > other.WallTime {
		return 1
	}
	if h.Logical < other.Logical {
		return -1
	}
	if h.Logical > other.Logical {
		return 1
	}
	if h.NodeID < other.NodeID {
		return -1
	}
	if h.NodeID > other.NodeID {
		return 1
	}
	return 0
}

// After returns true if h is strictly after other.
func (h HLC) After(other HLC) bool {
	return h.Compare(other) > 0
}

func (h HLC) String() string {
	return fmt.Sprintf("%d:%d:%s", h.WallTime, h.Logical, h.NodeID)
}

// Clock manages the local HLC state.
//
// Latest is exposed for serialisation but must not be mutated by callers
// after the clock is in use — direct writes bypass the internal mutex and
// race with concurrent Now/Update. Use [Clock.SetLatest] for explicit
// rehydration (e.g. from snapshots).
type Clock struct {
	mu     sync.Mutex
	Latest HLC
	NodeID string
}

// NewClock creates a new clock for the given node ID.
func NewClock(nodeID string) *Clock {
	return &Clock{
		NodeID: nodeID,
		Latest: HLC{
			WallTime: 0,
			Logical:  0,
			NodeID:   nodeID,
		},
	}
}

// SetLatest rehydrates the clock from a previously observed timestamp under
// the clock's mutex, so it is safe to call alongside concurrent Now/Update.
// Subsequent Now/Update calls advance from at least h.
func (c *Clock) SetLatest(h HLC) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Latest = h
}

// Now returns the current HLC timestamp.
func (c *Clock) Now() HLC {
	return c.Reserve(1)
}

// Reserve returns the current HLC timestamp and reserves n logical ticks.
//
// n must be non-negative and small enough that c.Latest.Logical + n fits in
// int32; otherwise Reserve panics. Practical text inserts and similar uses
// fall well under that bound; overflow would silently break causal ordering,
// so an explicit panic is preferred to a wraparound bug.
func (c *Clock) Reserve(n int) HLC {
	if n < 0 {
		panic("hlc: Reserve called with negative n")
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	physNow := time.Now().UnixNano()

	if physNow > c.Latest.WallTime {
		c.Latest.WallTime = physNow
		c.Latest.Logical = 0
	}

	if int64(c.Latest.Logical)+int64(n) > int64(math.MaxInt32) {
		panic("hlc: Reserve would overflow Logical (int32)")
	}

	start := c.Latest
	c.Latest.Logical += int32(n)
	return start
}

// Update updates the local clock based on a remote timestamp.
func (c *Clock) Update(remote HLC) {
	c.mu.Lock()
	defer c.mu.Unlock()

	physNow := time.Now().UnixNano()

	nextWall := physNow
	if c.Latest.WallTime > nextWall {
		nextWall = c.Latest.WallTime
	}
	if remote.WallTime > nextWall {
		nextWall = remote.WallTime
	}

	var nextLog int32
	if nextWall == c.Latest.WallTime && nextWall == remote.WallTime {
		maxLog := c.Latest.Logical
		if remote.Logical > maxLog {
			maxLog = remote.Logical
		}
		nextLog = maxLog + 1
	} else if nextWall == c.Latest.WallTime {
		nextLog = c.Latest.Logical + 1
	} else if nextWall == remote.WallTime {
		nextLog = remote.Logical + 1
	} else {
		nextLog = 0
	}

	c.Latest.WallTime = nextWall
	c.Latest.Logical = nextLog
}
