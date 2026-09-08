package game

import (
	"encoding/json"
	"errors"
	"math/rand/v2"
	"testing"

	deep "github.com/brunoga/deep/v6"

	"github.com/brunoga/deep/examples/arena/world"
)

func rng() *rand.Rand { return rand.New(rand.NewPCG(1, 2)) }

func arena(t *testing.T) world.World {
	t.Helper()
	w := world.New(8, 8)
	w.Players["ana"] = world.Player{X: 2, Y: 2, Hue: 1}
	w.Players["bob"] = world.Player{X: 5, Y: 5, Hue: 2}
	w.Gems["g1"] = world.Gem{X: 3, Y: 2, Value: 4}
	return w
}

// Actions cross a wire in real life; push each through gob before applying.
func wire(t *testing.T, p deep.Patch[world.World]) deep.Patch[world.World] {
	t.Helper()
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var q deep.Patch[world.World]
	if err := json.Unmarshal(data, &q); err != nil {
		t.Fatal(err)
	}
	return q
}

func TestMoveAndPickup(t *testing.T) {
	w := arena(t)
	patch, ok := Move(w, "ana", 1, 0) // onto g1
	if !ok {
		t.Fatal("move not built")
	}
	res, err := Apply(&w, "ana", wire(t, patch))
	if err != nil {
		t.Fatal(err)
	}
	if a, _, f := res.Counts(); a != 3 || f != 0 {
		t.Fatalf("counts: %+v", res)
	}
	me := w.Players["ana"]
	if me.X != 3 || me.Score != 4 {
		t.Fatalf("after pickup: %+v", me)
	}
	if _, ok := w.Gems["g1"]; ok {
		t.Fatal("gem still on the floor")
	}
}

// Two players walk onto the same gem; the second action's gem operations
// skip on their Exists conditions and only the winner scores.
func TestPickupRace(t *testing.T) {
	w := arena(t)
	w.Players["bob"] = world.Player{X: 3, Y: 3, Hue: 2} // one below g1

	// Both actions are built against the same stale view — the race.
	view := deep.Clone(w)
	anaPatch, _ := Move(view, "ana", 1, 0)
	bobPatch, _ := Move(view, "bob", 0, -1)

	if _, err := Apply(&w, "ana", wire(t, anaPatch)); err != nil {
		t.Fatal(err)
	}
	res, err := Apply(&w, "bob", wire(t, bobPatch))
	if err != nil {
		t.Fatalf("loser's action must not error: %v", err)
	}
	applied, skipped, _ := res.Counts()
	if applied != 1 || skipped != 2 {
		t.Fatalf("loser should move and skip the gem ops: %+v", res)
	}
	if w.Players["ana"].Score != 4 || w.Players["bob"].Score != 0 {
		t.Fatalf("scores: ana=%d bob=%d", w.Players["ana"].Score, w.Players["bob"].Score)
	}
	if w.Players["bob"].Y != 2 {
		t.Fatalf("loser's move lost: %+v", w.Players["bob"])
	}
}

// A duplicated packet: the same move applied twice skips the second time.
func TestStaleMoveSkips(t *testing.T) {
	w := arena(t)
	patch, _ := Move(w, "ana", 0, 1)
	p := wire(t, patch)
	if _, err := Apply(&w, "ana", p); err != nil {
		t.Fatal(err)
	}
	res, err := Apply(&w, "ana", p)
	if err != nil {
		t.Fatal(err)
	}
	if a, s, _ := res.Counts(); a != 0 || s != 1 {
		t.Fatalf("duplicate should skip: %+v", res)
	}
	if w.Players["ana"].Y != 3 {
		t.Fatalf("duplicate moved the player: %+v", w.Players["ana"])
	}
}

// The cheats. Each hand-built patch applies cleanly as a patch — it is the
// outcome that the rules refuse, and the world stays untouched.
func TestCheatsRejected(t *testing.T) {
	cheats := map[string]deep.Patch[world.World]{
		"teleport": deep.NewPatch[world.World]().With(
			deep.Set(playerPath[int]("ana", "x"), 7)).Build(),
		"score inflation": deep.NewPatch[world.World]().With(
			deep.Set(playerPath[int]("ana", "score"), 9000)).Build(),
		"gem edit": deep.NewPatch[world.World]().With(
			deep.Set(deep.PathString[world.World, int]("/gems/g1/value"), 99)).Build(),
		"gem conjuring": {Operations: []deep.Operation{{
			Kind: deep.OpAdd, Path: "/gems/mine", New: world.Gem{X: 2, Y: 2, Value: 5}}}},
		"remote pickup": {Operations: []deep.Operation{{
			Kind: deep.OpRemove, Path: "/gems/g1"}}},
		"hue change": deep.NewPatch[world.World]().With(
			deep.Set(playerPath[int]("ana", "hue"), 5)).Build(),
	}
	for name, cheat := range cheats {
		w := arena(t)
		before := deep.Clone(w)
		if _, err := Apply(&w, "ana", wire(t, cheat)); !errors.Is(err, ErrRejected) {
			t.Errorf("%s: want ErrRejected, got %v", name, err)
		}
		if !deep.Equal(w, before) {
			t.Errorf("%s: rejected action mutated the world", name)
		}
	}
}

// Another player's subtree is out of reach before the rules even look.
func TestCannotTouchOthers(t *testing.T) {
	w := arena(t)
	shove := deep.NewPatch[world.World]().With(
		deep.Set(playerPath[int]("bob", "x"), 0)).Build()
	_, err := Apply(&w, "ana", wire(t, shove))
	if !errors.Is(err, ErrRejected) || !errors.Is(err, deep.ErrPathNotAllowed) {
		t.Fatalf("want rejection via ErrPathNotAllowed, got %v", err)
	}
	// The server's own fields are equally off limits.
	clock := deep.NewPatch[world.World]().With(
		deep.Set(deep.PathString[world.World, int64]("/tick"), 999)).Build()
	if _, err := Apply(&w, "ana", wire(t, clock)); !errors.Is(err, deep.ErrPathNotAllowed) {
		t.Fatalf("tick write: want ErrPathNotAllowed, got %v", err)
	}
}

func TestJoinLeaveAndSpawner(t *testing.T) {
	w := world.New(4, 4)
	r := rng()
	if _, err := Join(&w, "../evil", r); err == nil {
		t.Fatal("traversal id joined")
	}
	p, err := Join(&w, "ana", r)
	if err != nil {
		t.Fatal(err)
	}
	if !w.InBounds(p.X, p.Y) {
		t.Fatalf("spawned out of bounds: %+v", p)
	}
	if _, err := Join(&w, "ana", r); err == nil {
		t.Fatal("duplicate join accepted")
	}

	s := NewSpawner(3, r)
	for range 10 {
		s.Refill(&w)
	}
	if len(w.Gems) != 3 {
		t.Fatalf("spawner target: %d gems", len(w.Gems))
	}
	for id, g := range w.Gems {
		if pid, taken := w.PlayerAt(g.X, g.Y); taken {
			t.Fatalf("gem %s spawned under player %s", id, pid)
		}
	}
	if err := w.Validate(); err != nil {
		t.Fatal(err)
	}

	Leave(&w, "ana")
	if _, ok := w.Players["ana"]; ok {
		t.Fatal("leave did not remove the player")
	}
}

// Moves off the edge or diagonal are not even built.
func TestMoveBuilderRefusesIllegal(t *testing.T) {
	w := world.New(3, 3)
	w.Players["ana"] = world.Player{X: 0, Y: 0}
	if _, ok := Move(w, "ana", -1, 0); ok {
		t.Fatal("built a move off the edge")
	}
	if _, ok := Move(w, "ana", 1, 1); ok {
		t.Fatal("built a diagonal move")
	}
	if _, ok := Move(w, "ghost", 1, 0); ok {
		t.Fatal("built a move for a missing player")
	}
}
