package crdt

import (
	"sync"
	"time"
)

// Awareness tracks what the other people editing alongside you are doing right
// now: where their cursor is, what they have selected, the name and colour to
// draw them in.
//
// It is deliberately not part of the document. A cursor position is not an edit
// and must not be merged into the text, survive a reload, or appear in the
// history — and a peer that closes its laptop lid should stop being drawn
// rather than leave a cursor behind forever. So awareness state is held
// separately, last-write-wins per peer, and expires when a peer stops saying it
// is there.
//
// That expiry is what makes it safe to be so simple. Because nothing here is
// durable, a peer being dropped is always recoverable — it reappears with its
// next update — and the replicas never need to agree about who left.
//
// An Awareness is safe for concurrent use. Observers are called after the lock
// is released, so a callback is free to read the awareness, or to write to it.
type Awareness[T any] struct {
	mu     sync.RWMutex
	local  string
	ttl    time.Duration
	now    func() time.Time
	states map[string]awarenessEntry[T]

	nextObs   int
	observers map[int]func(PresenceChange[T])
}

type awarenessEntry[T any] struct {
	state T
	// clock orders a peer's own updates. It is not a hybrid logical clock:
	// awareness never merges two peers' states, so only a peer's own sequence
	// matters.
	clock int64
	seen  time.Time
	gone  bool
}

// PresenceKind is what happened to a peer.
type PresenceKind int

const (
	// PresenceJoined is a peer heard from for the first time.
	PresenceJoined PresenceKind = iota
	// PresenceUpdated is a peer whose state changed.
	PresenceUpdated
	// PresenceLeft is a peer that said goodbye or fell silent past the
	// timeout.
	PresenceLeft
)

func (k PresenceKind) String() string {
	switch k {
	case PresenceJoined:
		return "joined"
	case PresenceUpdated:
		return "updated"
	case PresenceLeft:
		return "left"
	default:
		return "unknown"
	}
}

// PresenceChange describes one peer's arrival, update or departure.
type PresenceChange[T any] struct {
	Node  string
	Kind  PresenceKind
	State T // the peer's new state; the zero value when it left
}

// AwarenessOption configures an [Awareness].
type AwarenessOption[T any] func(*Awareness[T])

// WithTTL sets how long a peer is kept after it was last heard from. The
// default is 30 seconds, which suits a client announcing itself every few
// seconds.
func WithTTL[T any](d time.Duration) AwarenessOption[T] {
	return func(a *Awareness[T]) { a.ttl = d }
}

// WithClock replaces the source of the current time. It exists so that expiry
// can be tested without sleeping.
func WithClock[T any](now func() time.Time) AwarenessOption[T] {
	return func(a *Awareness[T]) { a.now = now }
}

// NewAwareness returns an Awareness for the given local node id, which should
// be the same id the node uses for its edits.
func NewAwareness[T any](node string, opts ...AwarenessOption[T]) *Awareness[T] {
	a := &Awareness[T]{
		local:     node,
		ttl:       30 * time.Second,
		now:       time.Now,
		states:    make(map[string]awarenessEntry[T]),
		observers: make(map[int]func(PresenceChange[T])),
	}
	for _, o := range opts {
		o(a)
	}
	return a
}

// Node returns the local node id.
func (a *Awareness[T]) Node() string { return a.local }

// SetLocal records this node's state and returns the update to broadcast.
//
// Call it whenever the state changes, and again periodically even when it has
// not: an update is also the heartbeat that keeps this node from expiring on
// its peers.
func (a *Awareness[T]) SetLocal(state T) AwarenessUpdate[T] {
	a.mu.Lock()
	entry := a.states[a.local]
	entry.state = state
	entry.clock++
	entry.seen = a.now()
	entry.gone = false
	a.states[a.local] = entry

	kind := PresenceUpdated
	if entry.clock == 1 {
		kind = PresenceJoined
	}
	update := AwarenessUpdate[T]{Entries: []AwarenessEntry[T]{{
		Node:  a.local,
		Clock: entry.clock,
		State: &state,
	}}}
	a.mu.Unlock()

	a.notify(PresenceChange[T]{Node: a.local, Kind: kind, State: state})
	return update
}

// Local returns this node's own state.
func (a *Awareness[T]) Local() (T, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	e, ok := a.states[a.local]
	if !ok || e.gone {
		var zero T
		return zero, false
	}
	return e.state, true
}

// Leave marks this node as gone and returns the update to broadcast, so peers
// can drop it at once rather than waiting for it to time out.
func (a *Awareness[T]) Leave() AwarenessUpdate[T] {
	a.mu.Lock()
	entry := a.states[a.local]
	entry.clock++
	entry.gone = true
	entry.seen = a.now()
	var zero T
	entry.state = zero
	a.states[a.local] = entry
	clock := entry.clock
	a.mu.Unlock()

	a.notify(PresenceChange[T]{Node: a.local, Kind: PresenceLeft})
	return AwarenessUpdate[T]{Entries: []AwarenessEntry[T]{{
		Node:  a.local,
		Clock: clock,
		State: nil, // a nil state is how a peer says it is gone
	}}}
}

// States returns the state of every peer currently present, including this
// node. Peers that have left, or that have not been heard from within the
// timeout, are not included.
func (a *Awareness[T]) States() map[string]T {
	expired := a.expire()

	a.mu.RLock()
	out := make(map[string]T, len(a.states))
	for node, e := range a.states {
		if !e.gone {
			out[node] = e.state
		}
	}
	a.mu.RUnlock()

	a.notifyAll(expired)
	return out
}

// Update returns the full state of every peer this node knows about, which is
// what a peer that has just connected needs in order to catch up.
func (a *Awareness[T]) Update() AwarenessUpdate[T] {
	a.mu.RLock()
	defer a.mu.RUnlock()

	u := AwarenessUpdate[T]{Entries: make([]AwarenessEntry[T], 0, len(a.states))}
	for node, e := range a.states {
		entry := AwarenessEntry[T]{Node: node, Clock: e.clock}
		if !e.gone {
			state := e.state
			entry.State = &state
		}
		u.Entries = append(u.Entries, entry)
	}
	return u
}

// Apply merges an update from a peer and returns what changed.
//
// An entry is taken only if its clock is ahead of what this node already has
// for that peer, which makes applying the same update twice a no-op and lets
// updates arrive out of order.
func (a *Awareness[T]) Apply(u AwarenessUpdate[T]) []PresenceChange[T] {
	var changes []PresenceChange[T]

	a.mu.Lock()
	for _, entry := range u.Entries {
		// A peer does not get to speak for this node: its own state is
		// authoritative here, and accepting an echo of it could resurrect a
		// state this node has already moved on from.
		if entry.Node == a.local {
			continue
		}
		existing, known := a.states[entry.Node]
		if known && entry.Clock <= existing.clock {
			continue
		}

		next := awarenessEntry[T]{clock: entry.Clock, seen: a.now()}
		var change PresenceChange[T]
		switch {
		case entry.State == nil:
			next.gone = true
			change = PresenceChange[T]{Node: entry.Node, Kind: PresenceLeft}
		default:
			next.state = *entry.State
			kind := PresenceUpdated
			if !known || existing.gone {
				kind = PresenceJoined
			}
			change = PresenceChange[T]{Node: entry.Node, Kind: kind, State: next.state}
		}
		a.states[entry.Node] = next
		changes = append(changes, change)
	}
	a.mu.Unlock()

	a.notifyAll(changes)
	return changes
}

// Expire drops peers that have not been heard from within the timeout and
// returns what it dropped. States calls it, so most callers never need to —
// but a client that wants presence to fade without anyone asking can call it
// on a ticker.
func (a *Awareness[T]) Expire() []PresenceChange[T] {
	changes := a.expire()
	a.notifyAll(changes)
	return changes
}

// expire drops timed-out peers and returns the changes, without notifying.
func (a *Awareness[T]) expire() []PresenceChange[T] {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.ttl <= 0 {
		return nil
	}
	cutoff := a.now().Add(-a.ttl)
	var changes []PresenceChange[T]
	for node, e := range a.states {
		// This node never times itself out: it knows it is here.
		if node == a.local || e.gone || !e.seen.Before(cutoff) {
			continue
		}
		delete(a.states, node)
		changes = append(changes, PresenceChange[T]{Node: node, Kind: PresenceLeft})
	}
	return changes
}

// OnChange registers fn to be called whenever a peer joins, changes or leaves.
// The returned function unregisters it.
//
// Callbacks run after the awareness lock is released, so fn may read or write
// the awareness. They run on the goroutine that caused the change.
func (a *Awareness[T]) OnChange(fn func(PresenceChange[T])) (cancel func()) {
	a.mu.Lock()
	id := a.nextObs
	a.nextObs++
	a.observers[id] = fn
	a.mu.Unlock()

	return func() {
		a.mu.Lock()
		delete(a.observers, id)
		a.mu.Unlock()
	}
}

func (a *Awareness[T]) notify(c PresenceChange[T]) {
	a.mu.RLock()
	fns := make([]func(PresenceChange[T]), 0, len(a.observers))
	for _, fn := range a.observers {
		fns = append(fns, fn)
	}
	a.mu.RUnlock()

	for _, fn := range fns {
		fn(c)
	}
}

func (a *Awareness[T]) notifyAll(changes []PresenceChange[T]) {
	if len(changes) == 0 {
		return
	}
	for _, c := range changes {
		a.notify(c)
	}
}

// ── wire types ───────────────────────────────────────────────────────────────

// AwarenessEntry is one peer's state as it travels between replicas. A nil
// State means the peer has left.
type AwarenessEntry[T any] struct {
	Node  string `json:"n"`
	Clock int64  `json:"c"`
	State *T     `json:"s,omitempty"`
}

// AwarenessUpdate is what a replica broadcasts: one or more peers' states.
type AwarenessUpdate[T any] struct {
	Entries []AwarenessEntry[T] `json:"e,omitempty"`
}

// IsEmpty reports whether the update carries nothing.
func (u AwarenessUpdate[T]) IsEmpty() bool { return len(u.Entries) == 0 }
