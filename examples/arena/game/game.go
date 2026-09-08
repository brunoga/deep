// Package game is arena's rulebook. Player actions arrive as patches; the
// rules decide what a legal outcome looks like.
//
// The split of responsibilities is the point of the example:
//
//   - The patch's conditions handle the races. "Take gem g7 If it still
//     exists" needs no lock and no retry loop: whichever action the server
//     applies first wins, and the loser's gem operations skip cleanly.
//   - The allowlist confines the blast radius. A player's patch may touch
//     their own subtree and the gems, nothing else — a path aimed at another
//     player is refused before anything runs.
//   - Validation judges the outcome. Conditions cannot say "you may only
//     move one step" or "your score may only grow by what you picked up";
//     the rules apply the patch to a scratch world and compare.
package game

import (
	"errors"
	"fmt"
	"math/rand/v2"

	deep "github.com/brunoga/deep/v6"

	"github.com/brunoga/deep/examples/arena/world"
)

var (
	// ErrRejected reports an action whose outcome broke the rules. The world
	// is untouched.
	ErrRejected = errors.New("action rejected")
	// ErrUnknownPlayer reports an actor the world does not hold.
	ErrUnknownPlayer = errors.New("unknown player")
)

// Apply runs one player's action patch against the world. The patch executes
// on a clone under the actor's allowlist; only an outcome that passes the
// rules replaces the world, so a rejection of any kind leaves it untouched.
//
// A nil error does not mean everything applied: operations whose conditions
// met reality — a gem somebody else took first — skip, and the result says
// so. Skipping is how races lose politely.
func Apply(w *world.World, actor string, patch deep.Patch[world.World]) (*deep.ApplyResult, error) {
	if _, ok := w.Players[actor]; !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownPlayer, actor)
	}
	work := deep.Clone(*w)
	res, err := deep.ApplyWithResult(&work, patch,
		deep.WithAllowedPaths("/players/"+actor, "/gems"))
	if err != nil {
		return res, fmt.Errorf("%w: %w", ErrRejected, err)
	}
	if err := judge(*w, work, actor); err != nil {
		return res, fmt.Errorf("%w: %w", ErrRejected, err)
	}
	*w = work
	return res, nil
}

// judge compares the world before and after one action and decides whether
// the difference is something the actor was allowed to cause.
func judge(before, after world.World, actor string) error {
	if err := after.Validate(); err != nil {
		return err
	}
	if len(after.Players) != len(before.Players) {
		return fmt.Errorf("player set changed")
	}
	for id, b := range before.Players {
		a, ok := after.Players[id]
		if !ok {
			return fmt.Errorf("player %s vanished", id)
		}
		if id != actor {
			if a != b {
				return fmt.Errorf("player %s is not yours", id)
			}
			continue
		}
		if a.Hue != b.Hue {
			return fmt.Errorf("hue is assigned, not chosen")
		}
		if dist := abs(a.X-b.X) + abs(a.Y-b.Y); dist > 1 {
			return fmt.Errorf("moved %d cells in one action", dist)
		}
	}

	// Gems may only be removed, and only from under the actor's feet; the
	// score may only grow by exactly what was removed.
	me := after.Players[actor]
	var picked int
	for id, g := range before.Gems {
		ag, ok := after.Gems[id]
		if !ok {
			if g.X != me.X || g.Y != me.Y {
				return fmt.Errorf("gem %s taken from a distance", id)
			}
			picked += g.Value
			continue
		}
		if ag != g {
			return fmt.Errorf("gem %s modified", id)
		}
	}
	for id := range after.Gems {
		if _, ok := before.Gems[id]; !ok {
			return fmt.Errorf("gem %s conjured", id)
		}
	}
	if delta := me.Score - before.Players[actor].Score; delta != picked {
		return fmt.Errorf("score moved by %d for %d worth of gems", delta, picked)
	}
	return nil
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// Join adds a player on a free cell and returns the world's new view of them.
func Join(w *world.World, id string, rng *rand.Rand) (world.Player, error) {
	if !world.IDPattern.MatchString(id) {
		return world.Player{}, fmt.Errorf("bad player id %q", id)
	}
	if _, ok := w.Players[id]; ok {
		return world.Player{}, fmt.Errorf("player %q already in the arena", id)
	}
	x, y, ok := freeCell(*w, rng)
	if !ok {
		return world.Player{}, fmt.Errorf("arena is full")
	}
	p := world.Player{X: x, Y: y, Hue: rng.IntN(6)}
	w.Players[id] = p
	return p, nil
}

// Leave removes a player.
func Leave(w *world.World, id string) {
	delete(w.Players, id)
}

// Spawner drips gems into the world, giving every tick something to diff
// even when the players stand still.
type Spawner struct {
	rng    *rand.Rand
	target int
	n      int
}

// NewSpawner keeps the world stocked with up to target gems.
func NewSpawner(target int, rng *rand.Rand) *Spawner {
	return &Spawner{rng: rng, target: target}
}

// Refill spawns at most one gem per call, so gems trickle rather than flood.
func (s *Spawner) Refill(w *world.World) {
	if len(w.Gems) >= s.target {
		return
	}
	x, y, ok := freeCell(*w, s.rng)
	if !ok {
		return
	}
	s.n++
	w.Gems[fmt.Sprintf("g%d", s.n)] = world.Gem{X: x, Y: y, Value: 1 + s.rng.IntN(5)}
}

// freeCell picks a random cell with nobody and nothing on it.
func freeCell(w world.World, rng *rand.Rand) (int, int, bool) {
	for range 64 {
		x, y := rng.IntN(w.Width), rng.IntN(w.Height)
		if _, taken := w.PlayerAt(x, y); taken {
			continue
		}
		if _, taken := w.GemAt(x, y); taken {
			continue
		}
		return x, y, true
	}
	return 0, 0, false
}
