package deep_test

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"testing"

	deep "github.com/brunoga/deep/v5"
	"github.com/brunoga/deep/v5/internal/testmodels/shapes"
)

// Properties that must hold for every value, checked over generated inputs
// rather than over cases someone thought to write down.
//
// This exists because of how the bugs in this library have actually been found.
// Generated Clone duplicated values reached twice; it confused a nil slice with
// an empty one, so Equal(v, Clone(v)) was false; strict checks rejected state
// they matched once the patch had been through JSON; Diff silently dropped
// changes to a value reachable by two routes. Every one of those violates a
// one-line property below, and not one was caught by a unit test — the tests
// were written next to the implementation and shared its assumptions. The
// strict-mode tests, for instance, all built their patches in-process, so none
// of them ever saw a float64 where an int was expected.
//
// Generated inputs do not share those assumptions.

// ── value generation ─────────────────────────────────────────────────────────

// genDoc builds a shapes.Doc from a seed. shapes.Doc is the model for the
// structural shapes the generator has historically mishandled: an embedded
// struct, a keyed slice, a nested map, a map of pointers, a lone pointer, and
// an atomic field.
//
// Every field is drawn independently, including the choice between nil, empty
// and populated for each collection — the distinction that Clone used to lose.
func genDoc(rng *rand.Rand) shapes.Doc {
	d := shapes.Doc{
		Meta: shapes.Meta{Version: rng.Intn(4), Author: word(rng)},
		Name: word(rng),
	}

	switch rng.Intn(3) {
	case 0: // nil
	case 1:
		d.Entries = []shapes.Entry{}
	default:
		n := rng.Intn(4)
		d.Entries = make([]shapes.Entry, 0, n)
		for i := 0; i < n; i++ {
			// Keys must be distinct: a keyed slice addresses elements by key,
			// so duplicates are not a shape the model is meant to describe.
			d.Entries = append(d.Entries, shapes.Entry{
				ID: fmt.Sprintf("e%d", i),
				N:  rng.Intn(10),
			})
		}
	}

	switch rng.Intn(3) {
	case 0:
	case 1:
		d.Nested = map[string]map[string]int{}
	default:
		d.Nested = map[string]map[string]int{}
		for i := 0; i < rng.Intn(3); i++ {
			inner := map[string]int{}
			for j := 0; j < rng.Intn(3); j++ {
				inner[word(rng)] = rng.Intn(10)
			}
			d.Nested[word(rng)] = inner
		}
	}

	switch rng.Intn(3) {
	case 0:
	case 1:
		d.Stages = map[string]*shapes.Meta{}
	default:
		d.Stages = map[string]*shapes.Meta{}
		for i := 0; i < rng.Intn(3); i++ {
			d.Stages[word(rng)] = &shapes.Meta{Version: rng.Intn(4), Author: word(rng)}
		}
	}

	if rng.Intn(2) == 0 {
		d.Side = &shapes.Meta{Version: rng.Intn(4), Author: word(rng)}
	}

	switch rng.Intn(3) {
	case 0:
	case 1:
		d.Scores = map[string]int{}
	default:
		d.Scores = map[string]int{}
		for i := 0; i < rng.Intn(4); i++ {
			d.Scores[word(rng)] = rng.Intn(100)
		}
	}

	if rng.Intn(2) == 0 {
		d.Blob = shapes.Payload{Bytes: []byte(word(rng)), Label: word(rng)}
	}
	return d
}

// word returns one of a small vocabulary, so that two generated documents
// collide often enough for diffs to be interesting rather than always being a
// wholesale replacement.
func word(rng *rand.Rand) string {
	words := []string{"", "a", "b", "c", "one", "two", "~esc", "/slash"}
	return words[rng.Intn(len(words))]
}

// jsonRoundTrip sends a patch through the wire format and back, which is what
// a patch built in one process and applied in another goes through.
func jsonRoundTrip[T any](t *testing.T, p deep.Patch[T]) deep.Patch[T] {
	t.Helper()
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back deep.Patch[T]
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return back
}

const propertySeeds = 400

// ── the properties ───────────────────────────────────────────────────────────

func TestPropertyDiffApplyReachesTarget(t *testing.T) {
	// The defining property of a diff: applying it to where you started puts
	// you where you were going.
	for seed := 0; seed < propertySeeds; seed++ {
		rng := rand.New(rand.NewSource(int64(seed)))
		a, b := genDoc(rng), genDoc(rng)

		p, err := deep.Diff(a, b)
		if err != nil {
			t.Fatalf("seed %d: diff: %v", seed, err)
		}
		got := deep.Clone(a)
		if err := deep.Apply(&got, p); err != nil {
			t.Fatalf("seed %d: apply: %v", seed, err)
		}
		if !deep.Equal(got, b) {
			t.Fatalf("seed %d: diff+apply did not reach the target\n got: %+v\nwant: %+v", seed, got, b)
		}
	}
}

func TestPropertyDiffSurvivesTheWire(t *testing.T) {
	// The same, for a patch that was serialized and read back. This is the
	// property strict mode used to violate for every numeric field, because
	// JSON has one number type and the check demanded an exact type match.
	for seed := 0; seed < propertySeeds; seed++ {
		rng := rand.New(rand.NewSource(int64(seed)))
		a, b := genDoc(rng), genDoc(rng)

		p, err := deep.Diff(a, b)
		if err != nil {
			t.Fatalf("seed %d: diff: %v", seed, err)
		}

		got := deep.Clone(a)
		if err := deep.Apply(&got, jsonRoundTrip(t, p)); err != nil {
			t.Fatalf("seed %d: apply after round trip: %v", seed, err)
		}
		if !deep.Equal(got, b) {
			t.Fatalf("seed %d: a patch that went through JSON did not reach the target\n got: %+v\nwant: %+v",
				seed, got, b)
		}
	}
}

func TestPropertyStrictSurvivesTheWire(t *testing.T) {
	// Strict mode verifies every recorded Old before writing. Applied to the
	// value the diff was taken from, every one of those checks must pass —
	// including after the patch has been through JSON.
	for seed := 0; seed < propertySeeds; seed++ {
		rng := rand.New(rand.NewSource(int64(seed)))
		a, b := genDoc(rng), genDoc(rng)

		p, err := deep.Diff(a, b)
		if err != nil {
			t.Fatalf("seed %d: diff: %v", seed, err)
		}
		decoded := jsonRoundTrip(t, p)
		decoded.Strict = true

		got := deep.Clone(a)
		if err := deep.Apply(&got, decoded); err != nil {
			t.Fatalf("seed %d: strict apply of a decoded patch failed against the value it came from: %v",
				seed, err)
		}
	}
}

func TestPropertyReverseUndoesDiff(t *testing.T) {
	// Reverse has to take you back.
	for seed := 0; seed < propertySeeds; seed++ {
		rng := rand.New(rand.NewSource(int64(seed)))
		a, b := genDoc(rng), genDoc(rng)

		p, err := deep.Diff(a, b)
		if err != nil {
			t.Fatalf("seed %d: diff: %v", seed, err)
		}
		got := deep.Clone(b)
		if err := deep.Apply(&got, p.Reverse()); err != nil {
			t.Fatalf("seed %d: reverse apply: %v", seed, err)
		}
		if !deep.Equal(got, a) {
			t.Fatalf("seed %d: reverse did not return to the start\n got: %+v\nwant: %+v", seed, got, a)
		}
	}
}

func TestPropertyCloneIsFaithful(t *testing.T) {
	// A copy equals its source, and differs from it in nothing. The second half
	// is not redundant: Clone turning a nil slice into an empty one kept the
	// values equal under == but produced a non-empty diff.
	for seed := 0; seed < propertySeeds; seed++ {
		rng := rand.New(rand.NewSource(int64(seed)))
		v := genDoc(rng)

		c := deep.Clone(v)
		if !deep.Equal(v, c) {
			t.Fatalf("seed %d: a clone does not equal its source\nsrc: %+v\ncpy: %+v", seed, v, c)
		}
		p, err := deep.Diff(v, c)
		if err != nil {
			t.Fatalf("seed %d: diff: %v", seed, err)
		}
		if len(p.Operations) != 0 {
			t.Fatalf("seed %d: a clone differs from its source at %d paths: %v", seed, len(p.Operations), p)
		}
	}
}

func TestPropertyDiffOfEqualValuesIsEmpty(t *testing.T) {
	for seed := 0; seed < propertySeeds; seed++ {
		rng := rand.New(rand.NewSource(int64(seed)))
		v := genDoc(rng)

		p, err := deep.Diff(v, v)
		if err != nil {
			t.Fatalf("seed %d: diff: %v", seed, err)
		}
		if len(p.Operations) != 0 {
			t.Fatalf("seed %d: a value differs from itself at %d paths: %v", seed, len(p.Operations), p)
		}
	}
}

func TestPropertyEqualAgreesWithDiff(t *testing.T) {
	// Two values are equal exactly when there is nothing to say about turning
	// one into the other. These are separate implementations, so they can and
	// have disagreed.
	for seed := 0; seed < propertySeeds; seed++ {
		rng := rand.New(rand.NewSource(int64(seed)))
		a, b := genDoc(rng), genDoc(rng)

		p, err := deep.Diff(a, b)
		if err != nil {
			t.Fatalf("seed %d: diff: %v", seed, err)
		}
		if equal, empty := deep.Equal(a, b), len(p.Operations) == 0; equal != empty {
			t.Fatalf("seed %d: Equal says %v but the diff has %d operations: %v",
				seed, equal, len(p.Operations), p)
		}
	}
}

// ── fuzzing ──────────────────────────────────────────────────────────────────

// FuzzDiffApply drives the same round-trip property from fuzzer-chosen seeds,
// so the search is not limited to the range the tests above happen to walk.
//
//	go test -run=XXX -fuzz=FuzzDiffApply
func FuzzDiffApply(f *testing.F) {
	for _, s := range []int64{0, 1, 7, 42, 1 << 20} {
		f.Add(s, s+1)
	}
	f.Fuzz(func(t *testing.T, seedA, seedB int64) {
		a := genDoc(rand.New(rand.NewSource(seedA)))
		b := genDoc(rand.New(rand.NewSource(seedB)))

		p, err := deep.Diff(a, b)
		if err != nil {
			t.Fatalf("diff: %v", err)
		}

		for _, tc := range []struct {
			name  string
			patch deep.Patch[shapes.Doc]
		}{
			{"direct", p},
			{"through JSON", jsonRoundTrip(t, p)},
		} {
			got := deep.Clone(a)
			if err := deep.Apply(&got, tc.patch); err != nil {
				t.Fatalf("%s: apply: %v", tc.name, err)
			}
			if !deep.Equal(got, b) {
				t.Fatalf("%s: did not reach the target\n got: %+v\nwant: %+v", tc.name, got, b)
			}
		}
	})
}
