package hlc

import (
	"fmt"
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

// Now returns the current HLC timestamp.
func (c *Clock) Now() HLC {
	c.mu.Lock()
	defer c.mu.Unlock()

	physNow := time.Now().UnixNano()

	if physNow > c.Latest.WallTime {
		c.Latest.WallTime = physNow
		c.Latest.Logical = 0
	} else {
		c.Latest.Logical++
	}

	return c.Latest
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
