package crdt

import (
	"encoding/json"
	"math/rand"
	"testing"
	"unicode/utf8"

	"github.com/brunoga/deep/v5/crdt/hlc"
	"strings"
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
	if len(wire) >= len(whole)/10 {
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
