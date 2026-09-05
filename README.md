# deep: Type-Safe Diff, Patch, Clone and Sync for Go

`deep` is a comprehensive Go library for comparing, cloning, patching and synchronizing complex data structures. It combines **code generation** (fast, reflection-free implementations for your types) with a **reflection engine** (which handles everything else, including unexported fields) behind one uniform API.

## Key Features

- **Four core operations**: `Diff`, `Apply`, `Equal`, `Clone` — one call each, for any type.
- **Hybrid architecture**: `deep-gen` generates optimized fast paths; a reflection engine transparently handles types without generated code, unexported fields, and exotic shapes.
- **Recursive data**: values that reference themselves, or reach the same value twice, work on both paths — cycles are rebuilt, shared nodes stay shared (foreign pointers included), and diffs stay linear with `alias` operations rewiring the extra routes.
- **Compile-time safety**: type-safe field selectors replace brittle string paths.
- **Data-oriented patches**: a patch is a flat, serializable list of operations — portable, mergeable, reversible.
- **Conditional patching**: guards and per-operation conditions travel inside the patch.
- **Strict mode**: optimistic-concurrency checks that verify expected old values before writing.
- **Conditional writes**: apply a patch server-side and get a per-operation report of what applied, what its condition skipped, and what failed — narrowing the unit of conflict from the whole row to the fields a write depends on.
- **Checked on the wire**: a patch that travelled as JSON, gob or RFC 6902 applies to the same result, strict checks included — verified by a matrix of operation kind × field type × encoding.
- **Standard interop**: RFC 6902 JSON Patch import/export, including strict checks as `test` ops.
- **First-class CRDTs**: `CRDT[T]` wrapper with hybrid logical clocks, plus `LWW`, `Text`, `Counter`, `Set` and `Map` convergent types.

## Quick Start

```bash
go get github.com/brunoga/deep/v6
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
//go:generate go run github.com/brunoga/deep/v6/cmd/deep-gen -type=User .
```

```bash
go generate ./...
```

This writes `user_deep.go` next to your source. Commit it. Everything works without this step too — the reflection engine picks up any type automatically — generation just makes it much faster (see benchmarks below).

### 3. Use the API

```go
import deep "github.com/brunoga/deep/v6"

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
| **Diff** | 2,576 ns/op (63 allocs) | **562 ns/op** (16 allocs) | **4.6×** |
| **Apply** | 1,162 ns/op (42 allocs) | **90 ns/op** (2 allocs) | **12.9×** |
| **Equal** | 261 ns/op (5 allocs) | **100 ns/op** (2 allocs) | **2.6×** |
| **Clone** | 1,115 ns/op (17 allocs) | **187 ns/op** (5 allocs) | **6.0×** |

A patch that has been through JSON — a server applying client patches — stays on
the generated path too, decoding each value against its field's type as it
lands: 529 ns/op where falling through to reflection costs over a microsecond.

Diff produces the same operations in the same order every time. Go randomises
map iteration, so something has to impose an order, and a patch whose order
varies between runs cannot be logged, cached, compared or signed.

That order costs nothing measurable, because it is applied to the operations a
map field produced rather than to the map's keys. The keys are all of them; the
operations are only the entries that changed, which is usually a handful
whatever the map holds. A diff of a map with one changed entry allocates the
same 368 bytes whether the map has ten entries or ten thousand.

Deep copy compared with other clone libraries, same struct:

| Library | ns/op | allocs/op | Unexported fields | Cyclic structures |
| :--- | ---: | ---: | :---: | :---: |
| **deep (generated)** | **168** | 5 | ✅ | ✅ |
| [barkimedes/go-deepcopy](https://github.com/barkimedes/go-deepcopy) | 740 | 16 | silently zeroed | ✅ |
| deep (reflection) | 981 | 15 | ✅ | ✅ |
| [mitchellh/copystructure](https://github.com/mitchellh/copystructure) | 3,149 | 91 | silently zeroed | stack overflow |

Numbers from an Intel Core Ultra 9 285K; relative ordering is what matters. The competitor benchmarks live out-of-tree so this module stays dependency-free.

## Core Operations

- `deep.Diff(a, b) (Patch[T], error)` — computes the operations turning `a` into `b`. Changed `chan`/`func` values diff to a whole-value replace that shares the reference; the error return covers values the reflection engine cannot process.
- `deep.Apply(&target, patch, opts...) error` — applies a patch. Individual operation failures are collected: the returned error is an `*ApplyError` whose `Unwrap() []error` yields every failure; the remaining operations still apply. Pass `deep.WithLogger(l)` to route `log` operations to a specific `*slog.Logger`.
- `deep.Equal(a, b) bool` — deep equality, including unexported fields and cyclic values.
- `deep.Clone(v) T` — deep copy. The reflection path also handles unexported fields; non-nil `chan` and `func` values are cloned as `nil`.

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
patch := deep.NewPatch[User]().
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
patch := deep.NewPatch[Order]().
    With(deep.Set(statusPath, "shipped")).
    Guard(deep.Eq(statusPath, "paid")).
    Build()
```

**Per-operation conditions** — individual ops skip independently:

```go
patch := deep.NewPatch[Invoice]().
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

## Conditional Writes

A patch carries its own preconditions, so it can be applied where the data
lives rather than round-tripped through the writer. That changes what counts as
a conflict.

Whole-payload optimistic concurrency conflicts when *anything* in the row
changed. A conditional patch conflicts only when the fields its conditions name
changed, so two writers touching unrelated fields stop knocking each other back:

```go
res, err := deep.ApplyWithResult(&row, patch,
    deep.WithAllowedPaths("/title", "/price", "/stock"))
if err != nil {
    return err              // guard rejected, path refused, or an operation failed
}
if !res.AllApplied() {
    return retry(res)       // a condition no longer holds
}
commit(row)
```

[`ApplyWithResult`](https://pkg.go.dev/github.com/brunoga/deep/v6#ApplyWithResult)
behaves exactly as `Apply` — operations in order, each condition seeing what the
ones before it left — but returns an `*ApplyResult` with one outcome per
operation: `StatusApplied`, `StatusSkipped` or `StatusFailed`. The distinction
matters because a skipped operation is not an error: `Apply` returns nil whether
a conditional operation ran or not, so on its own it cannot tell a caller
whether its conditional write landed.

`Patch.Guard` rejects a whole patch with `ErrGuardNotMet`, which is the direct
analogue of a compare-and-swap. `WithAllowedPaths` confines a patch that arrived
from elsewhere to a set of prefixes, checking `From` as well as `Path` so a
`move` or `copy` cannot read outside them; an operation that reaches further
voids the whole patch with `ErrPathNotAllowed`. Struct tags remain the way to
express restrictions that belong to the type rather than to one caller —
`deep:"-"` to hide a field, `deep:"readonly"` to make writing it an error.

Two things to get right:

- **A condition must cover the read set, not just the write set.** Anything a
  writer read in order to compute its new value has to appear in the condition,
  or a concurrent change to it is a silently lost update. Blind writes need no
  condition and never conflict; a read-modify-write needs one on every field it
  read.
- **Retries are only free if the operations are idempotent.** `add` on a slice
  appends, so a lost response plus a client retry gives two entries. Either make
  the operation self-guarding with a condition or carry an idempotency key. For
  genuinely commutative updates — counters, sets, registers — the [CRDT
  types](#crdts-and-synchronization) remove the retry entirely instead of
  narrowing it.

`benchmarks` below and [`examples/conditional_writes`](examples/conditional_writes)
both work through a store holding serialized rows.

### Contention

`BenchmarkContention` runs both strategies against one shared row, with a
jittered 200µs round trip standing in for the network — without it a client's
read and its write land nanoseconds apart and compare-and-swap never sees an
intervening commit. Writers take eight independent counters in turn, so at eight
writers or fewer no two want the same field:

| Concurrent writers | CAS retries/op | Conditional retries/op | Throughput |
| ---: | ---: | ---: | ---: |
| 2 | 0.94 | **0** | 2.0× |
| 4 | 2.74 | **0** | 3.7× |
| 8 | 6.35 | **0** | 7.5× |
| 16 | 13.0 | 0.85 | 8.0× |
| 32 | 21.6 | 2.09 | 9.9× |

Up to eight writers the conditional patches never retry at all: every conflict
compare-and-swap paid there was one its granularity invented. Past eight,
writers start sharing a counter and some conflicts become real for both — the
conditional patch still takes about a tenth as many.

Neither strategy backs off, which real clients do; backoff trades retries for
latency rather than removing conflicts, so read the ratio rather than the
absolute numbers.

```
go test -run=XXX -bench=Contention -benchtime=2000x
```

## What Survives the Wire

Everything, checked. `TestWireFidelity` is a matrix of operation kind × field
type × encoding — native JSON, gob, RFC 6902 — and every mutation survives
every encoding, strict checks included:

| | native JSON | gob | RFC 6902 |
| :--- | :---: | :---: | :---: |
| Every operation kind and field type round-trips | ✅ | ✅ | ✅ |
| Strict `Old` checks survive | ✅ | ✅ | as `test` ops |
| nil distinguished from empty | ✅ | ✅ | ✅ |
| Clearing a pointer field | ✅ | ✅ | ✅ |
| Reference sharing (`alias`) | ✅ | ✅ | downgraded to `copy` |
| `gob.Register` calls required | — | **none** | — |

The mechanism is that a decoded operation does not decode its values. `Old` and
`New` arrive as [`RawValue`] — the encoded bytes, kept — and are decoded at
apply time against the type of the field the operation actually addresses, so
an int field receives an int and a struct field a struct rather than the
float64 and `map[string]any` a blind decode produces. Inspecting an operation
yourself, use [`ValueAs`]:

```go
if price, ok := deep.ValueAs[int](op.New); ok { ... }
```

Gob carries operations as their JSON form, which is why it needs no
registrations and has none of gob's interface-field constraints. Operation
kinds are strings on the wire (`"k":"replace"`); patches stored by v5, which
wrote integers, are still read.

[`RawValue`]: https://pkg.go.dev/github.com/brunoga/deep/v6#RawValue
[`ValueAs`]: https://pkg.go.dev/github.com/brunoga/deep/v6#ValueAs

## Merging Patches

`deep.Merge(base, other, resolver)` combines two patches. Operations are matched
by path; when both write the same path the resolver decides the value, and
without one `other` wins.

Paths that are not equal can still collide. An operation on `/user` and one on
`/user/name` are not independent, and keeping both produced a patch that could
not be applied — removing `/user` and then writing `/user/name` fails on the
path that is no longer there. When the two sides disagree that way the operation
from `other` wins and the one it encloses, or is enclosed by, is dropped. The
resolver is not consulted for these, because there is no single path at which to
ask. An ancestor and a descendant from the *same* patch are left alone: that is
a legitimate sequence, and the output ordering applies them in order.

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
patch := deep.NewPatch[User]().
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
- **Recursive and shared types are detected and handled.** See below.

## Protocol Buffers

Do not point the generic machinery at protoc-generated structs without this: a
message's Go struct carries the proto runtime's internal state, so
field-by-field equality reports two equal messages unequal once either has been
marshaled, a diff emits operations for the runtime's bookkeeping, and a
reflection-made clone crashes the runtime on its next `Marshal`.

The companion module `github.com/brunoga/deep/proto` fixes all of it with one
call — it is a separate module so that only proto users take the protobuf
dependency:

```go
import deepproto "github.com/brunoga/deep/proto"

func main() {
    deepproto.Register()
}
```

Register installs a type family claiming every `proto.Message`. Inside that
boundary the proto runtime's own machinery is used — `proto.Equal`,
`proto.Clone`, `protoreflect` for diffing and applying, protojson on the wire —
and outside it nothing changes: messages sit in ordinary structs, patches carry
ordinary operations, strict checks and `ApplyResult` work as they do anywhere,
and paths address message fields by their protojson names
(`/details/fields/price/numberValue`). Oneofs diff as remove-old-case plus
add-new-case; messages with differing unknown fields fall back to a whole-value
replace, since no per-field path can describe bytes the schema cannot name.

Conditions look inside messages too: a `Guard` or an operation's `If` may use
a path like `/details/fields/stock/numberValue`, resolved by `protoreflect`
with the same protojson names — so a conditional write can hinge on a field
deep inside the proto row it is updating.

## Custom Behaviour per Type

A type can carry its own behaviour by implementing a method — `Equal`, `Clone`,
`Diff` or `Copy` — and both engines honour it. For a type whose method set you
do not control, register a function instead:

```go
deep.RegisterCustomEqual(func(a, b money) bool { return a.Cents == b.Cents })
deep.RegisterCustomClone(func(src money) (money, error) { return src, nil })
deep.RegisterCustomDiff(func(a, b ver) (deep.Patch[ver], error) { ... })
```

Registration is global and applies to every subsequent operation, so do it
during initialisation. It affects the reflection engine: generated code has its
comparison and copying inlined and will not consult the registry for a type it
handles itself, so prefer a method where you can define one — both paths honour
that.

## Recursive and Shared Data

A value can reference itself — a tree with parent pointers, a graph, a linked
list — or reach the same value by two different routes. Both engines handle
both, with the same behavior.

`Clone` copies a value reached more than once exactly once, and points every
reference at that one copy. That holds for pointers to your own structs and for
foreign pointers alike: a `*time.Time` held by a field and a map entry clones
into one copied `time.Time` with both routes pointing at it. A cycle is rebuilt
as a cycle in the copy, closing on the copy rather than on the original:

```go
n := &Node{Name: "n"}
n.Next = n
n.Peers = []*Node{n, n}

c := n.Clone()
c.Next == c        // true — the cycle was rebuilt
c.Peers[0] == c    // true — and every route agrees
c.Next == n        // false — nothing is shared with the original
```

`Equal` follows a cycle only until it repeats, so two values with matching
cyclic shapes compare equal instead of recursing forever.

`Diff` descends into a pair of values once, at the first path that reaches it —
shared structure can be reachable by exponentially many paths, so repeating the
operations at each one is not an option. Every later route to a changed value
becomes a single **alias** operation instead: `{Kind: OpAlias, Path: to, From:
from}` makes `to` hold the same object `from` holds, where `OpCopy` would make
an independent deep copy. Applying the patch lands the right values at every
route and rebuilds the sharing — whether the target still shared the value (a
clone) or was rebuilt without sharing (decoded from JSON, say). Alias ops
survive the native flat JSON encoding; the RFC 6902 export maps them to
`copy`, which reproduces the values but not the sharing, since JSON values have
no identity.

deep-gen works out which types need any of this by looking for two facts in the
package's type graph: cycles, and reference classes reachable by more than one
route (two `*Meta` fields, a `*time.Time` next to a `map[string]*time.Time`, an
interface next to any pointer). Types where neither is possible — the common
case — generate exactly the code they did before, with no bookkeeping and no
extra allocation. Types that qualify thread a `deep.CloneMemo`, `deep.VisitSet`
or `deep.DiffMemo`, still around 6× faster than the reflection engine on the
same value; opaque fields are handed to `deep.CloneShared`, which runs the
reflection engine against the same memo, so sharing survives even across the
generated/reflection boundary.

One boundary remains, shared by both engines: slice and map *headers* are
copied per occurrence — two fields holding the same `[]int` clone into two
independent slices. Identity is tracked through pointers.

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

### Presence

Awareness carries what the people editing alongside you are doing right now —
cursor, selection, name, colour — deliberately outside the document. A cursor
position is not an edit: it must not merge into the text, survive a reload, or
appear in the history, and a peer that closes its laptop should stop being drawn
rather than leave a cursor behind.

```go
me := crdt.NewAwareness[Cursor]("alice", crdt.WithTTL[Cursor](10*time.Second))

me.OnChange(func(c crdt.PresenceChange[Cursor]) {
    // c.Kind is PresenceJoined, PresenceUpdated or PresenceLeft
})

broadcast(me.SetLocal(Cursor{Index: 12}))  // also the heartbeat
me.Apply(updateFromPeer)                   // last write wins, per peer
me.States()                                // everyone currently present
```

State is last-write-wins per peer, ordered by a counter that peer controls, so
updates may arrive out of order or twice without harm. A peer that says nothing
for the timeout is dropped locally, with no coordination: because presence is
ephemeral, a peer dropped too eagerly simply reappears with its next update.

### Binary encoding

`Update` and `StateVector` implement `encoding.BinaryMarshaler` and
`BinaryUnmarshaler`, so gob and anything else that looks for those uses the
compact format without being asked.

An update is mostly identifiers, and identifiers are mostly repetition: every
run carries a clock, every clock carries the node id that issued it, and
neighbouring wall times differ by microseconds. JSON has no way to say "the same
node as before" or "three microseconds later". The binary format says both —
node ids are interned into a table and referred to by index, wall times are
stored as deltas:

| Runs in the update | JSON | Binary | |
| ---: | ---: | ---: | ---: |
| 10 | 1,336 B | 201 B | 6.6× |
| 100 | 12,839 B | 1,480 B | 8.7× |
| 1,000 | 129,150 B | 14,605 B | 8.8× |

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
| [`conditional_writes`](examples/conditional_writes) | Server-side conditional writes: narrowing the unit of conflict, and reading `ApplyResult` |
| [`concurrent_updates`](examples/concurrent_updates) | Strict mode as optimistic locking |
| [`three_way_merge`](examples/three_way_merge) | `Merge` with a custom `ConflictResolver` |
| [`reflection_fallback`](examples/reflection_fallback) | Unexported fields and cyclic structures with no generated code at all |
| [`cyclic_graph`](examples/cyclic_graph) | Self-referencing types: cycles rebuilt, shared nodes kept shared, alias ops in diffs |

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
| [`crdt_presence`](examples/crdt_presence) | Awareness: who else is editing, where their cursor is, and how they stop being drawn |
| [`crdt_sync_incremental`](examples/crdt_sync_incremental) | State vectors: sending only what a peer is missing |
| [`crdt_custom_type`](examples/crdt_custom_type) | `Convergent` and `Compactable`: making your own type merge instead of losing a writer |
| [`lww_fields`](examples/lww_fields) | Per-field `LWW[T]` registers resolving a write conflict |
| [`text_sync`](examples/text_sync) | Collaborative text with `crdt.Text` |

## Migrating from v5

- **Import path**: `github.com/brunoga/deep/v6`. Regenerate with deep-gen —
  required, since generated code imports the module by path.
- **Serialized patches**: v6 writes operation kinds as strings and keeps values
  encoded until apply. v6 reads patches stored by v5; v5 cannot read patches
  written by v6.
- `deep.Edit(nil)` → `deep.NewPatch[T]()`.
- `deep.Status` → `deep.OpStatus`.
- The generator bookkeeping (`CloneMemo`, `VisitSet`, `DiffMemo`,
  `CloneShared`, `ApplyOpReflection`, …) moved to `deep/gen`; regenerating is
  the whole migration.
- An operation decoded from the wire now carries `RawValue` in `Old`/`New`
  rather than float64s and `map[string]any`; use `deep.ValueAs[T]` to read one.
- `deep.Clone` of a value containing a non-nil chan or func now clones it as
  nil, as always documented — previously the whole result was silently zero.
- Requires Go 1.27.

## License

Apache 2.0
