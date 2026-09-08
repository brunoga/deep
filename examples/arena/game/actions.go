package game

import (
	deep "github.com/brunoga/deep/v6"

	"github.com/brunoga/deep/examples/arena/world"
)

// Action builders: every move a client can make, as a conditional patch. The
// conditions carry the client's assumptions — where it was standing, that the
// gem it wants still exists — so a stale or racing action degrades into skips
// and rejections instead of corruption.

func playerPath[V any](actor, field string) deep.Path[world.World, V] {
	return deep.PathString[world.World, V]("/players/" + actor + "/" + field)
}

func gemPath(id string) deep.Path[world.World, world.Gem] {
	return deep.PathString[world.World, world.Gem]("/gems/" + id)
}

// Move builds the patch for stepping one cell from where the client believes
// it stands. The step is conditioned on that belief: if the server has this
// player elsewhere — a duplicated or reordered packet — the move skips
// instead of teleporting from the wrong square.
//
// If the client can see a gem on the target cell, the pickup rides along:
// take the gem and bank its value, both If the gem still exists. Two players
// stepping onto the same gem in the same tick both move; the gem operations
// apply for whichever action the server ran first and skip for the other.
func Move(w world.World, actor string, dx, dy int) (deep.Patch[world.World], bool) {
	me, ok := w.Players[actor]
	if !ok || abs(dx)+abs(dy) != 1 {
		return deep.Patch[world.World]{}, false
	}
	nx, ny := me.X+dx, me.Y+dy
	if !w.InBounds(nx, ny) {
		return deep.Patch[world.World]{}, false
	}

	b := deep.NewPatch[world.World]()
	if dx != 0 {
		b.With(deep.Set(playerPath[int](actor, "x"), nx).
			If(deep.Eq(playerPath[int](actor, "x"), me.X)))
	} else {
		b.With(deep.Set(playerPath[int](actor, "y"), ny).
			If(deep.Eq(playerPath[int](actor, "y"), me.Y)))
	}

	if gemID, ok := w.GemAt(nx, ny); ok {
		gem := w.Gems[gemID]
		// Score first, then the gem: operations run in order and each
		// condition sees the state the previous ones left, so both watch the
		// gem while it is still (perhaps) there.
		b.With(
			deep.Set(playerPath[int](actor, "score"), me.Score+gem.Value).
				If(deep.Exists(gemPath(gemID))),
			deep.Remove(gemPath(gemID)).
				If(deep.Exists(gemPath(gemID))),
		)
	}
	return b.Build(), true
}
