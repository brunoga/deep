// Package world is arena's state: one struct that the server mutates, diffs
// and broadcasts, and that every client mirrors by applying patches.
//
// The shapes are deliberately map-heavy — players and gems are maps keyed by
// id — because map diffs are what a game state exercises: an entity appearing
// is one Add at /gems/g7, an entity leaving is one Remove, and a field change
// is one Replace at /players/bob/x. Nothing here knows about the network;
// the whole synchronization story is deep.Diff on one side and deep.Apply on
// the other.
package world

//go:generate go run github.com/brunoga/deep/v6/cmd/deep-gen -type=World,Player,Gem -output world_deep.go .

import (
	"fmt"
	"regexp"
)

// World is the entire game, and the unit of replication. The server owns the
// only mutable copy; every client holds a replica that patches keep current.
type World struct {
	// Tick counts state generations. It is the server's own field — action
	// patches may not touch it — and doubles as the replay file's clock.
	Tick   int64 `json:"tick"`
	Width  int   `json:"width"`
	Height int   `json:"height"`
	// Players by id. A player's id is also the path under which every one of
	// their actions must stay: /players/<id>.
	Players map[string]Player `json:"players,omitempty"`
	// Gems by id, waiting to be picked up.
	Gems map[string]Gem `json:"gems,omitempty"`
}

// Player is one participant: a position and a score.
type Player struct {
	X     int `json:"x"`
	Y     int `json:"y"`
	Score int `json:"score"`
	// Hue picks the player's colour in clients, assigned at join.
	Hue int `json:"hue"`
}

// Gem sits on a cell until somebody walks onto it.
type Gem struct {
	X     int `json:"x"`
	Y     int `json:"y"`
	Value int `json:"value"`
}

// IDPattern is the shape of player and gem ids. Ids become path segments
// ("/players/bob/x") and map keys on the wire; keeping them to this alphabet
// keeps paths canonical and escaping-free.
var IDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,31}$`)

// New returns an empty world of the given size.
func New(width, height int) World {
	return World{Width: width, Height: height,
		Players: map[string]Player{}, Gems: map[string]Gem{}}
}

// PlayerAt reports the id of a player on the cell, if any.
func (w World) PlayerAt(x, y int) (string, bool) {
	for id, p := range w.Players {
		if p.X == x && p.Y == y {
			return id, true
		}
	}
	return "", false
}

// GemAt reports the id of a gem on the cell, if any.
func (w World) GemAt(x, y int) (string, bool) {
	for id, g := range w.Gems {
		if g.X == x && g.Y == y {
			return id, true
		}
	}
	return "", false
}

// InBounds reports whether the cell exists.
func (w World) InBounds(x, y int) bool {
	return x >= 0 && x < w.Width && y >= 0 && y < w.Height
}

// Validate checks structural sanity — used on worlds that arrive from
// outside, like a replay file's initial snapshot.
func (w World) Validate() error {
	if w.Width < 1 || w.Height < 1 {
		return fmt.Errorf("world: bad size %dx%d", w.Width, w.Height)
	}
	for id, p := range w.Players {
		if !IDPattern.MatchString(id) {
			return fmt.Errorf("world: bad player id %q", id)
		}
		if !w.InBounds(p.X, p.Y) {
			return fmt.Errorf("world: player %s out of bounds at %d,%d", id, p.X, p.Y)
		}
	}
	for id, g := range w.Gems {
		if !IDPattern.MatchString(id) {
			return fmt.Errorf("world: bad gem id %q", id)
		}
		if !w.InBounds(g.X, g.Y) {
			return fmt.Errorf("world: gem %s out of bounds at %d,%d", id, g.X, g.Y)
		}
	}
	return nil
}
