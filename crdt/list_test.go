package crdt

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"reflect"
	"testing"

	deep "github.com/brunoga/deep/v5"
	"github.com/brunoga/deep/v5/crdt/hlc"
	"sort"
)

func TestListBasicOperations(t *testing.T) {
	clock := hlc.NewClock("a")
	l := NewList(clock, "a", "b", "c")

	if got := l.Items(); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Fatalf("Items = %v, want [a b c]", got)
	}
	if l.Len() != 3 {
		t.Errorf("Len = %d, want 3", l.Len())
	}

	l = l.Insert(1, "x", clock)
	if got := l.Items(); !reflect.DeepEqual(got, []string{"a", "x", "b", "c"}) {
		t.Fatalf("after Insert at 1: %v", got)
	}

	l = l.Insert(0, "head", clock)
	l = l.Insert(l.Len(), "tail", clock)
	if got := l.Items(); !reflect.DeepEqual(got, []string{"head", "a", "x", "b", "c", "tail"}) {
		t.Fatalf("after head/tail inserts: %v", got)
	}

	l = l.Delete(1, 2) // remove "a" and "x"
	if got := l.Items(); !reflect.DeepEqual(got, []string{"head", "b", "c", "tail"}) {
		t.Fatalf("after Delete: %v", got)
	}
	if v, ok := l.At(1); !ok || v != "b" {
		t.Errorf("At(1) = %q %v, want b true", v, ok)
	}
	if _, ok := l.At(99); ok {
		t.Error("At past the end reported ok")
	}
}

// Concurrent insertions at the same position must survive on both replicas and
// land in the same order — the case an ordinary slice cannot express.
func TestListConcurrentInsertSamePosition(t *testing.T) {
	ca, cb := hlc.NewClock("a"), hlc.NewClock("b")
	base := NewList(ca, "start", "end")

	docA := base.Insert(1, "from-a", ca)
	docB := base.Insert(1, "from-b", cb)

	mergedA := MergeLists(docA, docB)
	mergedB := MergeLists(docB, docA)

	if !reflect.DeepEqual(mergedA.Items(), mergedB.Items()) {
		t.Fatalf("diverged: %v vs %v", mergedA.Items(), mergedB.Items())
	}
	if mergedA.Len() != 4 {
		t.Fatalf("expected both insertions to survive, got %v", mergedA.Items())
	}
}

// A deletion on one replica and an insertion anchored to the deleted element on
// another must both hold: the element goes, the new neighbour stays.
func TestListConcurrentDeleteAndInsert(t *testing.T) {
	ca, cb := hlc.NewClock("a"), hlc.NewClock("b")
	base := NewList(ca, "one", "two", "three")

	docA := base.Delete(1, 1)              // remove "two"
	docB := base.Insert(2, "inserted", cb) // insert after "two"

	mergedA := MergeLists(docA, docB)
	mergedB := MergeLists(docB, docA)

	if !reflect.DeepEqual(mergedA.Items(), mergedB.Items()) {
		t.Fatalf("diverged: %v vs %v", mergedA.Items(), mergedB.Items())
	}
	want := []string{"one", "inserted", "three"}
	if !reflect.DeepEqual(mergedA.Items(), want) {
		t.Errorf("got %v, want %v", mergedA.Items(), want)
	}
}

// Merging is commutative, associative and idempotent.
func TestListMergeProperties(t *testing.T) {
	ca, cb, cc := hlc.NewClock("a"), hlc.NewClock("b"), hlc.NewClock("c")
	base := NewList(ca, "seed")

	a := base.Insert(1, "a", ca)
	b := base.Insert(1, "b", cb)
	c := base.Insert(0, "c", cc)

	left := MergeLists(MergeLists(a, b), c)
	right := MergeLists(a, MergeLists(b, c))
	if !reflect.DeepEqual(left.Items(), right.Items()) {
		t.Errorf("not associative: %v vs %v", left.Items(), right.Items())
	}

	if !reflect.DeepEqual(MergeLists(a, b).Items(), MergeLists(b, a).Items()) {
		t.Error("not commutative")
	}

	once := MergeLists(a, b)
	twice := MergeLists(once, b)
	if !reflect.DeepEqual(once.Items(), twice.Items()) {
		t.Errorf("not idempotent: %v vs %v", once.Items(), twice.Items())
	}
}

// A List inside a CRDT[T] must merge rather than resolve by last-write-wins,
// which is what an ordinary slice in the same position would do.
func TestListInsideCRDT(t *testing.T) {
	type board struct {
		Title string       `json:"title"`
		Tasks List[string] `json:"tasks"`
	}

	a := NewCRDT(board{Title: "Sprint"}, "a")
	b := NewCRDT(board{Title: "Sprint"}, "b")

	seed := a.Edit(func(v *board) { v.Tasks = v.Tasks.Insert(0, "seed", a.Clock()) })
	b.ApplyDelta(seed)

	da := a.Edit(func(v *board) { v.Tasks = v.Tasks.Insert(1, "from-a", a.Clock()) })
	db := b.Edit(func(v *board) { v.Tasks = v.Tasks.Insert(1, "from-b", b.Clock()) })

	a.ApplyDelta(db)
	b.ApplyDelta(da)

	va, vb := a.View(), b.View()
	if !reflect.DeepEqual(va.Tasks.Items(), vb.Tasks.Items()) {
		t.Fatalf("diverged: %v vs %v", va.Tasks.Items(), vb.Tasks.Items())
	}
	if va.Tasks.Len() != 3 {
		t.Fatalf("expected both insertions to survive, got %v", va.Tasks.Items())
	}
}

// Several replicas, concurrent edits, delivered in a different order to each,
// with redelivery.
func TestListRandomizedConvergence(t *testing.T) {
	type doc struct {
		Items List[string] `json:"items"`
	}

	for seed := int64(0); seed < 20; seed++ {
		rng := rand.New(rand.NewSource(seed))

		nodes := make([]*CRDT[doc], 4)
		for i := range nodes {
			nodes[i] = NewCRDT(doc{}, fmt.Sprintf("n%d", i))
		}
		seedDelta := nodes[0].Edit(func(d *doc) {
			d.Items = d.Items.Insert(0, "seed", nodes[0].Clock())
		})
		for _, n := range nodes[1:] {
			n.ApplyDelta(seedDelta)
		}

		var deltas []Delta[doc]
		for i, n := range nodes {
			label := fmt.Sprintf("item-%d", i)
			node := n
			if rng.Intn(4) == 0 {
				deltas = append(deltas, node.Edit(func(d *doc) {
					if d.Items.Len() > 0 {
						d.Items = d.Items.Delete(rng.Intn(d.Items.Len()), 1)
					}
				}))
				continue
			}
			pos := rng.Intn(node.View().Items.Len() + 1)
			deltas = append(deltas, node.Edit(func(d *doc) {
				d.Items = d.Items.Insert(pos, label, node.Clock())
			}))
		}

		for _, n := range nodes {
			for _, idx := range rng.Perm(len(deltas)) {
				n.ApplyDelta(deltas[idx])
			}
			n.ApplyDelta(deltas[rng.Intn(len(deltas))]) // redelivery
		}

		want := nodes[0].View().Items.Items()
		for i, n := range nodes[1:] {
			if got := n.View().Items.Items(); !reflect.DeepEqual(got, want) {
				t.Fatalf("seed %d: replica %d diverged\n  want %v\n  got  %v", seed, i+1, want, got)
			}
		}
	}
}

// A List must survive a JSON round-trip, including through a Delta.
func TestListJSONRoundTrip(t *testing.T) {
	clock := hlc.NewClock("a")
	l := NewList(clock, "one", "two", "three").Delete(1, 1)

	data, err := json.Marshal(l)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back List[string]
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(back.Items(), l.Items()) {
		t.Errorf("round-trip: %v, want %v", back.Items(), l.Items())
	}

	type doc struct {
		Items List[string] `json:"items"`
	}
	a := NewCRDT(doc{}, "a")
	b := NewCRDT(doc{}, "b")
	delta := a.Edit(func(d *doc) { d.Items = d.Items.Insert(0, "hello", a.Clock()) })

	wire, err := json.Marshal(delta)
	if err != nil {
		t.Fatalf("marshal delta: %v", err)
	}
	var received Delta[doc]
	if err := json.Unmarshal(wire, &received); err != nil {
		t.Fatalf("unmarshal delta: %v", err)
	}
	if !b.ApplyDelta(received) {
		t.Fatal("delta from JSON was not applied")
	}
	if got := b.View().Items.Items(); !reflect.DeepEqual(got, []string{"hello"}) {
		t.Errorf("after applying decoded delta: %v", got)
	}
	_ = deep.Equal[doc]
}

// A user-defined type implementing Convergent must get the same treatment as
// the built-in ones: applied unconditionally, merged rather than overwritten.
type growOnly []string

func (g growOnly) MergeFrom(other any) any {
	o, ok := other.(growOnly)
	if !ok {
		return g
	}
	seen := make(map[string]bool, len(g)+len(o))
	var out growOnly
	for _, s := range append(append(growOnly{}, g...), o...) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

func TestCustomConvergentType(t *testing.T) {
	type doc struct {
		Tags growOnly `json:"tags"`
	}

	a := NewCRDT(doc{}, "a")
	b := NewCRDT(doc{}, "b")

	da := a.Edit(func(d *doc) { d.Tags = growOnly{"alpha"} })
	db := b.Edit(func(d *doc) { d.Tags = growOnly{"beta"} })

	a.Merge(b)
	b.Merge(a)
	_ = da
	_ = db

	va, vb := a.View(), b.View()
	if !reflect.DeepEqual(va.Tags, vb.Tags) {
		t.Fatalf("diverged: %v vs %v", va.Tags, vb.Tags)
	}
	if len(va.Tags) != 2 {
		t.Errorf("expected both tags to survive the merge, got %v", va.Tags)
	}
}

// A type of the caller's own that implements Convergent must merge when a
// delta arrives, not only when two replicas are merged wholesale. It skips the
// clock filter because it settles concurrency itself; if the delta then
// overwrote it instead of merging, a concurrent edit would be lost and the two
// replicas would disagree about which — worse than either behaviour alone.
func TestCustomConvergentTypeThroughDelta(t *testing.T) {
	type doc struct {
		Tags  growOnly `json:"tags"`
		Title string   `json:"title"`
	}

	a := NewCRDT(doc{}, "a")
	b := NewCRDT(doc{}, "b")

	fromA := a.Edit(func(d *doc) {
		d.Tags = growOnly{"alpha"}
		d.Title = "from a"
	})
	fromB := b.Edit(func(d *doc) {
		d.Tags = growOnly{"beta"}
		d.Title = "from b"
	})

	a.ApplyDelta(fromB)
	b.ApplyDelta(fromA)

	if !deep.Equal(a.View(), b.View()) {
		t.Fatalf("replicas diverged:\n  a %+v\n  b %+v", a.View(), b.View())
	}
	if got := a.View().Tags; len(got) != 2 {
		t.Errorf("expected both tags to survive, got %v", got)
	}
	// The ordinary field is still last-write-wins, which is the contrast.
	if a.View().Title == "" {
		t.Error("the plain field lost its value entirely")
	}
}

// The same type must also survive a delta that has been through JSON.
func TestCustomConvergentTypeThroughJSON(t *testing.T) {
	type doc struct {
		Tags growOnly `json:"tags"`
	}

	a := NewCRDT(doc{}, "a")
	b := NewCRDT(doc{Tags: growOnly{"existing"}}, "b")

	delta := a.Edit(func(d *doc) { d.Tags = growOnly{"added"} })

	wire, err := json.Marshal(delta)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var received Delta[doc]
	if err := json.Unmarshal(wire, &received); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	b.ApplyDelta(received)

	got := b.View().Tags
	if len(got) != 2 {
		t.Errorf("after a delta through JSON: %v, want both tags", got)
	}
}
