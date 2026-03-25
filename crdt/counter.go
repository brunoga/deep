package crdt

// counterState holds the grow-only increment and decrement maps for a PN-Counter.
type counterState struct {
	Inc map[string]int64 `json:"inc"`
	Dec map[string]int64 `json:"dec"`
}

// Counter is a Positive-Negative Counter CRDT. Each node maintains independent
// increment and decrement totals; the observed value is sum(Inc) - sum(Dec).
type Counter struct {
	inner *CRDT[counterState]
}

// NewCounter creates a new Counter for the given nodeID.
func NewCounter(nodeID string) *Counter {
	return &Counter{
		inner: NewCRDT(counterState{
			Inc: make(map[string]int64),
			Dec: make(map[string]int64),
		}, nodeID),
	}
}

// NodeID returns the node identifier for this Counter.
func (c *Counter) NodeID() string { return c.inner.NodeID() }

// Increment adds delta to this node's increment total. Ignored if delta <= 0.
func (c *Counter) Increment(delta int64) {
	if delta <= 0 {
		return
	}
	nodeID := c.inner.NodeID()
	c.inner.Edit(func(s *counterState) {
		s.Inc[nodeID] += delta
	})
}

// Decrement adds delta to this node's decrement total. Ignored if delta <= 0.
func (c *Counter) Decrement(delta int64) {
	if delta <= 0 {
		return
	}
	nodeID := c.inner.NodeID()
	c.inner.Edit(func(s *counterState) {
		s.Dec[nodeID] += delta
	})
}

// Value returns the current counter value: sum(Inc) - sum(Dec).
func (c *Counter) Value() int64 {
	s := c.inner.View()
	var total int64
	for _, v := range s.Inc {
		total += v
	}
	for _, v := range s.Dec {
		total -= v
	}
	return total
}

// Merge merges the state of other into this Counter. Returns true if any
// changes were applied.
func (c *Counter) Merge(other *Counter) bool {
	return c.inner.Merge(other.inner)
}
