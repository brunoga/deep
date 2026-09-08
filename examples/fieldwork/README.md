# fieldwork

An offline-first sync engine. Technicians inspect assets in places with no
signal; dispatch edits the same records from the office. Both sides work on
copies, and one round trip reconciles them.

```
   field (device)                            fieldd                dispatch
   ┌─────────────────┐                  ┌──────────────┐          (online,
   │ shadow  working │                  │ asset + log  │           direct
   │   │        │    │                  │ v1 ─ v2 ─ v3 │           patches)
   │   └─ diff ─┘    │ ── push (diff) ─▶│      │       │◀── patch ──
   │        =        │                  │  three-way   │
   │     outbox      │◀── authoritative │    merge     │
   └─────────────────┘        state     └──────────────┘
     no queue, no journal:                 Merge(theirs, mine, policy)
     the outbox is derived
```

## The idea

The device stores every asset **twice**: a *shadow* (the last state the
server confirmed) and a *working copy* (what the technician has done since).
That is the whole design, because it makes the outbox derived rather than
maintained:

```go
pending := deep.Diff(shadow, working)
```

Edit one field ten times offline and you still owe the server one operation.
Edit a field and undo it and you owe nothing. There is no operation queue to
append to, replay, deduplicate, compact or garbage-collect — and `field
status` is not a report *about* the queue, it is the queue:

```
$ field status
pump-7 (from version 1)
    Replace /status: ok -> fault
    Replace /readings/ph/value: 7.1 -> 6.2
    Replace /checks/seals/done: false -> true
    Replace /notes:  -> seals leaking, replaced gasket
```

## Three-way merge, where `Merge` belongs

When a push arrives with a stale base version, the server has moved on. It
reconstructs the state the device diverged from — by reversing canonical log
entries — diffs that against the present to get *its* concurrent changes, and
hands both patches to `deep.Merge` with a policy resolver:

```go
base       := stateAt(push.BaseVersion)        // reverse-walk the log
theirs     := deep.Diff(base, current)          // what the office did
merged     := deep.Merge(theirs, push.Patch, policy)
```

This is `Merge` in the role it was designed for: **two concurrent patches
against one common base**. (The other two example systems deliberately do
*not* use it for composing sequential patch streams — see
[`arena`](../arena)'s `replay.Compact` for why that is a different problem.)

The policy is domain knowledge, and it is small:

| Path | Winner | Because |
| :--- | :--- | :--- |
| `/readings/*` | the field | whoever stood at the asset measured it |
| `/status` | the worse of the two | a fault seen by either side is a fault |
| `/notes` | both, concatenated | nobody's field notes get thrown away |
| everything else | the office | assignment and scheduling are dispatch's call |

Every decision the resolver makes is recorded and returned, so the technician
sees what happened rather than discovering it later:

```
$ field sync
pushed 1, merged 1, received 0

pump-7 — the office had changed things too:
    /status: kept yours (yours "fault", theirs "attention")
    /notes: combined both
```

Because diffs are per-field, most concurrent edits never reach the policy at
all: two technicians recording *different* sensors, or completing *different*
checklist items, produce disjoint paths and merge with no conflict.

## Why not a CRDT?

[`incident`](../incident) solves collaborative editing with a CRDT and
[`arena`](../arena) broadcasts authoritative state; this example is the third
answer, and the trade is worth being explicit about.

A CRDT converges without a server and without ever asking anyone — but it
converges to whatever its merge rule says, the rule is fixed in the data
structure, and history is the structure's business. This design keeps a
server in the loop and pays a round trip, and in exchange the merge rule is
*application policy* you can read, test and change ("the worse status wins"),
every change is a numbered version with an author, any past state is a
reverse-walk away, and a change the domain considers invalid can be
**rejected** — something a CRDT has no vocabulary for. When a push is
refused, the device keeps its work and retries after a fix; nothing is lost
and nothing was corrupted.

## Running it

```bash
go run ./cmd/fieldd -token sekrit -seed                    # the office server

export FIELD_SERVER=http://localhost:8090 FIELD_TOKEN=sekrit FIELD_AUTHOR=ana
go run ./cmd/field pull                                    # provision the device

# ...drive into the hills. None of this touches the network:
go run ./cmd/field reading pump-7 ph 6.4 pH
go run ./cmd/field check pump-7 seals
go run ./cmd/field status pump-7 fault
go run ./cmd/field note pump-7 "seals leaking"
go run ./cmd/field status                                  # the outbox

# meanwhile, at the office:
go run ./cmd/dispatch assign pump-7 bruno
go run ./cmd/dispatch note pump-7 "scada flagged drift"

# ...back in signal:
go run ./cmd/field sync
go run ./cmd/field show pump-7
go run ./cmd/field history pump-7
```

## What happens where

| Library feature | Where it works here |
| :--- | :--- |
| `Diff` as the outbox | `client/local.go` `pendingLocked` — the shadow/working difference *is* the pending work |
| `Merge` with a `ConflictResolver` | `server/sync.go` `mergeWithPolicy` — two concurrent patches, one base, a domain policy |
| `Reverse` for time travel | `server/store.go` `stateAtLocked` — any past version, reconstructed from the log |
| Canonical patch log | `server/store.go` `commitLocked` — entries are server-derived diffs, so they always reverse exactly |
| Per-field map diffs | `model/model.go` `Readings` — two techs editing different sensors, or different fields of one sensor, never collide |
| Keyed slices (`deep:"key"`) | `model/model.go` `Checks` — checklist items addressed by identity |
| `WithAllowedPaths` | `server/store.go` — `/id` is identity and unpatchable |
| Outcome validation | `server/sync.go` — a merge that produces an invalid asset is rejected whole; the device keeps its work |
| `Clone` | everywhere state crosses a boundary — shadows, working copies, scratch merges |
| Generated fast path (`deep-gen`) | `model/model.go` (`go:generate`) — diffs run on every edit and every sync |
| JSON wire form / `RawValue` | `server/http.go`, `client/client.go` — patches and merge values decode at apply time |

## Layout

```
model/     the asset: map readings, keyed checks, severity ordering
server/    versioned store, patch log, sync engine and policy, HTTP API
client/    the device: shadow/working store, sync round trip
cmd/       fieldd (server) · field (technician, offline) · dispatch (office)
e2e_test.go  two devices and the office, all editing at once
```

This is its own Go module, like the other complete systems.
