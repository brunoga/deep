# commandpost

A working incident-management system built to show the whole `deep` library
composed into one application. It is not a toy loop over one concept like the
other examples — it is a server, a CLI, a live terminal UI, and a bot, each
using the part of the library whose job matches its own.

```
                 ┌───────────────────────────────┐
   incident ────▶│           incidentd           │◀──── statusbot
   (CLI + TUI)   │                               │      (protobuf consumer)
                 │  JSON patch API  ── audit log │
   patches with  │  websocket hub   ── notes     │  snapshots as proto msgs,
   conditions    │  proto endpoints ── envelope  │  change feed from diffs
                 └───────────────┬───────────────┘
                                 │
                          a plain directory:
                    state.json · log.jsonl · notes.bin
```

## The idea

An incident is two kinds of state, and the library has a consistency model for
each:

- **The record** — severity, status, commander, checklist — needs one
  authoritative answer, so it lives on the server and every change is a
  `deep.Patch[Incident]`: conditional, guarded, audited, reversible.
- **The timeline notes** — prose typed by several responders at once — needs
  everyone typing simultaneously, so it is a `crdt.Document` in a websocket
  room: merges instead of conflicts, presence instead of locks.

Nobody ever sends a whole incident after creation. The server re-derives every
change as a canonical diff, so the audit log is exact regardless of what the
client sent — and `history` + `undo` fall out of `Patch.String()` and
`Patch.Reverse()` rather than being features anyone had to build.

## Running it

```bash
# The server. Auth is one shared token; data is a plain directory.
go run ./cmd/incidentd -addr :8080 -data ./data -token sekrit

# A responder's session (in another terminal):
export INCIDENT_SERVER=http://localhost:8080 INCIDENT_TOKEN=sekrit INCIDENT_AUTHOR=ana
go build -o /tmp/incident ./cmd/incident
/tmp/incident create inc-1 "checkout errors" 2
/tmp/incident task add inc-1 t1 "page db oncall"
/tmp/incident task claim inc-1 t1     # If /tasks/t1/owner == "" — race-safe
/tmp/incident escalate inc-1 1        # If /severity > 1 — never de-escalates
/tmp/incident history inc-1           # the audit log, patch by patch
/tmp/incident undo inc-1 3            # Reverse, applied strictly, appended
/tmp/incident open inc-1              # the TUI: record + shared notes + presence

# The external consumer (any language could do this; this one is Go):
go run ./cmd/statusbot -incident inc-1
```

Open the TUI from two terminals with different `INCIDENT_AUTHOR`s and type
into the notes pane: keystrokes merge live, cursors show in the presence bar,
and killing one terminal mid-sentence loses nothing.

## What happens where

The interesting property is *why* each piece is easy: the concurrency story is
carried by the patches themselves, so the server has no per-field locking, the
clients no retry loops, and the bot no schema knowledge beyond its own
generated types.

| Library feature | Where it works here |
| :--- | :--- |
| `Diff` / `Apply` / `Clone` / `Equal` | `server/store.go` — every read is a `Clone`, every audit entry a canonical `Diff`; `cmd/incident` `watch` recovers a change feed by diffing snapshots |
| Generated fast path (`deep-gen`) | `model/model.go` (`go:generate`) → `model/model_deep.go`; the server diffs and patches through it |
| Keyed collections (`deep:"key"`) | `model.Task.ID` — `/tasks/t3/done` paths, reorder-silent diffs, per-task conflict isolation |
| Type families | `model/model.go` — `time.Time` and `netip.Addr` kept opaque, with their own equality and wire forms (without this, diffs address `/updated/ext` and `/hosts/0/addr/lo`) |
| Conditions (`If` / `Unless`) | `client/patches.go` — `ClaimTask` (exactly one racer wins), `Escalate` (idempotent, never lowers); `cmd/statusbot` announces `Unless` it already did |
| Patch `Guard` | `client/patches.go` `Close` — refuse the whole patch unless resolved |
| Strict mode (optimistic locking) | `client/patches.go` `Handoff` — command transfers only from who you think holds it; `server/store.go` `Undo` — a stale undo refuses instead of clobbering |
| `WithAllowedPaths` | `server/store.go` — `/id` and `/updated` are unpatchable, whatever arrives |
| `ApplyWithResult` | `server/store.go` — applied / skipped / failed per operation, surfaced through the API so a skipped claim reads as "someone got there first" |
| `Reverse` | `server/store.go` `Undo` — append-only undo of any audit entry |
| `Merge` | `server/store.go` `Compact` — collapsing the log head into one baseline, later writes winning |
| JSON wire form / `RawValue` | `server/http.go` — patches arrive encoded and decode at apply time against the real field types |
| Path selectors | `client/patches.go` — `PathString`, `EscapePathKey` building every path |
| `crdt.Document` | `tui/` and `server/notes.go` — the shared timeline |
| Binary encoding (`Update`, `StateVector`) | `server/notes.go` — notes persist to disk as one full-state update |
| `crdt.Awareness` | `tui/` — the presence bar and cursor positions, heartbeat and expiry |
| `deepws.Hub` (auth, eviction, liveness) | `server/notes.go` — token auth shared with HTTP, lazy seeding from disk on first join, persistence on eviction |
| `deepws.Client` (sync, resume) | `tui/`; `e2e_test.go` — `Detach` + `WithDocument` carry offline edits across a reconnect |
| `deepproto` (proto family, keyed fields) | `cmd/statusbot` — snapshots diffed as protobuf messages through the proto runtime, tasks matched by `id` via `RegisterListKey` |
| `wire.Patch` envelope | `server/proto.go` + `cmd/statusbot` — a patch built against generated proto types, carried as protobuf, applied to the Go model |

## Layout

```
model/       the Incident: keyed tasks, families, generated fast path
server/      store + audit log, HTTP API, notes hub, proto endpoints
client/      typed HTTP client and the patch vocabulary
tui/         the live view: record pane, CRDT notes editor, presence
pb/          the protobuf schema and generated types
cmd/         incidentd (server) · incident (CLI/TUI) · statusbot (consumer)
e2e_test.go  the whole story against one real server
```

This is its own Go module so its dependencies (bubbletea, protobuf) stay out
of the core library.
