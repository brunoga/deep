# deep: Type-Safe Diff, Patch, Clone and Sync for Go

`deep` is a comprehensive Go library for comparing, cloning, patching and synchronizing complex data structures. It combines **code generation** (fast, reflection-free implementations for your types) with a **reflection engine** (which handles everything else, including unexported fields and cyclic structures) behind one uniform API.

## Key Features

- **Four core operations**: `Diff`, `Apply`, `Equal`, `Clone` — one call each, for any type.
- **Hybrid architecture**: `deep-gen` generates optimized fast paths; a reflection engine transparently handles types without generated code, unexported fields, cycles, and exotic shapes.
- **Compile-time safety**: type-safe field selectors replace brittle string paths.
- **Data-oriented patches**: a patch is a flat, serializable list of operations — portable, mergeable, reversible.
- **Conditional patching**: guards and per-operation conditions travel inside the patch.
- **Strict mode**: optimistic-concurrency checks that verify expected old values before writing.
- **Standard interop**: RFC 6902 JSON Patch import/export, including strict checks as `test` ops.
- **First-class CRDTs**: `CRDT[T]` wrapper with hybrid logical clocks, plus `LWW`, `Text`, `Counter`, `Set` and `Map` convergent types.

## Quick Start

```bash
go get github.com/brunoga/deep/v5
```

### 1. Define your model

```go
type User struct {
    ID    int            `json:"id"`
    Name  string         `json:"name"`
    Roles []string       `json:"roles"`
    Score map[string]int `json:"score"`
}
```

### 2. Generate the fast path (optional but recommended)

```go
//go:generate go run github.com/brunoga/deep/v5/cmd/deep-gen -type=User .
```

```bash
go generate ./...
```

This writes `user_deep.go` next to your source. Commit it. Everything works without this step too — the reflection engine picks up any type automatically — generation just makes it much faster (see benchmarks below).

### 3. Use the API

```go
import deep "github.com/brunoga/deep/v5"

u1 := User{ID: 1, Name: "Alice", Roles: []string{"user"}}
u2 := User{ID: 1, Name: "Bob", Roles: []string{"user", "admin"}}

// Compare two values; get a patch describing the changes.
patch, err := deep.Diff(u1, u2)

// Apply a patch.
err = deep.Apply(&u1, patch)

// Deep equality and deep copy.
same := deep.Equal(u1, u2)
clone := deep.Clone(u1)
```

## Benchmarks

Generated code vs the reflection engine, on the same five-field struct (nested struct, slice, map). Reproduce with `go test -bench 'Generated|Reflection' -benchmem .`:

| Operation | Reflection | Generated | Speedup |
| :--- | ---: | ---: | ---: |
| **Diff** | 3,121 ns/op (77 allocs) | **546 ns/op** (16 allocs) | **5.7×** |
| **Apply** | 1,476 ns/op (49 allocs) | **80 ns/op** (2 allocs) | **18.5×** |
| **Equal** | 245 ns/op (5 allocs) | **100 ns/op** (2 allocs) | **2.4×** |
| **Clone** | 986 ns/op (15 allocs) | **188 ns/op** (5 allocs) | **5.3×** |

Deep copy compared with other clone libraries, same struct:

| Library | ns/op | allocs/op | Unexported fields | Cyclic structures |
| :--- | ---: | ---: | :---: | :---: |
| **deep (generated)** | **168** | 5 | ✅ | — (¹) |
| [barkimedes/go-deepcopy](https://github.com/barkimedes/go-deepcopy) | 740 | 16 | silently zeroed | ✅ |
| deep (reflection) | 981 | 15 | ✅ | ✅ |
| [mitchellh/copystructure](https://github.com/mitchellh/copystructure) | 3,149 | 91 | silently zeroed | stack overflow |

¹ Generated `Clone` assumes acyclic data; cyclic values are the reflection engine's territory.

Numbers from an Intel Core Ultra 9 285K; relative ordering is what matters. The competitor benchmarks live out-of-tree so this module stays dependency-free.

## Core Operations

- `deep.Diff(a, b) (Patch[T], error)` — computes the operations turning `a` into `b`. Changed `chan`/`func` values diff to a whole-value replace that shares the reference; the error return covers values the reflection engine cannot process.
- `deep.Apply(&target, patch, opts...) error` — applies a patch. Individual operation failures are collected: the returned error is an `*ApplyError` whose `Unwrap() []error` yields every failure; the remaining operations still apply. Pass `deep.WithLogger(l)` to route `log` operations to a specific `*slog.Logger`.
- `deep.Equal(a, b) bool` — deep equality, including unexported fields and cyclic values.
- `deep.Clone(v) T` — deep copy. The reflection path handles unexported fields and cycles; non-nil `chan` and `func` values are cloned as `nil`.

All four automatically dispatch to generated methods when they exist and fall back to reflection when they don't — including per-operation: a generated `Patch` method hands any operation it does not model (slice indexing, `move`/`copy`, strict map entries) to the reflection engine.

## Type-Safe Selectors

Selectors turn field accessors into JSON Pointer paths at compile time — no string literals to typo:

```go
namePath  := deep.Field(func(u *User) *string         { return &u.Name })
scorePath := deep.Field(func(u *User) *map[string]int { return &u.Score })
rolesPath := deep.Field(func(u *User) *[]string       { return &u.Roles })

deep.At(rolesPath, 0)            // "/roles/0"    — slice element
deep.MapKey(scorePath, "power")  // "/score/power" — map value
namePath.String()                // "/name"
```

Selectors work for unexported fields too — expose an accessor method returning the field's address and pass it to `Field`.

Map keys are RFC 6901-escaped automatically (`/` → `~1`, `~` → `~0`). Building paths by hand? Use `deep.EscapePathKey` / `deep.UnescapePathKey`.

## Building Patches

`Diff` derives patches from state; the builder constructs them explicitly:

```go
patch := deep.Edit(&user).
    With(
        deep.Set(namePath, "Alice Smith"),            // replace
        deep.Add(deep.MapKey(scorePath, "power"), 100),
        deep.Remove(deep.MapKey(scorePath, "legacy")),
        deep.Move(oldPath, newPath),
        deep.Copy(srcPath, dstPath),
    ).
    Log("update applied").              // structured log line during Apply
    Guard(deep.Eq(statusPath, "paid")). // whole patch is a no-op unless this holds
    Build()
```

`Edit`'s argument is used only for type inference; the builder produces a standalone `Patch[T]`, not a live view.

## Conditions

Conditions are data — they serialize with the patch and are enforced wherever it is applied, including remote peers.

Comparisons: `Eq`, `Ne`, `Gt`, `Ge`, `Lt`, `Le` • Membership: `In` • Structure: `Exists`, `Type` (`"string"`, `"number"`, `"boolean"`, `"object"`, `"array"`, `"null"`) • Text: `Matches` (regexp) • Combinators: `And`, `Or`, `Not`.

**Patch-level guard** — all-or-nothing, for state-machine transitions:

```go
patch := deep.Edit(&order).
    With(deep.Set(statusPath, "shipped")).
    Guard(deep.Eq(statusPath, "paid")).
    Build()
```

**Per-operation conditions** — individual ops skip independently:

```go
patch := deep.Edit(&invoice).
    With(deep.Set(paidAtPath, time.Now())).
    With(deep.Set(feePath, 25.0).If(deep.Gt(balancePath, 0.0))).
    With(deep.Set(notePath, "").Unless(deep.Exists(notePath))).
    Build()
```

A condition evaluating to **false** skips its operation (or, for a guard, the patch — `Apply` returns an error so the caller knows it did not run). A condition that **cannot be evaluated** — malformed value, bad path — is an error, not a silent skip.

## Struct Tags

| Tag | Effect |
| :--- | :--- |
| `json:"name"` | Field appears in paths as `/name` (the Go field name is accepted on apply as well) |
| `json:"-"` or `deep:"-"` | Invisible to deep: skipped by Diff, Equal **and Clone**; operations targeting it are silently ignored |
| `deep:"readonly"` | Operations targeting the field fail with an error (`log` operations are still allowed) |
| `deep:"atomic"` | Diffed as a single whole-value replace — no per-field/per-element operations |
| `deep:"key"` | Marks a slice element's identity field: the slice diffs by key (add/remove/modify per element) instead of by index, so reordering produces no operations |

```go
type Item struct {
    SKU string `deep:"key" json:"sku"`
    Qty int    `json:"qty"`
}
type Inventory struct {
    Items []Item `json:"items"` // diffs as "/items/<sku>", order-insensitive
}
```

**Embedded fields** are addressed by their type name (`/Meta/version` for an embedded `Meta`), matching how Go names them.

## Strict Mode (Optimistic Concurrency)

```go
strict := patch.AsStrict()
err := deep.Apply(&target, strict) // fails if any Old value no longer matches
```

Every `replace`/`remove` verifies the operation's `Old` value against the current state before writing. `Diff` fills `Old` automatically. Strictness survives JSON Patch interop: `ToJSONPatch` emits an RFC 6902 `test` op before each checked operation, and `ParseJSONPatch` folds them back into `Old` + `Strict`.

## Merging Patches

```go
merged := deep.Merge(base, other, resolver) // resolver may be nil: other wins
```

Operations are deduplicated by path; on conflict a custom `ConflictResolver` (`Resolve(path string, local, remote any) any`) decides, and the output is sorted by path for determinism.

## Patch Utilities

```go
patch.IsEmpty()       // no operations?
patch.Reverse()       // undo patch (swaps Old/New, add↔remove; log ops are dropped)
patch.WithGuard(cond) // returns a copy with a patch-level guard
patch.String()        // human-readable summary
```

## Serialization

**Native JSON** — `Patch[T]` marshals directly; compact keys keep the wire format small (`k`=kind, `p`=path, `f`=from, `o`=old, `n`=new, `if`/`un`=conditions):

```json
{"ops":[{"k":2,"p":"/name","o":"Alice","n":"Bob"}],"strict":true}
```

**RFC 6902 JSON Patch** — for interop with other tooling:

```go
jsonData, err := patch.ToJSONPatch()
// [{"op":"replace","path":"/name","value":"Bob"}]
restored, err := deep.ParseJSONPatch[User](jsonData)
```

Deep extensions: a leading `test` op on `/` with an `"if"` key carries the patch guard, per-op conditions ride as `"if"`/`"unless"` keys, `log` is a non-standard op, and strict `Old` checks map to standard `test` ops.

> **Note**: after any JSON round-trip, numbers in `Old`/`New` are `float64` (standard Go JSON behavior). Generated code coerces numerics automatically; be aware of it when inspecting operations directly.

## Observability

Embed `log` operations to emit structured trace messages during `Apply` — request-scoped loggers, test capture, tracing, all without touching your model types:

```go
patch := deep.Edit(&u).
    Log("starting update").
    With(deep.Set(namePath, "Alice Smith")).
    Log("update complete").
    Build()

logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
deep.Apply(&u, patch, deep.WithLogger(logger))
// {"level":"INFO","msg":"deep log","message":"starting update","path":"/"}
// {"level":"INFO","msg":"deep log","message":"update complete","path":"/"}
```

Without `WithLogger`, `slog.Default()` is used.

## deep-gen

```
deep-gen -type=TypeA,TypeB [-output file.go] [dir]
```

Generates `Patch`, `Diff`, `Equal` and `Clone` methods (plus internal helpers) for the named struct types. Output defaults to `<firsttype>_deep.go` in the target directory.

Rules worth knowing:

- **Every referenced struct needs generated code too.** If `Doc` has a `Detail` field (direct, embedded, pointer, or as a slice/map element), `Detail` must be listed in `-type` or generated by another run over the same package. Multiple runs per package are supported.
- **Types from other packages** (`time.Time`, generic instantiations, interfaces) are handled through `deep.Equal`/`deep.Clone`, which dispatch to the type's own methods when present. A small set of stdlib value types (`time.Time`, `time.Duration`, …) stays on a fast assignment path.
- **What generation can't express falls back to reflection** — per operation, transparently: channels/funcs, slice-index paths, `move`/`copy`, strict map-entry checks, conditions on nested paths.
- Generated files never need hand-editing and are safe to regenerate; deep-gen ignores its own previous output while parsing.

## CRDTs and Synchronization

The `crdt` package builds multi-writer synchronization on top of patches, ordered by hybrid logical clocks (`crdt/hlc`).

**`CRDT[T]`** — wraps any type in a concurrency-safe, convergent container:

```go
nodeA := crdt.NewCRDT(GameState{}, "node-a")
nodeB := crdt.NewCRDT(GameState{}, "node-b")

delta := nodeA.Edit(func(s *GameState) { s.Score = 10 }) // timestamped Delta[T]
nodeB.ApplyDelta(delta)                                  // idempotent, causally ordered

nodeA.Merge(nodeB)     // full-state merge, LWW per path
state := nodeA.View()  // snapshot copy
```

`CRDT[T]` is JSON-serializable (state + clock + per-path metadata survive the round-trip).

**Undo/redo** — `Reverse` applies a delta's inverse locally and returns a fresh-timestamped undo delta, safe to propagate; reversing the undo redoes:

```go
delta := node.Edit(func(d *Doc) { d.Title = "Draft" })
undo := node.Reverse(delta)
redo := node.Reverse(undo)
```

**Field-level types:**

```go
type Document struct {
    Title   crdt.LWW[string] // last-write-wins register: Set(v, ts)
    Content crdt.Text        // collaborative text: Insert/Delete/String, MergeTextRuns
}
```

**Standalone convergent containers** — `crdt.Counter` (increment/decrement), `crdt.Set[T]` (add-wins set) and `crdt.Map[K,V]` (LWW map), each with a commutative, idempotent `Merge`.

**How collections merge inside a `CRDT[T]`** — everything converges, but only the first two merge concurrent edits rather than picking a winner:

| Field type | Concurrent edits |
| :--- | :--- |
| `map[K]V` | Different keys both survive |
| `[]T` with `deep:"key"` on `T` | Different elements both survive; an element's fields merge independently. Order is not synchronized — replicas keep these in key order, so sort on read if order matters |
| `[]T` without a key | Whole-slice last-write-wins: one writer's version wins |
| `crdt.List[T]` | Concurrent insertions and deletions all survive, in an order every replica agrees on |

Prefer a map, a keyed slice, or a `List` for any collection edited concurrently.

**`crdt.List[T]`** — a sequence that merges rather than overwrites. Elements are placed relative to their neighbours rather than by index, so concurrent edits do not fight over positions:

```go
type Board struct {
    Tasks crdt.List[string] `json:"tasks"`
}

tasks = tasks.Insert(0, "write tests", node.Clock()) // position, value, clock
tasks = tasks.Delete(2, 1)                           // remove one at index 2
tasks.Items()                                        // []string in order
```

**Observing changes** — `OnChange` reports the operations that were actually applied, so a UI can redraw just what moved instead of diffing snapshots:

```go
cancel := node.OnChange(func(c crdt.Change[Doc]) {
    for _, op := range c.Patch.Operations {
        redraw(op.Path, op.New) // c.Source is local, remote, or merge
    }
})
defer cancel()
```

Only surviving operations are reported: a remote write that lost to a newer local one never appears. Callbacks run on the goroutine that made the change, with no lock held, so they may read or edit the replica.

**`crdt.Document`** — the same runs as `crdt.Text`, kept in a tree ordered by position rather than a slice. Finding a position and editing there costs the same whether the document holds a hundred runs or ten thousand:

```go
doc := crdt.NewDocument(node.Clock())
doc.Insert(0, "hello")     // position, value
doc.Delete(0, 2)
doc.MergeFrom(peer.Text()) // converges with a peer, Text or Document
```

| 50 edits to a document of | `Text` | `Document` |
| :--- | ---: | ---: |
| 500 runs | 1.2 ms | 39 µs |
| 2,000 runs | 4.5 ms | 25 µs |
| 8,000 runs | 22.4 ms | 58 µs |

`Text` slows down as runs accumulate; `Document` does not. Both serialize identically, so a replica running one converges with a replica running the other.

Inside a `CRDT[T]`, an edit to a `Document` costs about 4 µs whatever the document holds, and the delta it produces is the size of the edit rather than the size of the document. The wrapper copies its value before every edit to work out what changed, and a document is copied by sharing rather than duplicating — nothing in its index is ever changed in place, so the copy and the original cannot disturb each other.

**Syncing only what is missing** — a state vector says how much of each writer's output a replica holds, one number per writer rather than per character, so a peer sends only the difference:

```go
update := server.Since(client.StateVector()) // what the client lacks
client.Apply(update)
```

A 23-character edit to a 5,000-character document sends **151 bytes instead of 5,239**, and the state vector asking for it is 35. Deletions travel too, applying an update twice changes nothing, and syncing both directions converges. Hold a `Document` directly for a large collaborative document; a `Document` inside a `CRDT[T]` converges but does not bring its speed with it, because `Edit` copies and compares the value to work out what changed.

**Reclaiming history** — a replica remembers when every path was written and deleted, and a sequence keeps deleted elements as tombstones, so a long-lived replica carries more history than data. `Compact` discards it, down to a watermark you supply:

```go
// Older than anything still in flight: usually the oldest timestamp
// every peer has acknowledged.
node.Compact(watermark)
```

It reaches the `Text` and `List` values inside the replica too, changes nothing about what the replica holds, and emits no delta. A compacted replica still converges with one that has not compacted.

**Custom convergent types** — implement `crdt.Convergent` and a `CRDT[T]` merges your type instead of picking a winner, whether the change arrives as a delta or a full merge:

```go
func (s MySet) MergeFrom(other any) any {
    o, ok := other.(MySet)
    if !ok {
        return s
    }
    return union(s, o)
}
```

`MergeFrom` must be commutative, associative and idempotent — merging in any order, any number of times, has to reach the same value.

A type holding history of its own can also implement `crdt.Compactable`, and `Compact` will reach it:

```go
func (r Retired) CompactBefore(before hlc.HLC) any { /* drop entries older than before */ }
```

**`crdt/hlc`** — `Clock` (per-node: `Now`, `Update`, `Reserve`, `SetLatest`) and `HLC` timestamps (`Compare`, `After`) giving a total order across nodes without synchronized wall clocks.

## Architecture

A patch is a **flat operation list** — `[]Operation` with JSON Pointer paths — rather than a recursive tree. That makes patches trivially serializable, cheap to iterate, and composable (merging is stateless). Application is a **hybrid**: generated `applyOperation` methods handle the common shapes at native speed and report anything else as unhandled, at which point the reflection engine — which understands every Go shape, unexported fields included — takes over for that one operation. Both paths implement the same semantics; divergence is treated as a bug.

## Examples

Every directory under [`examples/`](examples/) is a runnable program (`go run ./examples/<name>`) built around one concept. The [examples guide](examples/README.md) describes what each one demonstrates and suggests a reading order.

**Core operations and patches**

| Example | Concept |
| :--- | :--- |
| [`config_manager`](examples/config_manager) | Diff, apply, and roll back with `Reverse` |
| [`state_management`](examples/state_management) | An undo stack built from reverse patches |
| [`nested_structs`](examples/nested_structs) | Nested and embedded struct paths; targeted field updates |
| [`slice_paths`](examples/slice_paths) | `At` for positional slice elements; how slice diffs are shaped |
| [`keyed_inventory`](examples/keyed_inventory) | `deep:"key"` — order-insensitive, identity-based slice diffs |
| [`struct_map_keys`](examples/struct_map_keys) | Non-string map keys in paths |
| [`move_copy_ops`](examples/move_copy_ops) | `Move` and `Copy` operations |
| [`multi_error`](examples/multi_error) | Error collection and `ApplyError` unwrapping |

**Tags, conditions, and safety**

| Example | Concept |
| :--- | :--- |
| [`atomic_config`](examples/atomic_config) | `deep:"readonly"` enforcement and `deep:"atomic"` whole-value updates |
| [`ignored_fields`](examples/ignored_fields) | `json:"-"` / `deep:"-"` — keeping secrets out of patches |
| [`policy_engine`](examples/policy_engine) | Patch-level `Guard` with composed conditions |
| [`conditional_ops`](examples/conditional_ops) | Per-operation `If` / `Unless` inside one patch |
| [`concurrent_updates`](examples/concurrent_updates) | Strict mode as optimistic locking |
| [`three_way_merge`](examples/three_way_merge) | `Merge` with a custom `ConflictResolver` |
| [`reflection_fallback`](examples/reflection_fallback) | Unexported fields and cyclic structures |

**Transport and interop**

| Example | Concept |
| :--- | :--- |
| [`json_interop`](examples/json_interop) | Native JSON, RFC 6902 export, and `ParseJSONPatch` ingest |
| [`http_patch_api`](examples/http_patch_api) | A patch-driven HTTP PATCH endpoint |
| [`websocket_sync`](examples/websocket_sync) | Broadcasting state deltas to clients |
| [`audit_logging`](examples/audit_logging) | Diffs as an audit trail, plus `OpLog` tracing |

**CRDTs**

| Example | Concept |
| :--- | :--- |
| [`crdt_sync`](examples/crdt_sync) | `CRDT[T]` delta exchange and convergence |
| [`crdt_undo_redo`](examples/crdt_undo_redo) | Distributed undo/redo via `Reverse` |
| [`crdt_containers`](examples/crdt_containers) | `Counter`, `Set` and `Map` |
| [`crdt_list`](examples/crdt_list) | `List[T]`, a sequence that merges concurrent insertions and deletions |
| [`crdt_observers`](examples/crdt_observers) | `OnChange` for incremental UI updates |
| [`crdt_compaction`](examples/crdt_compaction) | `Compact` for reclaiming the history a long-lived replica accumulates |
| [`crdt_document`](examples/crdt_document) | `Document`, a text CRDT indexed for editing rather than stored as a slice |
| [`crdt_sync_incremental`](examples/crdt_sync_incremental) | State vectors: sending only what a peer is missing |
| [`crdt_custom_type`](examples/crdt_custom_type) | `Convergent` and `Compactable`: making your own type merge instead of losing a writer |
| [`lww_fields`](examples/lww_fields) | Per-field `LWW[T]` registers resolving a write conflict |
| [`text_sync`](examples/text_sync) | Collaborative text with `crdt.Text` |

## License

Apache 2.0
