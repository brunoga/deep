package crdt

import (
	"sync"
	"testing"
)

func TestCounter_BasicIncrement(t *testing.T) {
	c := NewCounter("node-a")
	c.Increment(1)
	c.Increment(1)
	c.Increment(1)
	if got := c.Value(); got != 3 {
		t.Errorf("expected 3, got %d", got)
	}
}

func TestCounter_BasicDecrement(t *testing.T) {
	c := NewCounter("node-a")
	c.Increment(5)
	c.Decrement(2)
	if got := c.Value(); got != 3 {
		t.Errorf("expected 3, got %d", got)
	}
}

func TestCounter_ZeroAndNegativeDeltaIgnored(t *testing.T) {
	c := NewCounter("node-a")
	c.Increment(0)
	c.Increment(-1)
	c.Decrement(0)
	if got := c.Value(); got != 0 {
		t.Errorf("expected 0, got %d", got)
	}
}

func TestCounter_MergeIndependentNodes(t *testing.T) {
	a := NewCounter("node-a")
	b := NewCounter("node-b")

	a.Increment(3)
	b.Increment(2)

	a.Merge(b)
	b.Merge(a)

	if got := a.Value(); got != 5 {
		t.Errorf("node-a: expected 5, got %d", got)
	}
	if got := b.Value(); got != 5 {
		t.Errorf("node-b: expected 5, got %d", got)
	}
}

func TestCounter_ConcurrentIncrements(t *testing.T) {
	a := NewCounter("node-a")
	b := NewCounter("node-b")

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 10; i++ {
			a.Increment(1)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 10; i++ {
			b.Increment(1)
		}
	}()
	wg.Wait()

	a.Merge(b)
	b.Merge(a)

	if got := a.Value(); got != 20 {
		t.Errorf("node-a: expected 20, got %d", got)
	}
	if got := b.Value(); got != 20 {
		t.Errorf("node-b: expected 20, got %d", got)
	}
}

func TestCounter_MergeIdempotent(t *testing.T) {
	a := NewCounter("node-a")
	b := NewCounter("node-b")

	a.Increment(4)
	b.Increment(6)

	a.Merge(b)
	first := a.Value()

	a.Merge(b)
	second := a.Value()

	if first != second {
		t.Errorf("merge not idempotent: first=%d second=%d", first, second)
	}
	if first != 10 {
		t.Errorf("expected 10, got %d", first)
	}
}

func TestCounter_Commutativity(t *testing.T) {
	a := NewCounter("node-a")
	b := NewCounter("node-b")

	a.Increment(7)
	b.Increment(3)

	// merge(A into B)
	bCopy := NewCounter("node-b")
	bCopy.Increment(3)
	bCopy.Merge(a)

	// merge(B into A)
	a.Merge(b)

	if a.Value() != bCopy.Value() {
		t.Errorf("merge not commutative: merge(B,A)=%d merge(A,B)=%d", a.Value(), bCopy.Value())
	}
}

func TestCounter_NegativeValue(t *testing.T) {
	c := NewCounter("node-a")
	c.Increment(2)
	c.Decrement(5)
	if got := c.Value(); got != -3 {
		t.Errorf("expected -3, got %d", got)
	}
}
