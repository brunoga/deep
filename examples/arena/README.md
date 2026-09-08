# arena

A multiplayer game built on one loop: the server diffs the world every tick
and broadcasts only the patch. Where [`incident`](../incident) shows the
library's breadth, arena shows its hot path — `deep.Diff` twenty times a
second as the entire netcode.

```
   arena (TUI) ──┐                      ┌── arenabot ×N
   arrows move,  │   ws + gob frames    │   walk to nearest gem
   r = instant ──┤◀══ tick patches ══▶├──
   replay        │    actions in        │
                 └──────┬───────────────┘
                     arenad
            one goroutine owns the world:
        actions → rules → tick → Diff → broadcast
                        │
                  match.tape  ──▶  arenatape
             (initial world +      play · -rewind · -verify
              the patch stream)    -keyframes
```

## The idea

There is no dirty-flag bookkeeping anywhere. The game rules mutate the world
however they like; `deep.Diff(lastBroadcast, world)` finds what changed, the
patch goes out as gob, and every client folds it into its replica with
`deep.Apply`. A periodic snapshot frame lets clients *prove* their replica
never drifted — the test suite fails if that check ever fires.

Player actions are conditional patches, and that carries the whole
concurrency story:

- **Move** — `Replace /players/ana/x` `If` it still holds the value the
  client saw. A duplicated or reordered packet skips instead of teleporting.
- **Pickup** — bank the score and remove the gem, both `If` the gem still
  exists. Two players stepping onto the same gem in the same tick both move;
  exactly one scores. No locks, no retry loops, on either side.
- **Cheating** — a hand-built patch can say anything, so the server confines
  each player to `/players/<you>` and `/gems` with `WithAllowedPaths`, then
  judges the *outcome* on a clone: one step per action, score growth equal to
  gems removed from under your feet, nothing else touched.

The replay file is the same patch stream written to disk. `arenatape` plays
it forward with `Apply`, backward with `Reverse` (and `-verify` proves the
round trip is exact), and `-keyframes` collapses it by diffing boundary
states — a 200-tick bot match compacts about 10× because wandering cancels
out. The TUI's instant replay (`r`) needs no file at all: the client rewinds
its own world through the `Reverse` of patches it already applied.

## Running it

```bash
go run ./cmd/arenad -addr :7777 -replay match.tape   # the server
go run ./cmd/arenabot -n 4                           # some opponents
go run ./cmd/arena -name you                         # you; arrows move, r replays

# afterwards:
go run ./cmd/arenatape -verify match.tape
go run ./cmd/arenatape match.tape
go run ./cmd/arenatape -rewind match.tape
go run ./cmd/arenatape -keyframes 50 match.tape
```

## What happens where

| Library feature | Where it works here |
| :--- | :--- |
| Per-tick `Diff` broadcast | `server/server.go` `tick()` — the entire sync protocol is one Diff and one gob encode |
| Generated fast path (`deep-gen`) | `world/world.go` (`go:generate`); `BenchmarkTickDiff` shows ~5µs for a 200-entity world |
| Map diffs, per-entity ops | `world/` — players and gems are maps; an entity change is one op at `/players/ana/x` or `/gems/g7` |
| `Apply` on replicas | `client/client.go` read loop — a replica is nothing but applied patches |
| `Equal` as drift proof | `client/client.go` snapshot check; `Drift()` staying zero is the diff/apply contract holding |
| Conditions (`If`, `Exists`) | `game/actions.go` — stale moves skip, gem races have one winner |
| `WithAllowedPaths` | `game/game.go` `Apply` — your subtree and the gems, nothing else |
| `ApplyWithResult` | `game/game.go` — a skip is a lost race, not an error |
| Outcome validation on a clone | `game/game.go` `judge` — what conditions cannot express |
| gob wire format | `transport/transport.go` — patches as binary frames |
| `Reverse` | `replay/replay.go` `Rewind`; the TUI's instant replay; `arenatape -rewind`/`-verify` |
| Keyframe compaction | `replay/replay.go` `Compact` — boundary-state diffs, deliberately **not** `Merge` (see the comment there for why concurrent-merge semantics are wrong for sequential composition) |
| `Clone` | everywhere state changes hands — replicas, scratch worlds, broadcast baselines |

## Layout

```
world/       the replicated state: World, Player, Gem + generated fast path
game/        the rules: action builders, allowlist, outcome judging
transport/   frame kinds and gob helpers
replay/      the tape: write, read, replay, rewind, compact
server/      the authoritative loop; also the convergence/race/replay tests
client/      the replica keeper
cmd/         arenad · arena · arenabot · arenatape
```

This is its own Go module so its dependencies (websocket, bubbletea) stay out
of the core library.
