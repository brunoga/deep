package crdt

import (
	"encoding/json"
	"testing"

	deep "github.com/brunoga/deep/v5"
	"github.com/brunoga/deep/v5/crdt/hlc"
)

type compactDoc struct {
	Data map[string]int `json:"data"`
}

// A replica that has emptied a map should not keep carrying an entry for every
// key it ever held.
func TestCompactReclaimsMetadata(t *testing.T) {
	c := NewCRDT(compactDoc{Data: map[string]int{}}, "a")

	for i := 0; i < 200; i++ {
		k := string(rune('a' + i%26))
		c.Edit(func(d *compactDoc) { d.Data[k] = i })
		c.Edit(func(d *compactDoc) { delete(d.Data, k) })
	}

	before, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// Everything is in the past by the time the clock reads again.
	dropped := c.Compact(c.Clock().Now())
	if dropped == 0 {
		t.Fatal("compaction dropped nothing")
	}

	after, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if len(after) >= len(before) {
		t.Errorf("state did not shrink: %d bytes before, %d after", len(before), len(after))
	}
	if len(c.View().Data) != 0 {
		t.Errorf("compaction changed the value: %v", c.View().Data)
	}
}

// Compacting must not change what the replica holds.
func TestCompactPreservesValue(t *testing.T) {
	c := NewCRDT(compactDoc{Data: map[string]int{}}, "a")
	c.Edit(func(d *compactDoc) { d.Data["keep"] = 1 })
	c.Edit(func(d *compactDoc) { d.Data["also"] = 2 })

	want := c.View()
	c.Compact(c.Clock().Now())

	if !deep.Equal(c.View(), want) {
		t.Errorf("value changed: %v, want %v", c.View().Data, want.Data)
	}
}

// A replica that has compacted must still converge with one that has not.
func TestCompactedReplicaStillConverges(t *testing.T) {
	a := NewCRDT(compactDoc{Data: map[string]int{}}, "a")
	b := NewCRDT(compactDoc{Data: map[string]int{}}, "b")

	seed := a.Edit(func(d *compactDoc) { d.Data["x"] = 1 })
	b.ApplyDelta(seed)

	// a compacts; b does not.
	a.Compact(a.Clock().Now())

	da := a.Edit(func(d *compactDoc) { d.Data["from-a"] = 2 })
	db := b.Edit(func(d *compactDoc) { d.Data["from-b"] = 3 })

	a.ApplyDelta(db)
	b.ApplyDelta(da)

	if !deep.Equal(a.View(), b.View()) {
		t.Fatalf("diverged: %v vs %v", a.View().Data, b.View().Data)
	}
	if len(a.View().Data) != 3 {
		t.Errorf("expected all three keys, got %v", a.View().Data)
	}
}

// Deleted text should become reclaimable once every replica has seen the
// deletion.
func TestTextCompactDropsTombstones(t *testing.T) {
	clock := hlc.NewClock("a")

	doc := Text{}.Insert(0, "keep this", clock)
	for i := 0; i < 50; i++ {
		doc = doc.Insert(0, "temporary ", clock)
		doc = doc.Delete(0, len("temporary "))
	}

	if len(doc) < 10 {
		t.Fatalf("expected tombstones to have accumulated, got %d runs", len(doc))
	}
	content := doc.String()

	compacted := doc.Compact(clock.Now())
	if compacted.String() != content {
		t.Fatalf("compaction changed the text: %q, want %q", compacted.String(), content)
	}
	if len(compacted) >= len(doc) {
		t.Errorf("no tombstones dropped: %d runs before, %d after", len(doc), len(compacted))
	}
}

// A tombstone that something is anchored to must survive compaction, or the
// runs anchored to it lose their place.
func TestTextCompactKeepsAnchoringTombstones(t *testing.T) {
	ca, cb := hlc.NewClock("a"), hlc.NewClock("b")

	base := Text{}.Insert(0, "one two three", ca)
	// One replica deletes the middle word; another concurrently inserts inside
	// it, anchoring to what is about to become a tombstone.
	deleted := base.Delete(4, 4)
	inserted := base.Insert(6, "X", cb)

	merged := MergeTextRuns(deleted, inserted)
	want := merged.String()

	compacted := merged.Compact(ca.Now())
	if got := compacted.String(); got != want {
		t.Errorf("compaction changed the text: %q, want %q", got, want)
	}

	// Compacting either way round must agree, and merging a compacted copy with
	// an uncompacted one must not disturb the order.
	remerged := MergeTextRuns(compacted, merged)
	if got := remerged.String(); got != want {
		t.Errorf("merging a compacted copy changed the text: %q, want %q", got, want)
	}
}

// Compaction must respect the watermark: a deletion newer than it stays.
func TestTextCompactRespectsWatermark(t *testing.T) {
	clock := hlc.NewClock("a")

	watermark := clock.Now()
	doc := Text{}.Insert(0, "hello world", clock) // after the watermark
	doc = doc.Delete(0, 6)

	compacted := doc.Compact(watermark)
	if len(compacted) != len(doc) {
		t.Errorf("dropped a tombstone newer than the watermark: %d runs became %d",
			len(doc), len(compacted))
	}
	if compacted.String() != "world" {
		t.Errorf("content = %q, want %q", compacted.String(), "world")
	}
}

// The property compaction must not break: replicas that compact at different
// moments, or not at all, still agree. Compaction is only safe for changes
// everyone has seen, so the watermark here is taken after the replicas have
// exchanged everything — which is exactly the contract the API states.
func TestCompactionPreservesConvergence(t *testing.T) {
	for seed := 0; seed < 20; seed++ {
		a := NewCRDT(compactDoc{Data: map[string]int{}}, "a")
		b := NewCRDT(compactDoc{Data: map[string]int{}}, "b")
		c := NewCRDT(compactDoc{Data: map[string]int{}}, "c")

		var deltas []Delta[compactDoc]
		for round := 0; round < 5; round++ {
			for i, node := range []*CRDT[compactDoc]{a, b, c} {
				key := string(rune('a' + (round*3+i)%7))
				n := node
				if (round+i)%3 == 0 {
					deltas = append(deltas, n.Edit(func(d *compactDoc) { delete(d.Data, key) }))
				} else {
					deltas = append(deltas, n.Edit(func(d *compactDoc) { d.Data[key] = round*10 + i }))
				}
			}
		}

		// Everyone sees everything.
		for _, node := range []*CRDT[compactDoc]{a, b, c} {
			for _, d := range deltas {
				node.ApplyDelta(d)
			}
		}

		// Now that every delta has been delivered, compacting up to this point
		// is safe. Two replicas do it; the third never does.
		watermark := a.Clock().Now()
		a.Compact(watermark)
		b.Compact(watermark)

		if !deep.Equal(a.View(), c.View()) {
			t.Fatalf("seed %d: compacted replica diverged from uncompacted: %v vs %v",
				seed, a.View().Data, c.View().Data)
		}
		if !deep.Equal(a.View(), b.View()) {
			t.Fatalf("seed %d: two compacted replicas diverged", seed)
		}

		// Editing continues afterwards and must still converge.
		post := a.Edit(func(d *compactDoc) { d.Data["after"] = 1 })
		b.ApplyDelta(post)
		c.ApplyDelta(post)
		if !deep.Equal(a.View(), b.View()) || !deep.Equal(a.View(), c.View()) {
			t.Fatalf("seed %d: diverged after compaction: %v / %v / %v",
				seed, a.View().Data, b.View().Data, c.View().Data)
		}
	}
}

// The same property for text: compacted and uncompacted copies still merge to
// the same document.
func TestTextCompactionPreservesConvergence(t *testing.T) {
	ca, cb := hlc.NewClock("a"), hlc.NewClock("b")

	base := Text{}.Insert(0, "the quick brown fox", ca)
	docA := base.Delete(4, 6).Insert(4, "slow ", ca)
	docB := base.Insert(19, " jumps", cb)

	merged := MergeTextRuns(docA, docB)
	want := merged.String()

	watermark := ca.Now()
	compacted := merged.Compact(watermark)
	if got := compacted.String(); got != want {
		t.Fatalf("compaction changed the text: %q, want %q", got, want)
	}

	// A compacted copy merged with an uncompacted one, both ways round.
	if got := MergeTextRuns(compacted, merged).String(); got != want {
		t.Errorf("compacted merged with uncompacted = %q, want %q", got, want)
	}
	if got := MergeTextRuns(merged, compacted).String(); got != want {
		t.Errorf("uncompacted merged with compacted = %q, want %q", got, want)
	}

	// Editing a compacted document still works and still merges.
	edited := compacted.Insert(compacted.Len(), " today", ca)
	if got := MergeTextRuns(edited, merged).String(); got != edited.String() {
		t.Errorf("merging an edited compacted document = %q, want %q", got, edited.String())
	}
}

type compactNested struct {
	Notes Text         `json:"notes"`
	Items List[string] `json:"items"`
}

// Compacting a replica must reach the sequences inside its value, and must not
// announce anything: dropping history does not change what the replica holds,
// so there is nothing for peers to apply.
func TestCompactReachesSequencesWithoutEmittingADelta(t *testing.T) {
	c := NewCRDT(compactNested{}, "a")

	c.Edit(func(d *compactNested) {
		d.Notes = d.Notes.Insert(0, "keep", c.Clock())
		d.Items = d.Items.Insert(0, "keep", c.Clock())
	})
	for i := 0; i < 20; i++ {
		c.Edit(func(d *compactNested) {
			d.Notes = d.Notes.Insert(0, "scratch ", c.Clock())
			d.Notes = d.Notes.Delete(0, len("scratch "))
			d.Items = d.Items.Insert(0, "scratch", c.Clock())
			d.Items = d.Items.Delete(0, 1)
		})
	}

	notesBefore, itemsBefore := len(c.View().Notes), len(c.View().Items)
	if notesBefore < 5 || itemsBefore < 5 {
		t.Fatalf("expected tombstones to accumulate, got %d runs and %d entries",
			notesBefore, itemsBefore)
	}

	announced := 0
	c.OnChange(func(Change[compactNested]) { announced++ })

	c.Compact(c.Clock().Now())

	if announced != 0 {
		t.Errorf("compaction announced %d changes; it should be silent", announced)
	}
	if got := c.View().Notes.String(); got != "keep" {
		t.Errorf("text content = %q, want %q", got, "keep")
	}
	if got := c.View().Items.Items(); len(got) != 1 || got[0] != "keep" {
		t.Errorf("list content = %v, want [keep]", got)
	}
	if n := len(c.View().Notes); n >= notesBefore {
		t.Errorf("text tombstones not reclaimed: %d runs before, %d after", notesBefore, n)
	}
	if n := len(c.View().Items); n >= itemsBefore {
		t.Errorf("list tombstones not reclaimed: %d entries before, %d after", itemsBefore, n)
	}
}
