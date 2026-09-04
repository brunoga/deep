package deep_test

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	deep "github.com/brunoga/deep/v6"
	"github.com/brunoga/deep/v6/condition"
	"github.com/brunoga/deep/v6/internal/testmodels/record"
)

// This file compares two ways of writing to a contended row: whole-payload
// compare-and-swap, and a conditional patch applied inside the transaction.
//
// What is being measured is how often each has to try again. Both do identical
// work per attempt — one round trip, one deserialize, one serialize — so the
// difference between them is entirely the size of the window each treats as a
// conflict. Compare-and-swap conflicts when *any* field changed; a conditional
// patch conflicts only when the field its condition names changed.
//
// Two details of the model are load-bearing rather than decorative.
//
// The round trip is simulated because everything here is fast enough
// in-process that a client's read and its write land nanoseconds apart, so a
// version check would almost never see an intervening commit and
// compare-and-swap would look free. In a real system that gap is a network
// round trip, and it is exactly the interval during which other writers
// commit. Leaving it out does not simplify the comparison, it deletes it.
//
// The round trip is jittered because a fixed one makes writers resonate:
// everyone sleeps the same length, wakes in the same order, and misses in
// lockstep, which inflates retries for reasons that have nothing to do with
// either protocol. Real latency varies.
//
// Neither strategy backs off. Real clients do, which trades retries for
// latency rather than removing conflicts — the conflict rate is a property of
// the protocol, not of the retry policy. Read the ratio between the two
// strategies rather than either absolute number.
const simulatedRTT = 200 * time.Microsecond

// A store standing in for a database row holding a serialized blob. Each
// method holds the lock for its whole body, which is what a transaction over a
// single row amounts to.
//
// A rejected write hands the current row back with the rejection, so a retry
// costs one round trip rather than two. Both strategies get that, which keeps
// the comparison about conflict granularity alone.
type store struct {
	mu      sync.Mutex
	blob    []byte
	version int
}

func newStore(id string) *store {
	blob, err := json.Marshal(record.Record{ID: id})
	if err != nil {
		panic(err)
	}
	return &store{blob: blob, version: 1}
}

func (s *store) decodeLocked() record.Record {
	var r record.Record
	if err := json.Unmarshal(s.blob, &r); err != nil {
		panic(err)
	}
	return r
}

func (s *store) commitLocked(r record.Record) {
	blob, err := json.Marshal(r)
	if err != nil {
		panic(err)
	}
	s.blob, s.version = blob, s.version+1
}

func (s *store) read() (record.Record, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.decodeLocked(), s.version
}

// casWrite accepts the client's whole payload only if nothing has committed
// since the client read. Two writers that touched entirely unrelated fields
// still collide, because the unit of conflict is the row.
func (s *store) casWrite(r record.Record, expectedVersion int) (bool, record.Record, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.version != expectedVersion {
		return false, s.decodeLocked(), s.version
	}
	s.commitLocked(r)
	return true, r, s.version
}

// patchWrite applies a conditional patch inside the transaction. The client
// sends no payload: the store loads the row, applies the operations whose
// conditions still hold, and commits.
//
// The result, not the error, says whether the write landed. A condition that
// no longer holds is not a failure — it is the answer the condition asked for,
// and the only case the client has to retry.
func (s *store) patchWrite(p deep.Patch[record.Record]) (bool, record.Record, int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	r := s.decodeLocked()
	res, err := deep.ApplyWithResult(&r, p)
	if err != nil {
		panic(err)
	}
	if !res.AllApplied() {
		return false, s.decodeLocked(), s.version
	}

	r.Version++
	s.commitLocked(r)
	return true, r, s.version
}

// ── the two client strategies ────────────────────────────────────────────────

// client is one writer. It starts from a row it already has — read while
// serving the request that triggered the write — and keeps whatever the store
// hands back with a rejection, so every attempt is one round trip either way.
type client struct {
	store   *store
	rng     *rand.Rand
	have    record.Record
	version int
}

func newClient(s *store, seed int64) *client {
	c := &client{store: s, rng: rand.New(rand.NewSource(seed))}
	c.have, c.version = s.read()
	return c
}

func (c *client) roundTrip() {
	// Uniform in [0.5, 1.5) of the nominal latency.
	jittered := simulatedRTT/2 + time.Duration(c.rng.Int63n(int64(simulatedRTT)))
	time.Sleep(jittered)
}

// casIncrement adds one to the writer's counter with whole-payload
// compare-and-swap, returning the retries it took.
func (c *client) casIncrement(field string) int {
	for retries := 0; ; retries++ {
		next := c.have
		next.Set(field, next.Get(field)+1)

		c.roundTrip()
		ok, current, version := c.store.casWrite(next, c.version)
		c.have, c.version = current, version
		if ok {
			return retries
		}
		// Anyone's write invalidates this one, whatever field they touched.
	}
}

// patchIncrement does the same through a conditional patch whose condition
// names only the counter this writer owns, so a concurrent write to any other
// field is not a conflict and costs nothing.
func (c *client) patchIncrement(field string) int {
	for retries := 0; ; retries++ {
		observed := c.have.Get(field)

		p := deep.Patch[record.Record]{Operations: []deep.Operation{{
			Kind: deep.OpReplace,
			Path: field,
			New:  observed + 1,
			// Built by hand because the path is chosen at run time. Where the
			// path is known at build time, deep.Eq with a typed selector gives
			// the same condition with the path checked by the compiler.
			If: &condition.Condition{Op: condition.Eq, Path: field, Value: observed},
		}}}

		c.roundTrip()
		ok, current, version := c.store.patchWrite(p)
		c.have, c.version = current, version
		if ok {
			return retries
		}
	}
}

type strategy struct {
	name string
	incr func(*client, string) int
}

var strategies = []strategy{
	{"CAS", (*client).casIncrement},
	{"ConditionalPatch", (*client).patchIncrement},
}

// ── correctness ──────────────────────────────────────────────────────────────

func TestContentionStrategiesLoseNoUpdates(t *testing.T) {
	// Neither measurement is worth reading until both strategies are correct:
	// every increment has to survive, with no lost updates.
	const writers, perWriter = 8, 25

	for _, st := range strategies {
		t.Run(st.name, func(t *testing.T) {
			s := newStore("item-1")
			var wg sync.WaitGroup
			for w := 0; w < writers; w++ {
				field := record.Fields[w%len(record.Fields)]
				seed := int64(w) + 1
				wg.Add(1)
				go func() {
					defer wg.Done()
					c := newClient(s, seed)
					for i := 0; i < perWriter; i++ {
						st.incr(c, field)
					}
				}()
			}
			wg.Wait()

			final, _ := s.read()
			if got, want := final.Total(), writers*perWriter; got != want {
				t.Errorf("total = %d, want %d — %d updates were lost", got, want, want-got)
			}
		})
	}
}

// ── contention benchmark ─────────────────────────────────────────────────────

// BenchmarkContention measures both strategies against one shared row as the
// number of concurrent writers rises.
//
// Writers take the eight independent counters in turn, so at eight writers or
// fewer no two touch the same field: every retry either strategy pays at those
// sizes is one its conflict granularity invented rather than one the data
// required. Past eight, writers start sharing a counter and some conflicts
// become real for both.
//
// retries/op is the number to read. ns/op is dominated by the simulated round
// trip and is only meaningful against the other strategy in the same run.
//
//	go test -run=XXX -bench=Contention -benchtime=2000x
func BenchmarkContention(b *testing.B) {
	for _, writers := range []int{2, 4, 8, 16, 32} {
		for _, st := range strategies {
			b.Run(fmt.Sprintf("%s/writers=%d", st.name, writers), func(b *testing.B) {
				s := newStore("item-1")
				var retries int64

				// Goroutines are spawned directly rather than through
				// RunParallel, whose SetParallelism multiplies by GOMAXPROCS:
				// "8 writers" would have meant 8 per core, and with more
				// writers than fields the conditional patches would have been
				// contending with each other over a shared counter rather than
				// running on the independent ones this is meant to measure.
				per, extra := b.N/writers, b.N%writers

				b.ResetTimer()
				var wg sync.WaitGroup
				for w := 0; w < writers; w++ {
					n := per
					if w < extra {
						n++
					}
					field := record.Fields[w%len(record.Fields)]
					wg.Add(1)
					go func(w, n int, field string) {
						defer wg.Done()
						// Each writer keeps one field, the way a service owns
						// the column it maintains.
						c := newClient(s, int64(w)+1)
						local := 0
						for i := 0; i < n; i++ {
							local += st.incr(c, field)
						}
						atomic.AddInt64(&retries, int64(local))
					}(w, n, field)
				}
				wg.Wait()
				b.StopTimer()

				b.ReportMetric(float64(retries)/float64(b.N), "retries/op")
			})
		}
	}
}
