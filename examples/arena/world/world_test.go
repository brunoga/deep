package world

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"testing"

	deep "github.com/brunoga/deep/v6"
)

func sample() World {
	w := New(16, 12)
	w.Tick = 41
	w.Players["ana"] = Player{X: 3, Y: 4, Score: 7, Hue: 1}
	w.Players["bob"] = Player{X: 9, Y: 2, Score: 3, Hue: 2}
	w.Gems["g1"] = Gem{X: 5, Y: 5, Value: 3}
	w.Gems["g2"] = Gem{X: 1, Y: 9, Value: 1}
	return w
}

// One tick's worth of change diffs down to exactly the entities that moved:
// map entries appear as single Adds, vanish as single Removes, and a field
// change addresses the field.
func TestDiffShapesArePerEntity(t *testing.T) {
	a := sample()
	b := deep.Clone(a)
	b.Tick++
	p := b.Players["ana"]
	p.X++
	p.Score += 3
	b.Players["ana"] = p
	delete(b.Gems, "g1")
	b.Gems["g3"] = Gem{X: 0, Y: 0, Value: 5}

	patch, err := deep.Diff(a, b)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]deep.OpKind{}
	for _, op := range patch.Operations {
		got[op.Path] = op.Kind
	}
	want := map[string]deep.OpKind{
		"/tick":              deep.OpReplace,
		"/players/ana/x":     deep.OpReplace,
		"/players/ana/score": deep.OpReplace,
		"/gems/g1":           deep.OpRemove,
		"/gems/g3":           deep.OpAdd,
	}
	if len(got) != len(want) {
		t.Fatalf("got %d ops, want %d: %v", len(got), len(want), got)
	}
	for path, kind := range want {
		if got[path] != kind {
			t.Errorf("path %s: got %v, want %v", path, got[path], kind)
		}
	}

	if err := deep.Apply(&a, patch); err != nil {
		t.Fatal(err)
	}
	if !deep.Equal(a, b) {
		t.Fatal("apply did not converge")
	}
}

// The tick patch travels as gob; a replica applying the decoded patch lands
// on the same world.
func TestPatchGobRoundTrip(t *testing.T) {
	a := sample()
	b := deep.Clone(a)
	b.Tick++
	delete(b.Gems, "g2")
	p := b.Players["bob"]
	p.Y--
	p.Score++
	b.Players["bob"] = p

	patch, err := deep.Diff(a, b)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(patch); err != nil {
		t.Fatal(err)
	}
	var decoded deep.Patch[World]
	if err := gob.NewDecoder(&buf).Decode(&decoded); err != nil {
		t.Fatal(err)
	}

	replica := deep.Clone(a)
	if err := deep.Apply(&replica, decoded); err != nil {
		t.Fatal(err)
	}
	if !deep.Equal(replica, b) {
		t.Fatalf("gob round trip diverged:\n got  %+v\n want %+v", replica, b)
	}
}

// The generated applier and the reflection engine agree on wire-decoded
// patches.
func TestGeneratedAgreesWithReflection(t *testing.T) {
	a := sample()
	b := deep.Clone(a)
	b.Tick++
	b.Gems["g9"] = Gem{X: 2, Y: 2, Value: 4}
	delete(b.Players, "bob")

	patch := a.Diff(&b) // generated
	data, err := json.Marshal(deep.Patch[World](patch))
	if err != nil {
		t.Fatal(err)
	}
	var wire deep.Patch[World]
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}

	viaGen := deep.Clone(a)
	if err := viaGen.Patch(wire, nil); err != nil {
		t.Fatal(err)
	}
	viaRefl := deep.Clone(a)
	if err := deep.Apply(&viaRefl, wire); err != nil {
		t.Fatal(err)
	}
	if !deep.Equal(viaGen, b) || !deep.Equal(viaRefl, b) {
		t.Fatalf("appliers disagree:\n gen  %+v\n refl %+v\n want %+v", viaGen, viaRefl, b)
	}
}

// Reversing a tick patch steps the world back exactly — the property the
// replay tool's rewind rests on.
func TestTickPatchReverses(t *testing.T) {
	a := sample()
	b := deep.Clone(a)
	b.Tick++
	delete(b.Gems, "g1")
	p := b.Players["ana"]
	p.X, p.Score = p.X+1, p.Score+3
	b.Players["ana"] = p

	patch, err := deep.Diff(a, b)
	if err != nil {
		t.Fatal(err)
	}
	back := deep.Clone(b)
	if err := deep.Apply(&back, patch.Reverse()); err != nil {
		t.Fatal(err)
	}
	if !deep.Equal(back, a) {
		t.Fatalf("reverse did not restore:\n got  %+v\n want %+v", back, a)
	}
}

func TestValidate(t *testing.T) {
	w := sample()
	if err := w.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := deep.Clone(w)
	bad.Gems["../evil"] = Gem{}
	if bad.Validate() == nil {
		t.Fatal("traversal gem id accepted")
	}
	bad = deep.Clone(w)
	bad.Players["ana"] = Player{X: 99, Y: 0}
	if bad.Validate() == nil {
		t.Fatal("out-of-bounds player accepted")
	}
}

// The per-tick diff is the hot path; this is why the model ships generated
// code.
func BenchmarkTickDiff(b *testing.B) {
	w := New(64, 48)
	for i := range 200 {
		id := string(rune('a'+i%26)) + string(rune('a'+(i/26)%26)) + string(rune('0'+i%10))
		if i%2 == 0 {
			w.Players[id] = Player{X: i % 64, Y: i % 48, Score: i}
		} else {
			w.Gems[id] = Gem{X: i % 64, Y: i % 48, Value: i % 5}
		}
	}
	next := deep.Clone(w)
	next.Tick++
	for id, p := range next.Players {
		p.X = (p.X + 1) % 64
		next.Players[id] = p
		break
	}

	b.ReportAllocs()
	for b.Loop() {
		if _, err := deep.Diff(w, next); err != nil {
			b.Fatal(err)
		}
	}
}
