package crdt

import (
	"encoding/json"
	"sync"
	"testing"
	"time"
)

type cursor struct {
	Name  string `json:"name"`
	Index int    `json:"index"`
}

// clock is a settable time source, so expiry can be tested without sleeping.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *clock { return &clock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)} }
func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}
func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func TestAwarenessSharesStateBetweenPeers(t *testing.T) {
	a := NewAwareness[cursor]("alice")
	b := NewAwareness[cursor]("bob")

	b.Apply(a.SetLocal(cursor{Name: "Alice", Index: 3}))
	a.Apply(b.SetLocal(cursor{Name: "Bob", Index: 7}))

	if got := b.States()["alice"]; got.Name != "Alice" || got.Index != 3 {
		t.Errorf("bob sees alice as %+v", got)
	}
	if got := a.States()["bob"]; got.Name != "Bob" || got.Index != 7 {
		t.Errorf("alice sees bob as %+v", got)
	}
	// Each also sees itself.
	if _, ok := a.States()["alice"]; !ok {
		t.Error("alice does not see herself")
	}
}

func TestAwarenessLastWriteWinsPerPeer(t *testing.T) {
	a := NewAwareness[cursor]("alice")
	b := NewAwareness[cursor]("bob")

	first := b.SetLocal(cursor{Index: 1})
	second := b.SetLocal(cursor{Index: 2})

	// Out of order: the newer update arrives first, and the older one must not
	// undo it.
	a.Apply(second)
	a.Apply(first)
	if got := a.States()["bob"].Index; got != 2 {
		t.Errorf("index = %d, want 2 — a stale update was accepted", got)
	}

	// Replaying is a no-op.
	if changes := a.Apply(second); len(changes) != 0 {
		t.Errorf("replaying an update reported %d changes", len(changes))
	}
}

func TestAwarenessLeaveIsImmediate(t *testing.T) {
	a := NewAwareness[cursor]("alice")
	b := NewAwareness[cursor]("bob")
	a.Apply(b.SetLocal(cursor{Name: "Bob"}))

	if _, ok := a.States()["bob"]; !ok {
		t.Fatal("bob never arrived")
	}
	changes := a.Apply(b.Leave())
	if len(changes) != 1 || changes[0].Kind != PresenceLeft {
		t.Fatalf("got %+v, want a single leave", changes)
	}
	if _, ok := a.States()["bob"]; ok {
		t.Error("bob is still present after leaving")
	}
}

func TestAwarenessExpiresSilentPeers(t *testing.T) {
	c := newClock()
	a := NewAwareness[cursor]("alice", WithTTL[cursor](10*time.Second), WithClock[cursor](c.now))
	b := NewAwareness[cursor]("bob")

	a.SetLocal(cursor{Name: "Alice"}) // you are present once you say so
	a.Apply(b.SetLocal(cursor{Name: "Bob"}))
	c.advance(9 * time.Second)
	if _, ok := a.States()["bob"]; !ok {
		t.Fatal("bob expired early")
	}

	c.advance(2 * time.Second)
	if _, ok := a.States()["bob"]; ok {
		t.Error("bob should have timed out")
	}
	// Alice never times herself out.
	if _, ok := a.States()["alice"]; !ok {
		t.Error("alice expired herself")
	}

	// A peer that comes back is simply present again.
	a.Apply(b.SetLocal(cursor{Name: "Bob", Index: 5}))
	if got, ok := a.States()["bob"]; !ok || got.Index != 5 {
		t.Errorf("bob did not come back: %+v ok=%v", got, ok)
	}
}

func TestAwarenessIgnoresEchoesOfItself(t *testing.T) {
	// A hub that rebroadcasts everything will send alice her own state back.
	// Accepting it could resurrect a state she has already moved on from.
	a := NewAwareness[cursor]("alice")
	echo := a.SetLocal(cursor{Index: 1})
	a.SetLocal(cursor{Index: 2})

	a.Apply(echo)
	if got, _ := a.Local(); got.Index != 2 {
		t.Errorf("index = %d, want 2 — an echo overwrote the local state", got.Index)
	}
}

func TestAwarenessNotifiesObservers(t *testing.T) {
	a := NewAwareness[cursor]("alice")
	b := NewAwareness[cursor]("bob")

	var mu sync.Mutex
	var seen []PresenceChange[cursor]
	cancel := a.OnChange(func(c PresenceChange[cursor]) {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, c)
	})

	a.Apply(b.SetLocal(cursor{Name: "Bob"}))
	a.Apply(b.SetLocal(cursor{Name: "Bob", Index: 2}))
	a.Apply(b.Leave())
	cancel()
	a.Apply(NewAwareness[cursor]("carol").SetLocal(cursor{}))

	mu.Lock()
	defer mu.Unlock()
	want := []PresenceKind{PresenceJoined, PresenceUpdated, PresenceLeft}
	if len(seen) != len(want) {
		t.Fatalf("got %d changes, want %d: %+v", len(seen), len(want), seen)
	}
	for i, k := range want {
		if seen[i].Kind != k {
			t.Errorf("change %d is %v, want %v", i, seen[i].Kind, k)
		}
	}
}

func TestAwarenessObserverMayReadInsideCallback(t *testing.T) {
	// Notifying under the lock would deadlock here. The CRDT observers learned
	// this the hard way; awareness must not repeat it.
	a := NewAwareness[cursor]("alice")
	b := NewAwareness[cursor]("bob")

	done := make(chan struct{})
	a.OnChange(func(PresenceChange[cursor]) {
		_ = a.States()
		close(done)
	})
	a.Apply(b.SetLocal(cursor{}))

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("observer deadlocked reading the awareness it was notified by")
	}
}

func TestAwarenessUpdateCatchesUpANewPeer(t *testing.T) {
	hub := NewAwareness[cursor]("hub")
	for _, name := range []string{"a", "b", "c"} {
		peer := NewAwareness[cursor](name)
		hub.Apply(peer.SetLocal(cursor{Name: name}))
	}

	joiner := NewAwareness[cursor]("joiner")
	joiner.Apply(hub.Update())

	states := joiner.States()
	for _, name := range []string{"a", "b", "c"} {
		if _, ok := states[name]; !ok {
			t.Errorf("joiner did not learn about %s", name)
		}
	}
}

func TestAwarenessUpdateRoundTripsAsJSON(t *testing.T) {
	a := NewAwareness[cursor]("alice")
	u := a.SetLocal(cursor{Name: "Alice", Index: 4})

	data, err := json.Marshal(u)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back AwarenessUpdate[cursor]
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	peer := NewAwareness[cursor]("bob")
	peer.Apply(back)
	if got := peer.States()["alice"]; got.Name != "Alice" || got.Index != 4 {
		t.Errorf("after a round trip bob sees %+v", got)
	}

	// A departure survives too: a nil state is what says the peer has gone.
	leave, _ := json.Marshal(a.Leave())
	var backLeave AwarenessUpdate[cursor]
	if err := json.Unmarshal(leave, &backLeave); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	peer.Apply(backLeave)
	if _, ok := peer.States()["alice"]; ok {
		t.Error("alice is still present after a departure round trip")
	}
}

func TestAwarenessIsConcurrencySafe(t *testing.T) {
	a := NewAwareness[cursor]("alice")
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			peer := NewAwareness[cursor](string(rune('a' + i)))
			for j := 0; j < 200; j++ {
				a.Apply(peer.SetLocal(cursor{Index: j}))
				a.SetLocal(cursor{Index: j})
				_ = a.States()
				a.Expire()
			}
		}(i)
	}
	wg.Wait()
}
