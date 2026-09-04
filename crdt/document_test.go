package crdt

import (
	"encoding/json"
	"math/rand"
	"testing"
	"unicode/utf8"

	"github.com/brunoga/deep/v6/crdt/hlc"
	"strings"
	"time"
)

func TestDocumentBasicEditing(t *testing.T) {
	d := NewDocument(hlc.NewClock("a"))

	d.Insert(0, "world")
	d.Insert(0, "hello ")
	if got := d.String(); got != "hello world" {
		t.Fatalf("String = %q, want %q", got, "hello world")
	}
	if d.Len() != 11 {
		t.Errorf("Len = %d, want 11", d.Len())
	}

	d.Insert(5, ",")
	if got := d.String(); got != "hello, world" {
		t.Fatalf("after insert = %q", got)
	}

	d.Delete(5, 1)
	if got := d.String(); got != "hello world" {
		t.Fatalf("after delete = %q", got)
	}

	d.Delete(0, d.Len())
	if got := d.String(); got != "" {
		t.Fatalf("after deleting everything = %q", got)
	}

	// Editing past the ends is clamped rather than panicking.
	d.Insert(1000, "tail")
	d.Insert(-5, "head ")
	d.Delete(100, 5)
	if got := d.String(); got != "head tail" {
		t.Fatalf("after clamped edits = %q", got)
	}
}

func TestDocumentUnicode(t *testing.T) {
	d := NewDocument(hlc.NewClock("a"))
	d.Insert(0, "héllo wörld")
	d.Insert(2, "😀")
	if got := d.String(); got != "hé😀llo wörld" {
		t.Fatalf("String = %q", got)
	}
	if !utf8.ValidString(d.String()) {
		t.Fatal("produced invalid UTF-8")
	}
	d.Delete(2, 1)
	if got := d.String(); got != "héllo wörld" {
		t.Fatalf("after deleting the emoji = %q", got)
	}
	if d.Len() != 11 {
		t.Errorf("Len = %d, want 11 runes", d.Len())
	}
}

// Document and Text describe the same thing, so the same edits must produce the
// same document. Text is the proven implementation, so it is the reference.
func TestDocumentMatchesText(t *testing.T) {
	for seed := int64(0); seed < 200; seed++ {
		rng := rand.New(rand.NewSource(seed))

		// The two must draw identifiers from clocks that behave the same, but
		// the identifiers themselves need not match; only the text does.
		doc := NewDocument(hlc.NewClock("a"))
		txt := Text{}
		txtClock := hlc.NewClock("a")

		words := []string{"a", "bc", "def", "héllo", "😀", "日本"}
		for step := 0; step < 30; step++ {
			switch rng.Intn(3) {
			case 0, 1:
				pos := 0
				if n := doc.Len(); n > 0 {
					pos = rng.Intn(n + 1)
				}
				w := words[rng.Intn(len(words))]
				doc.Insert(pos, w)
				txt = txt.Insert(pos, w, txtClock)
			case 2:
				if n := doc.Len(); n > 1 {
					pos := rng.Intn(n - 1)
					count := 1 + rng.Intn(n-pos-1)
					doc.Delete(pos, count)
					txt = txt.Delete(pos, count)
				}
			}

			if doc.String() != txt.String() {
				t.Fatalf("seed %d step %d: document and text disagree\n  document %q\n  text     %q",
					seed, step, doc.String(), txt.String())
			}
			if doc.Len() != txt.Len() {
				t.Fatalf("seed %d step %d: Len %d vs %d", seed, step, doc.Len(), txt.Len())
			}
		}
	}
}

// A document merges with another the way two texts do, and converges.
func TestDocumentConvergence(t *testing.T) {
	for seed := int64(0); seed < 50; seed++ {
		rng := rand.New(rand.NewSource(seed))

		base := NewDocument(hlc.NewClock("a"))
		base.Insert(0, "shared starting text")

		a := NewDocument(hlc.NewClock("a"))
		a.MergeFrom(base.Text())
		b := NewDocument(hlc.NewClock("b"))
		b.MergeFrom(base.Text())

		for i := 0; i < 5; i++ {
			a.Insert(rng.Intn(a.Len()+1), "A")
			b.Insert(rng.Intn(b.Len()+1), "B")
		}

		mergedA := NewDocument(hlc.NewClock("a"))
		mergedA.MergeFrom(a.Text())
		mergedA.MergeFrom(b.Text())

		mergedB := NewDocument(hlc.NewClock("b"))
		mergedB.MergeFrom(b.Text())
		mergedB.MergeFrom(a.Text())

		if mergedA.String() != mergedB.String() {
			t.Fatalf("seed %d diverged:\n  %q\n  %q", seed, mergedA.String(), mergedB.String())
		}

		// And it must agree with what two Texts would have produced.
		viaText := MergeTextRuns(a.Text(), b.Text())
		if mergedA.String() != viaText.String() {
			t.Fatalf("seed %d: document merge %q, text merge %q",
				seed, mergedA.String(), viaText.String())
		}
	}
}

// A Document and a Text hold the same runs, so one can be handed to the other.
func TestDocumentTextInterop(t *testing.T) {
	clock := hlc.NewClock("a")
	txt := Text{}.Insert(0, "from a text", clock)

	doc := DocumentFromText(txt, clock)
	if got := doc.String(); got != txt.String() {
		t.Fatalf("DocumentFromText = %q, want %q", got, txt.String())
	}

	doc.Insert(doc.Len(), " and a document")
	back := doc.Text()
	if back.String() != doc.String() {
		t.Fatalf("Text() = %q, want %q", back.String(), doc.String())
	}

	// A text can merge a document's runs and reach the same place.
	merged := MergeTextRuns(txt, doc.Text())
	if merged.String() != doc.String() {
		t.Errorf("text merged with document runs = %q, want %q", merged.String(), doc.String())
	}
}

func TestDocumentJSONRoundTrip(t *testing.T) {
	d := NewDocument(hlc.NewClock("a"))
	d.Insert(0, "round trip me")
	d.Delete(5, 5)

	data, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back Document
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.String() != d.String() {
		t.Errorf("round-trip = %q, want %q", back.String(), d.String())
	}
	if back.Len() != d.Len() {
		t.Errorf("round-trip Len = %d, want %d", back.Len(), d.Len())
	}

	// The wire form is a Text, so a Text reads it too.
	var asText Text
	if err := json.Unmarshal(data, &asText); err != nil {
		t.Fatalf("unmarshal as text: %v", err)
	}
	if asText.String() != d.String() {
		t.Errorf("read as text = %q, want %q", asText.String(), d.String())
	}
}

// Typing must still collapse into runs rather than leaving one per keystroke.
func TestDocumentRunsMerge(t *testing.T) {
	d := NewDocument(hlc.NewClock("a"))
	for _, r := range "hello world" {
		d.Insert(d.Len(), string(r))
	}
	if got := d.String(); got != "hello world" {
		t.Fatalf("String = %q", got)
	}
	if runs := len(d.Text()); runs > 2 {
		t.Errorf("typing 11 characters left %d runs, want them merged", runs)
	}
}

func TestDocumentCompact(t *testing.T) {
	clock := hlc.NewClock("a")
	d := NewDocument(clock)
	d.Insert(0, "keep this")
	for i := 0; i < 30; i++ {
		d.Insert(0, "scratch ")
		d.Delete(0, len("scratch "))
	}

	before := len(d.Text())
	d.Compact(clock.Now())
	if d.String() != "keep this" {
		t.Fatalf("compaction changed the text: %q", d.String())
	}
	if after := len(d.Text()); after >= before {
		t.Errorf("no tombstones reclaimed: %d runs before, %d after", before, after)
	}
}

func TestDocumentInsideCRDT(t *testing.T) {
	type article struct {
		Body *Document `json:"body"`
	}

	a := NewCRDT(article{Body: NewDocument(hlc.NewClock("a"))}, "a")
	b := NewCRDT(article{Body: NewDocument(hlc.NewClock("b"))}, "b")

	da := a.Edit(func(v *article) { v.Body.Insert(0, "hello") })
	b.ApplyDelta(da)
	if got := b.View().Body.String(); got != "hello" {
		t.Fatalf("after applying the delta = %q, want %q", got, "hello")
	}

	d1 := a.Edit(func(v *article) { v.Body.Insert(5, " world") })
	d2 := b.Edit(func(v *article) { v.Body.Insert(0, ">> ") })
	a.ApplyDelta(d2)
	b.ApplyDelta(d1)

	if a.View().Body.String() != b.View().Body.String() {
		t.Fatalf("diverged inside a CRDT:\n  a %q\n  b %q",
			a.View().Body.String(), b.View().Body.String())
	}
	if got := a.View().Body.String(); len(got) == 0 {
		t.Error("content lost")
	}
	if a.View().Body.Len() != len([]rune(a.View().Body.String())) {
		t.Error("Len disagrees with the text it reports")
	}
}

// A document inside a CRDT must produce a delta the size of the edit, not the
// size of the document. The engine used to compare two indexes structurally to
// work out what changed, which was both slow and put the whole document into
// every delta.
func TestDocumentDeltaIsIncremental(t *testing.T) {
	type article struct {
		Body *Document `json:"body"`
	}

	a := NewCRDT(article{Body: NewDocument(hlc.NewClock("a"))}, "a")
	a.Edit(func(v *article) {
		for i := 0; i < 2000; i++ {
			v.Body.Insert(v.Body.Len(), "x")
		}
	})

	delta := a.Edit(func(v *article) { v.Body.Insert(v.Body.Len(), "y") })

	wire, err := json.Marshal(delta)
	if err != nil {
		t.Fatalf("marshal delta: %v", err)
	}
	whole, err := json.Marshal(a.View().Body)
	if err != nil {
		t.Fatalf("marshal document: %v", err)
	}
	// The one-operation delta carries fixed overhead — the operation kind is a
	// word, the clock a struct — so the ratio loosens as the document shrinks;
	// an eighth of a 2 KB document still fails if the delta stops being
	// incremental.
	if len(wire) >= len(whole)/8 {
		t.Errorf("a one-character edit produced a %d byte delta for a %d byte document",
			len(wire), len(whole))
	}

	// And it must still apply, including after a round-trip through JSON.
	b := NewCRDT(article{Body: NewDocument(hlc.NewClock("b"))}, "b")
	b.Edit(func(v *article) {
		for i := 0; i < 2000; i++ {
			v.Body.Insert(v.Body.Len(), "x")
		}
	})
	// Bring b up to date the honest way first.
	b.View()

	var received Delta[article]
	if err := json.Unmarshal(wire, &received); err != nil {
		t.Fatalf("unmarshal delta: %v", err)
	}
	c := NewCRDT(article{Body: NewDocument(hlc.NewClock("c"))}, "c")
	c.ApplyDelta(received)
	if got := c.View().Body.String(); got != "y" {
		t.Errorf("applying a decoded delta gave %q, want the inserted text", got)
	}
}

// Concurrent edits to a document inside a CRDT must both survive: the value
// settles concurrency itself, so its operations must not be filtered by the
// clock the way an ordinary field's are.
func TestDocumentInCRDTKeepsConcurrentEdits(t *testing.T) {
	type article struct {
		Body *Document `json:"body"`
	}

	a := NewCRDT(article{Body: NewDocument(hlc.NewClock("a"))}, "a")
	b := NewCRDT(article{Body: NewDocument(hlc.NewClock("b"))}, "b")

	seed := a.Edit(func(v *article) { v.Body.Insert(0, "base") })
	b.ApplyDelta(seed)

	// Both edit, a first, so a's delta is the older one when b receives it.
	da := a.Edit(func(v *article) { v.Body.Insert(4, "-A") })
	db := b.Edit(func(v *article) { v.Body.Insert(4, "-B") })

	a.ApplyDelta(db)
	b.ApplyDelta(da)

	if a.View().Body.String() != b.View().Body.String() {
		t.Fatalf("diverged:\n  a %q\n  b %q", a.View().Body.String(), b.View().Body.String())
	}
	got := a.View().Body.String()
	for _, want := range []string{"base", "-A", "-B"} {
		if !strings.Contains(got, want) {
			t.Errorf("lost %q from %q", want, got)
		}
	}
}

// Copying a document shares its tree rather than duplicating it, which is only
// safe because no operation changes a node in place. If one ever did, an edit
// to a copy would reach back into whatever it was copied from — so this checks
// both directions after every kind of edit, and checks that the copy really is
// shared rather than quietly duplicated.
func TestDocumentCopyIsIndependentAndShared(t *testing.T) {
	original := NewDocument(hlc.NewClock("a"))
	original.Insert(0, "the original text")
	before := original.String()

	copyOf, err := original.Copy()
	if err != nil {
		t.Fatalf("Copy: %v", err)
	}
	if copyOf.root != original.root {
		t.Error("Copy duplicated the tree instead of sharing it")
	}

	// Every kind of edit, on the copy.
	copyOf.Insert(0, "PREFIX ")
	copyOf.Insert(copyOf.Len(), " SUFFIX")
	copyOf.Insert(10, "-MIDDLE-")
	copyOf.Delete(2, 3)
	if original.String() != before {
		t.Errorf("editing the copy changed the original: %q, want %q", original.String(), before)
	}

	// And on the original, against a copy taken beforehand.
	second, _ := original.Copy()
	secondBefore := second.String()
	original.Insert(4, "X")
	original.Delete(0, 2)
	if second.String() != secondBefore {
		t.Errorf("editing the original changed an earlier copy: %q, want %q",
			second.String(), secondBefore)
	}

	// A merge into one must not disturb the other.
	third, _ := original.Copy()
	thirdBefore := third.String()
	other := NewDocument(hlc.NewClock("b"))
	other.Insert(0, "from elsewhere")
	original.MergeFrom(other.Text())
	if third.String() != thirdBefore {
		t.Errorf("merging into the original changed an earlier copy: %q, want %q",
			third.String(), thirdBefore)
	}
}

// The wrapper takes a copy of its value before every edit. With a shared tree
// that copy no longer grows with the document, so an edit costs what the edit
// costs rather than what the document weighs.
func TestDocumentInCRDTCostDoesNotGrowWithDocument(t *testing.T) {
	type article struct {
		Body *Document `json:"body"`
	}

	measure := func(runs int) time.Duration {
		c := NewCRDT(article{Body: NewDocument(hlc.NewClock("a"))}, "a")
		c.Edit(func(v *article) { v.Body.Insert(0, "seed") })
		for i := 0; i < runs; i++ {
			c.Edit(func(v *article) { v.Body.Insert(0, "x") })
		}
		start := time.Now()
		for i := 0; i < 50; i++ {
			c.Edit(func(v *article) { v.Body.Insert(v.Body.Len(), "y") })
		}
		return time.Since(start) / 50
	}

	small, large := measure(200), measure(3000)
	t.Logf("per edit: %s at 200 runs, %s at 3000", small, large)

	// Fifteen times the runs must not mean anything like fifteen times the cost.
	if large > small*5 {
		t.Errorf("cost grew with the document: %s per edit at 200 runs, %s at 3000", small, large)
	}
}

// A document describes an edit from its own record of what it changed, which
// is only right when the document being compared against is what it was copied
// from. Everything else has to fall back to comparing the two. Getting that
// wrong would drop content from a delta silently, so each case is checked by
// applying the delta and requiring it to reproduce the document.
func TestDocumentDiffFallsBackWhenRecordDoesNotApply(t *testing.T) {
	apply := func(t *testing.T, from, to *Document) string {
		t.Helper()
		patch, err := from.Diff(to)
		if err != nil {
			t.Fatalf("Diff: %v", err)
		}
		got, _ := from.Copy()
		if patch != nil {
			patch.Apply(&got)
		}
		return got.String()
	}

	t.Run("copied then edited", func(t *testing.T) {
		old := NewDocument(hlc.NewClock("a"))
		old.Insert(0, "hello")
		next, _ := old.Copy()
		next.Insert(next.Len(), " world")

		if got := apply(t, old, next); got != next.String() {
			t.Errorf("got %q, want %q", got, next.String())
		}
	})

	t.Run("copied then edited with a deletion", func(t *testing.T) {
		old := NewDocument(hlc.NewClock("a"))
		old.Insert(0, "hello cruel world")
		next, _ := old.Copy()
		next.Delete(5, 6)
		next.Insert(next.Len(), "!")

		if got := apply(t, old, next); got != next.String() {
			t.Errorf("got %q, want %q", got, next.String())
		}
	})

	t.Run("unrelated documents", func(t *testing.T) {
		old := NewDocument(hlc.NewClock("a"))
		old.Insert(0, "one")
		other := NewDocument(hlc.NewClock("b"))
		other.Insert(0, "two")

		// Nothing links these, so the record cannot describe the difference and
		// the comparison has to be done properly.
		if got := apply(t, old, other); !strings.Contains(got, "two") {
			t.Errorf("got %q, which lost the other document's content", got)
		}
	})

	t.Run("after merging", func(t *testing.T) {
		old := NewDocument(hlc.NewClock("a"))
		old.Insert(0, "base")

		peer := NewDocument(hlc.NewClock("b"))
		peer.Insert(0, "from a peer ")

		next, _ := old.Copy()
		next.MergeFrom(peer.Text()) // wholesale change, so the record is reset
		next.Insert(0, "then typed ")

		if got := apply(t, old, next); got != next.String() {
			t.Errorf("got %q, want %q", got, next.String())
		}
	})

	t.Run("two replicas, each with its own clock", func(t *testing.T) {
		// Two documents edited independently are two replicas, and each needs
		// its own clock: what one holds of the other is described by a single
		// bound per writer, which only means anything if each writer's output
		// goes to one document.
		old := NewDocument(hlc.NewClock("a"))
		old.Insert(0, "shared")

		other := NewDocument(hlc.NewClock("b"))
		other.MergeFrom(old.Text())
		other.Insert(other.Len(), " and more")
		old.Insert(0, "prefix ")

		got := apply(t, old, other)
		if !strings.Contains(got, "and more") {
			t.Errorf("got %q, which lost the other replica's edit", got)
		}
		if !strings.Contains(got, "prefix") {
			t.Errorf("got %q, which lost this replica's own edit", got)
		}
	})
}

// The record must survive a document being edited many times over, which is
// what a CRDT does: copy, edit, keep the copy, copy again.
func TestDocumentRepeatedEditRoundTrips(t *testing.T) {
	type article struct {
		Body *Document `json:"body"`
	}

	a := NewCRDT(article{Body: NewDocument(hlc.NewClock("a"))}, "a")
	b := NewCRDT(article{Body: NewDocument(hlc.NewClock("b"))}, "b")

	words := []string{"alpha ", "beta ", "gamma ", "delta "}
	for i, w := range words {
		word := w
		delta := a.Edit(func(v *article) { v.Body.Insert(v.Body.Len(), word) })
		b.ApplyDelta(delta)
		if got, want := b.View().Body.String(), a.View().Body.String(); got != want {
			t.Fatalf("after edit %d: b has %q, a has %q", i, got, want)
		}
	}

	// Deletions travel through the same path.
	delta := a.Edit(func(v *article) { v.Body.Delete(0, 6) })
	b.ApplyDelta(delta)
	if got, want := b.View().Body.String(), a.View().Body.String(); got != want {
		t.Fatalf("after a deletion: b has %q, a has %q", got, want)
	}
	if want := "beta gamma delta "; a.View().Body.String() != want {
		t.Errorf("document is %q, want %q", a.View().Body.String(), want)
	}
}

// After merging a peer's text in, a local insertion at the spot the merge
// touched must still land where every replica agrees it should. Runs sharing an
// anchor are ordered by identifier, so placing a new run at the cursor can put
// it before one that was merged in and sorts ahead of it — this replica would
// then read the document differently from everyone else.
func TestDocumentInsertStaysCanonicalAfterMerge(t *testing.T) {
	for seed := 0; seed < 50; seed++ {
		local := NewDocument(hlc.NewClock("a"))
		local.Insert(0, "base")

		peer := NewDocument(hlc.NewClock("b"))
		peer.Insert(0, "peer text ")

		local.MergeFrom(peer.Text())
		local.Insert(0, "typed ")

		// What a replica deriving the order from the runs alone would read.
		canonical := DocumentFromText(local.Text(), hlc.NewClock("c"))
		if local.String() != canonical.String() {
			t.Fatalf("seed %d: this replica reads %q, deriving the order gives %q",
				seed, local.String(), canonical.String())
		}

		// And a peer receiving those runs must agree.
		receiver := NewDocument(hlc.NewClock("d"))
		receiver.MergeFrom(local.Text())
		if receiver.String() != local.String() {
			t.Fatalf("seed %d: a peer reads %q, this replica reads %q",
				seed, receiver.String(), local.String())
		}
	}
}
