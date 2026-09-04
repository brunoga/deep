package crdt

import (
	"encoding/json"
	"math/rand"
	"testing"

	"github.com/brunoga/deep/v6/crdt/hlc"
)

// The point of the whole thing: syncing costs the size of the change, not the
// size of the document.
func TestSyncSendsOnlyWhatIsMissing(t *testing.T) {
	a := NewDocument(hlc.NewClock("a"))
	b := NewDocument(hlc.NewClock("b"))

	// A large shared document.
	for i := 0; i < 20000; i++ {
		a.Insert(a.Len(), "x")
	}
	b.Apply(a.Since(b.StateVector()))
	if b.String() != a.String() {
		t.Fatal("first sync did not bring b up to date")
	}

	full, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// A small edit afterwards.
	a.Insert(a.Len(), "one more sentence.")

	update := a.Since(b.StateVector())
	incremental, err := json.Marshal(update)
	if err != nil {
		t.Fatalf("marshal update: %v", err)
	}

	b.Apply(update)
	if b.String() != a.String() {
		t.Fatalf("incremental sync left the replicas different")
	}

	if len(incremental) >= len(full)/10 {
		t.Errorf("an 18-character edit sent %d bytes against %d for the whole document; "+
			"it should be a small fraction", len(incremental), len(full))
	}
	t.Logf("whole document %d bytes, incremental update %d bytes", len(full), len(incremental))
}

// Nothing to send when the peer is already up to date.
func TestSyncEmptyWhenCaughtUp(t *testing.T) {
	a := NewDocument(hlc.NewClock("a"))
	a.Insert(0, "some text")

	b := NewDocument(hlc.NewClock("b"))
	b.Apply(a.Since(b.StateVector()))

	if u := a.Since(b.StateVector()); len(u.Runs) != 0 {
		t.Errorf("a caught-up peer was sent %d runs", len(u.Runs))
	}
}

// Deletions must travel even though the peer already has the characters.
func TestSyncPropagatesDeletions(t *testing.T) {
	a := NewDocument(hlc.NewClock("a"))
	b := NewDocument(hlc.NewClock("b"))

	a.Insert(0, "hello cruel world")
	b.Apply(a.Since(b.StateVector()))
	if b.String() != "hello cruel world" {
		t.Fatalf("b = %q", b.String())
	}

	a.Delete(6, 6) // "cruel "
	b.Apply(a.Since(b.StateVector()))

	if a.String() != "hello world" {
		t.Fatalf("a = %q", a.String())
	}
	if b.String() != a.String() {
		t.Fatalf("deletion did not reach b: %q vs %q", b.String(), a.String())
	}
}

// A deletion covering part of a run must split it rather than removing too much.
func TestSyncPartialDeletion(t *testing.T) {
	a := NewDocument(hlc.NewClock("a"))
	b := NewDocument(hlc.NewClock("b"))

	a.Insert(0, "abcdefghij")
	b.Apply(a.Since(b.StateVector()))

	a.Delete(3, 4) // "defg"
	b.Apply(a.Since(b.StateVector()))

	if a.String() != "abchij" {
		t.Fatalf("a = %q, want %q", a.String(), "abchij")
	}
	if b.String() != a.String() {
		t.Fatalf("b = %q, want %q", b.String(), a.String())
	}
}

// Applying the same update twice must change nothing.
func TestSyncIsIdempotent(t *testing.T) {
	a := NewDocument(hlc.NewClock("a"))
	b := NewDocument(hlc.NewClock("b"))

	a.Insert(0, "hello")
	a.Delete(1, 1)

	u := a.Since(b.StateVector())
	b.Apply(u)
	once := b.String()
	b.Apply(u)
	b.Apply(u)

	if b.String() != once {
		t.Errorf("reapplying an update changed the document: %q then %q", once, b.String())
	}
	if b.String() != a.String() {
		t.Errorf("b = %q, want %q", b.String(), a.String())
	}
}

// Two replicas editing while apart, syncing in both directions, must converge —
// and must reach what a full merge would have produced.
func TestSyncConvergesLikeAFullMerge(t *testing.T) {
	for seed := int64(0); seed < 100; seed++ {
		rng := rand.New(rand.NewSource(seed))

		a := NewDocument(hlc.NewClock("a"))
		b := NewDocument(hlc.NewClock("b"))
		a.Insert(0, "shared starting text")
		b.Apply(a.Since(b.StateVector()))

		for i := 0; i < 6; i++ {
			a.Insert(rng.Intn(a.Len()+1), "A")
			b.Insert(rng.Intn(b.Len()+1), "B")
			if a.Len() > 4 && rng.Intn(3) == 0 {
				a.Delete(rng.Intn(a.Len()-1), 1)
			}
			if b.Len() > 4 && rng.Intn(3) == 0 {
				b.Delete(rng.Intn(b.Len()-1), 1)
			}
		}

		// Snapshot what a full-state merge would give, before syncing.
		viaMerge := MergeTextRuns(a.Text(), b.Text())
		expected := NewDocument(hlc.NewClock("x"))
		expected.reset(markDeleted(viaMerge, append(deletionsOf(a.Text()), deletionsOf(b.Text())...)))

		// Now sync incrementally, both ways.
		fromA := a.Since(b.StateVector())
		fromB := b.Since(a.StateVector())
		a.Apply(fromB)
		b.Apply(fromA)

		if a.String() != b.String() {
			t.Fatalf("seed %d: replicas diverged after syncing\n  a %q\n  b %q",
				seed, a.String(), b.String())
		}
		if a.String() != expected.String() {
			t.Fatalf("seed %d: incremental sync gave %q, a full merge gives %q",
				seed, a.String(), expected.String())
		}
	}
}

// deletionsOf collects the deleted ranges of a run list, for the comparison above.
func deletionsOf(t Text) []DeletedRange {
	var out []DeletedRange
	for _, run := range t {
		if run.Deleted {
			out = append(out, DeletedRange{ID: run.ID, N: int32(run.runeCount())})
		}
	}
	return out
}

// Three replicas, syncing in a ring, must all agree.
func TestSyncThreeReplicas(t *testing.T) {
	a := NewDocument(hlc.NewClock("a"))
	b := NewDocument(hlc.NewClock("b"))
	c := NewDocument(hlc.NewClock("c"))

	a.Insert(0, "base")
	b.Apply(a.Since(b.StateVector()))
	c.Apply(a.Since(c.StateVector()))

	a.Insert(4, "-A")
	b.Insert(4, "-B")
	c.Insert(0, "C-")

	// Ring: a -> b -> c -> a, twice round so everything propagates.
	for round := 0; round < 2; round++ {
		b.Apply(a.Since(b.StateVector()))
		c.Apply(b.Since(c.StateVector()))
		a.Apply(c.Since(a.StateVector()))
	}

	if a.String() != b.String() || b.String() != c.String() {
		t.Fatalf("three replicas disagree:\n  a %q\n  b %q\n  c %q",
			a.String(), b.String(), c.String())
	}
	for _, want := range []string{"A", "B", "C"} {
		if !contains(a.String(), want) {
			t.Errorf("lost %q: %q", want, a.String())
		}
	}
}

func contains(h, n string) bool {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return true
		}
	}
	return false
}

func TestStateVectorIsSmall(t *testing.T) {
	a := NewDocument(hlc.NewClock("a"))
	for i := 0; i < 5000; i++ {
		a.Insert(a.Len(), "x")
	}
	sv := a.StateVector()
	if len(sv) != 1 {
		t.Errorf("one writer produced %d state vector entries, want 1", len(sv))
	}
	data, err := json.Marshal(sv)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if len(data) > 100 {
		t.Errorf("state vector for a 5000-character document is %d bytes", len(data))
	}
}
