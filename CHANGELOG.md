# Changelog

All notable changes to this project are documented here, newest first.

> **Releasing:** update this file as part of every release — add the new
> version's entry before tagging, so the tag, the GitHub release notes, and this
> file always agree.

## Unreleased

### Fixed

- **A `Convergent` type of the caller's own was overwritten by a delta instead
  of merged.** Such a type skips the clock filter, because it settles
  concurrency itself — but only the sequence types this package defines were
  actually handed the operation, so anything else was then overwritten by
  whichever delta arrived last. Both writers' changes were meant to survive and
  neither reliably did, and the two replicas disagreed about which. A type
  implementing `Convergent` is now merged through that interface whether the
  change arrives as a delta or as a full merge. Only `CRDT.Merge` handled these
  types before, which is why the tests did not catch it.

### Documentation

- The `crdt` package documentation covers what the package has grown into:
  `Document` alongside `Text`, `List`, watching for changes with `OnChange`,
  syncing incrementally, and reclaiming history with `Compact`.
- Added `examples/crdt_custom_type`, showing a type of your own merging through
  `Convergent` and dropping its own history through `Compactable` — the example
  that turned up the bug above.
- Corrected the `Document` figures in the README, which predated the change to a
  persistent index: an edit made directly on a document costs a little more than
  it did, and an edit made through a `CRDT` costs far less.

## v5.10.1

### Performance

- **An edit to a `crdt.Document` inside a `CRDT[T]` no longer costs what the
  document weighs.** The wrapper copies its value before every edit, so that it
  has something to compare the result against; copying a document meant
  duplicating its whole index, and describing the edit afterwards meant reading
  every run twice over.

  The index is now persistent — an edit builds new nodes along the path it
  touches and leaves the rest alone — so copying a document is sharing its root
  rather than walking it, and the document it was copied from goes on describing
  what it described before. A document also keeps its own state vector up to
  date and notes what it changes as it changes, so describing an edit is reading
  that note rather than rediscovering it by comparison.

  | Per edit, document inside a `CRDT` | 200 runs | 3,000 runs |
  | :--- | ---: | ---: |
  | Before | 124 ms / 50 | 425 µs |
  | Now | **4.2 µs** | **4.2 µs** |

  The record is only trusted when it accounts for the whole difference, which is
  checked rather than assumed: applying it to where the other document stood has
  to land exactly where this one stands, for every writer and for the deleted
  characters both. Anything else is compared the general way.

### Fixed

- **A local insertion just after a merge could order the document differently
  from every other replica.** Runs sharing an anchor are ordered by identifier,
  so a run written elsewhere and merged in can belong between the anchor and a
  newly typed run; placing the new run at the cursor put it first regardless.
  The ordering is now re-derived when that can have happened, which is only at
  the spot a merge touched.

## v5.10.0

### Added

- **Incremental sync for `crdt.Document`.** Replicas exchanged whole documents,
  which costs the size of the document however little changed.
  `Document.StateVector` reports how much of each writer's output a replica
  holds — one number per writer, not per character — and `Document.Since`
  returns only what a peer holding that state vector is missing, trimming a run
  the peer holds part of to the part it does not. `Document.Apply` integrates
  the result.

  A 23-character edit to a 5,000-character document now sends 151 bytes instead
  of 5,239, and the state vector asking for it is 35. Applying an update twice
  changes nothing, deletions travel even though the peer already holds the
  characters, and syncing in both directions reaches what a full merge would.

- **A `crdt.Document` inside a `CRDT[T]` now produces deltas the size of the
  edit, and produces them quickly.** The engine compared two indexes
  structurally to work out what an edit had changed, which walked both trees and
  put the result into the delta node by node. A `Document` now reports the
  change itself, as the part the other side is missing — the same question
  [Document.Since] answers for a peer.

  | 50 edits to a 500-run document inside a `CRDT` | Before | After |
  | :--- | ---: | ---: |
  | Time | 124 ms | 4.6 ms |
  | Delta for a one-character edit | whole document | 200 bytes |

  Operations aimed at a value that merges itself also skip the clock filter and
  are handed to that value rather than applied generically. Both replicas write
  the same path when they edit the same document, so last-write-wins would have
  kept only one side of a concurrent edit.

### Fixed

- **Merging could order a document differently on two replicas.** A run
  anchored partway into another was placed after the whole of it rather than
  after the character it follows, because runs were divided only where a run
  began or ended, never where something anchored to one. Replicas exchanging
  whole documents hid this, since each carried the other's boundaries; it
  appeared as soon as they exchanged only what was missing. Merging now divides
  a run at any character something anchors to.
- **A type supplying its own `Diff` method produced no operations at all.** The
  engine used the custom differ and then flattened the result to nothing — the
  change vanished from the patch without an error, so `Diff` followed by `Apply`
  silently lost it. A custom patch describes a change in terms only its own type
  understands, which the flat operation form cannot carry, so it now falls back
  to recording what the value became.
- `deep.Clone` of a `crdt.Document` no longer walks the index node by node; a
  `Copy` method rebuilds from the runs instead, which is what a `CRDT` snapshot
  of its value goes through.

## v5.9.0

### Added

- **`crdt.Document`**, a text CRDT indexed for editing. It holds the same runs
  as `crdt.Text` and serializes identically — a replica running one converges
  with a replica running the other — but keeps them in a balanced tree ordered
  by position rather than a slice. Finding a position and editing there cost
  logarithmic rather than linear time in the number of runs, and an edit does
  not copy the document.

  Runs accumulate as edits scatter around a document, which is where a `Text`
  slows down:

  | 50 edits to a document of | `Text` | `Document` |
  | :--- | ---: | ---: |
  | 500 runs | 1.2 ms | 8.7 µs |
  | 2,000 runs | 4.8 ms | 5.9 µs |
  | 8,000 runs | 22.3 ms | 7.4 µs |

  Twenty thousand insertions at random positions take 7 ms, about 350 ns each,
  and the cost per edit does not grow: 117 ns at five hundred runs, 112 ns at
  sixteen thousand.

  The tree is only an index. Which order the runs are in is decided the same way
  as in `Text`, by the run each is anchored to, and merging goes through
  `MergeTextRuns` — so the two cannot disagree about ordering, and the proven
  implementation stays the reference. A property test drives both through two
  hundred randomized edit sequences and requires them to produce the same text
  at every step.

  Hold a `Document` directly for a large collaborative document. One inside a
  `CRDT[T]` converges but does not bring its speed with it: `Edit` copies the
  value and compares the copy to work out what changed, which costs time
  proportional to the size of the document however small the edit. That is the
  wrapper's doing, and a `Text` pays it too.

## v5.8.0

### Performance

- **A `crdt.Text` no longer stores one run per keystroke**, which is what made a
  document cost about a hundred bytes per character and made every edit walk the
  whole thing. Two changes together:

  Identifiers for sequence elements now come from `Clock.ReserveSequence`, which
  counts on from where it left off instead of following the wall clock. The
  existing `Clock.Reserve` begins a new logical range whenever physical time has
  moved on — which it always has by the next keystroke — so no two identifiers
  it handed out were ever adjacent and consecutive characters could never be
  recognized as belonging together.

  Each run also carries its rune count, and runs are capped in length. Positions
  are counted in runes, so nearly every operation needed that count, and
  counting it meant walking the string — for a document held as one long run,
  walking the whole document. Capping the length bounds the copy that joining
  two runs performs, which is what turns typing from quadratic into linear.

  | Workload | v5.6.1 | v5.7.0 | Now |
  | :--- | ---: | ---: | ---: |
  | Type 2,000 characters | 3.07 s | 239 ms | 2 ms |
  | Type 100,000 characters | — | 8.1 s | 142 ms |
  | Read a 1,000-character document | 587 µs | 25 µs | 0.2 µs |
  | Size of a 100,000-character document | ~9.9 MB | ~9.9 MB | 103 KB |

  A document now costs about 1.03 bytes per character on the wire, and typing
  scales roughly linearly with its length.

### Added

- **`CRDT[T].Compact`** discards the bookkeeping a replica accumulates. A
  replica remembers when every path was written and when every path was
  deleted, so it can recognize a stale update, and a sequence keeps deleted
  elements as tombstones so a concurrent insertion still has an anchor. Neither
  shrinks on its own: a map emptied of five hundred keys still held five hundred
  entries, and a document typed and retyped held two hundred runs behind nine
  visible characters. Compaction reclaimed 99% of a long-lived replica's stored
  state in the new example, with its value unchanged.

  What the record protects against is an old update arriving late, so dropping
  it is only safe for changes every replica has already seen — the caller passes
  that watermark, since only the application knows who its peers are. A replica
  that has compacted still converges with one that has not.

  `Compact` reaches the `Text` and `List` values inside a replica as well, and
  emits no delta: discarding history does not change what the replica holds.
- **`crdt.Text.Compact` and `crdt.List[T].Compact`** do the same for a sequence
  on its own. A tombstone that something is still anchored to is kept whatever
  its age, since removing it would change this replica's ordering without
  changing any other's.
- **`crdt.Compactable`**, the interface behind that, so a type of your own can
  take part.
- **`hlc.Clock.ReserveSequence`** allocates identifiers for the elements of a
  sequence. Unlike `Reserve` it does not follow the wall clock, so consecutive
  calls return adjacent blocks that a sequence can hold as one run. The
  identifiers are unique and totally ordered but carry no causality; use `Now`
  for a timestamp.
- **`crdt.TextRun.N`** carries the run's rune count. A run without one — an
  older document, or a literal built by hand — is counted on demand, so existing
  documents keep working.

## v5.7.0

### Performance

- **Editing a `crdt.Text` is roughly 13x faster**, and reading one is around 25x
  faster. Every operation derived the document order from scratch, several
  times per keystroke — locating the insertion point, splitting the run, and
  normalizing each rebuilt the whole ordering tree — so the cost of typing a
  character grew with the size of the document.

  The order is now derived once per operation, and not at all when the runs are
  already in it, which is the case for every `Text` this package produces.
  Confirming an existing order takes one pass where deriving it costs a map of
  every run plus a sort. The walk itself no longer probes every character of
  every run, and no longer recounts a run's length once per character.

  | Workload | Before | After |
  | :--- | ---: | ---: |
  | Type 1,000 characters | 715 ms | 52 ms |
  | Type 2,000 characters | 3.07 s | 239 ms |
  | 800 insertions mid-document | 189 ms | 14 ms |
  | Read a 1,000-character document | 587 µs | 25 µs |

  Editing remains linear in document size per operation; this removes the
  repeated work around it rather than changing the algorithm. Serialized size is
  unchanged — a document still carries one run per edit, which is the next thing
  to address.

### Added

- **`CRDT[T].OnChange`** reports each change as it is applied, carrying the
  operations that actually took effect and where the change came from (local
  edit, remote delta, or merge). A consumer can redraw exactly what moved
  instead of diffing snapshots, and never sees an operation that lost conflict
  resolution. Callbacks run on the goroutine that made the change with no lock
  held, so they may read or edit the replica.
- **`crdt.Text.Len`** returns the number of visible characters, in runes — the
  same unit `Insert` and `Delete` take positions in.
- **`crdt.List[T]`**, a sequence that merges. An ordinary slice inside a
  `CRDT[T]` is synchronized as one value, so concurrent edits resolve by
  last-write-wins and one writer's version of the whole slice wins. A `List`
  places elements relative to their neighbours rather than by index, so
  concurrent insertions and deletions from different replicas all survive and
  every replica converges on the same order.
- **`crdt.Convergent`**, the interface behind that behavior, is now exported.
  A type implementing `MergeFrom(other any) any` is applied unconditionally
  rather than filtered by last-write-wins, and two copies are merged rather
  than one replacing the other — so a data type of your own can plug into the
  same machinery. `Text` and `List` implement it.

### Fixed

- **A patch that had been through JSON could not be applied to a slice or map
  field.** Decoded values arrive as the generic shapes the JSON decoder
  produces (`[]any`, `map[string]any`), and only object-to-struct conversion
  was handled, so an operation carrying a decoded array silently failed to
  apply — including any delta touching a `crdt.Text` field. Composite values
  are now rebuilt into the target type.
- A `Convergent` value that is not addressed element by element was never
  recognized during `Merge`: the search for a self-merging value started one
  level above the operation's own path, which only works for the keyed slices
  `Text` and `List` happen to be.

## v5.6.1

### Fixed

- **`crdt.Text` merging still corrupted non-ASCII text.** v5.6.0 converted
  editing to runes but left `MergeTextRuns` counting the split boundaries it
  introduces in bytes, so merging two replicas that had split the same
  multi-byte run at different points sliced through a character: the result was
  invalid UTF-8 and the two replicas disagreed about it. Merging now counts
  runes like the rest of the type. Editing a document on one replica was
  correct in v5.6.0; merging concurrent edits to non-ASCII text was not.

## v5.6.0

### Behavior changes

- `crdt.Text` positions and lengths are counted in **runes**, not bytes. Code
  that computed positions from `len(string)` must count runes instead
  (`utf8.RuneCountInString`). ASCII-only documents are unaffected, including
  ones already persisted.
- Replicas keep keyed slices in key order. Element order in a keyed slice was
  never synchronized state, so this replaces an order that depended on delta
  arrival with one every replica agrees on. Sort on read, or carry an explicit
  ordering field, when a particular order matters. `deep.Apply` outside the
  `crdt` package is unchanged.

### Fixed

- **`crdt.Text` corrupted non-ASCII text.** Positions and lengths were counted
  in bytes while character IDs advance per character, so a split could land
  inside a multi-byte sequence: inserting into `"héllo"` produced invalid
  UTF-8, and deleting an emoji left its trailing bytes behind. Text is now
  rune-based throughout.
- **Replicas holding a keyed slice could diverge.** `Apply` appends a new
  element at the end, so after concurrent additions the order depended on which
  delta arrived first and two replicas with identical elements disagreed. Since
  element order in a keyed slice is not synchronized state — `Diff` emits
  nothing when one is merely reordered — replicas now keep keyed slices in key
  order, which is the same on every replica.
- **`NewCRDT` aliased the value it was given** instead of copying it, so a
  caller mutating its own value reached inside the replica behind the mutex,
  and two replicas seeded from one value shared state.

### Documentation

- Added `examples/README.md`: a guide to all 24 examples with a suggested
  reading order and what each one demonstrates, so browsing into the directory
  lands on something other than a list of names.
- `TestExamplesAreDocumented` asserts that every example is linked from both the
  root README and the examples guide, and that neither links to an example that
  no longer exists.

## v5.5.0

### Fixed

- **`Diff` followed by `Apply` corrupted unkeyed slices.** The reflection engine
  describes a slice change as an edit script whose indices refer to positions in
  the original slice, interpreted as a batch with a moving cursor. Flattening
  that script into the public operation list produced operations that were then
  applied one at a time against an already-mutated slice, so every index after a
  structural change pointed at the wrong element — `[a b c]` → `[a B c]`
  produced `[a c B]`, and a reorder failed with an index-out-of-bounds error.
  Generated code was unaffected, so this was also a divergence between the two
  paths. A slice patch containing a structural change to an unkeyed slice now
  flattens to a single whole-slice replace; keyed elements address by key rather
  than position and keep their fine-grained operations, as do same-length
  in-place replacements. The internal batch apply path is unchanged.
- **Applying a value of the wrong type panicked** instead of returning an error.
  Since patches routinely arrive as untrusted JSON from a peer, a malformed
  value could crash the process. Type mismatches are now reported as errors and
  the target is left untouched.

### Documentation

- Audited every example against real behavior and corrected seven that had
  drifted: `concurrent_updates` cited a `WithStrict(true)` API that does not
  exist; `multi_error` never demonstrated the type mismatch it claimed;
  `audit_logging` applied a different patch than the audit trail it printed;
  `keyed_inventory` never modified a keyed element; `lww_fields` merged disjoint
  fields and so never exercised last-write-wins; `move_detection` demonstrated a
  hand-built `Move` rather than detection of one (renamed `move_copy_ops`, now
  covering `Copy` as well); `json_interop` gained `ParseJSONPatch` ingest.
- Added examples for concepts nothing exercised: per-operation `If`/`Unless`
  (`conditional_ops`), positional slice paths (`slice_paths`), nested and
  embedded struct paths (`nested_structs`), ignored fields (`ignored_fields`),
  unexported fields and cyclic structures (`reflection_fallback`), the
  `Counter`/`Set`/`Map` containers (`crdt_containers`), and distributed
  undo/redo (`crdt_undo_redo`).
- Eleven examples shipped generated code with no `//go:generate` directive, so
  `go generate ./...` could not reproduce it; every example with generated code
  now carries its directive.
- Rewrote the README as complete, verified documentation: every code snippet is
  compile-checked, the benchmark tables are measured and reproducible, and all
  24 examples are indexed by area.

## v5.4.0

### Behavior changes

- **Op-level condition evaluation errors now surface.** An `op.If`/`op.Unless`
  that cannot be evaluated previously skipped its operation silently; it now
  contributes an error to `Apply`'s result, matching the patch-level `Guard`. A
  condition evaluating to *false* still skips the operation.
- **Strict patches emit RFC 6902 `test` operations** in `ToJSONPatch`, and
  `ParseJSONPatch` folds a test-with-value entry back into the following
  operation's `Old`, restoring `Strict`. Previously `AsStrict().ToJSONPatch()`
  silently dropped all strict semantics.
- **Generated `Clone` deep-copies opaque types** (`crdt.LWW[[]int]`, `any`,
  pointers to types from other packages) instead of sharing their inner
  references. `time.Time`, `time.Duration`, `time.Month` and `time.Weekday`
  remain on the fast assignment path.
- **Map and keyed-slice keys are RFC 6901-escaped** in diff paths, in both the
  engine and generated code, so keys containing `/` or `~` round-trip.

### Added

- `deep.EscapePathKey` and `deep.UnescapePathKey` for building paths by hand.

### Fixed

Generated code, where a consistency audit against the reflection engine found
eleven divergences:

- Embedded fields were dropped entirely — `Clone` zeroed them, `Equal` ignored
  them, `Diff` emitted nothing. They are now addressed by type name.
- A `*Struct` field transitioning nil→set or set→nil produced an empty diff.
- A keyed-slice element whose key survived but whose content changed produced an
  empty diff; matched keys now sub-diff.
- `deep:"atomic"` on a non-comparable field generated code that did not compile;
  atomic composites now diff as one whole-value replace.
- `OpRemove` on a deeper map path deleted the outer entry.
- Entry-level operations on pointer-valued maps errored instead of applying.
- Strict map-entry operations applied without verifying `Old`.
- `evaluateCondition` panicked on a non-string value for `type`/`matches` and
  errored on nested paths; it now matches the engine and falls back to
  `condition.Evaluate` for anything outside the fast path.
- Two `deep-gen` runs over one package collided on a shared emitted helper.

Engine:

- `mapPatch` and keyed `slicePatch` path flattening emitted unescaped keys, so
  the engine's own diff→apply round-trip failed on keys containing `/` or `~`.

## v5.3.1

### Fixed

- **`deep-gen` generated invalid code for field types from other packages**
  ([#46](https://github.com/brunoga/deep/issues/46)). Pointer fields were only
  rendered when the pointee was a bare identifier, so `*time.Time` produced an
  empty type assertion — `op.Old.()` — and the file did not compile. The same
  gap covered `[]time.Time` and `map[string]time.Time` (silently rendered as
  `[]any`/`map[string]any`), `[]*pkg.T`, generic instantiations, and fixed-size
  arrays; a plain `time.Time` field named its type correctly but the generated
  file never imported the package it came from. Type rendering is now recursive
  and the import block is derived from the generated code itself.
- Strict `Old` checks and `Equal` compare a `*time.Time` by value rather than by
  pointer identity, and `Clone` copies the pointee instead of sharing it.
- Slice elements and map values use the element type's own `Equal`/`Clone` where
  it exists, so a cloned `map[string]Player` no longer shares state.
- `deep-gen` no longer parses the file it is about to write, so output left by a
  buggy version cannot block regeneration.

## v5.3.0

### Breaking changes

- **JSON wire format**: `Operation.From` (json `"f"`) now carries the source
  path for `OpMove`/`OpCopy`; it was previously stored in `Operation.Old` (json
  `"o"`). Patches serialized by v5.2.0 and earlier will not round-trip cleanly
  through `ParseJSONPatch`/`UnmarshalJSON`. Re-serialize persisted patches, or
  copy the source path from `Old` to `From` for move/copy operations before
  parsing.

### Fixed

- `OpCopy` deep-copies the source, so reference-typed destinations no longer
  alias it.
- `MapKey` RFC 6901-escapes the key, so values containing `/` or `~` navigate
  correctly.
- `Reverse` drops `OpLog` operations instead of emitting a malformed
  zero-`Kind` operation.
- `ParseJSONPatch` only lifts the *leading* `test` operation into `Guard`.
- `deep-gen`: the root-level strict check uses a comma-ok type assertion and no
  longer panics on a mismatched `Old`.
- `ApplyOpReflectionValue` nil-checks the logger.
- `OpMove`/`OpCopy` from a non-existent source surface an error.
- `hlc`: `Clock.SetLatest` rehydrates the clock under the mutex, and
  `Clock.Reserve` panics on negative `n` or `int32` overflow instead of silently
  wrapping.
- `condition`: `Not` with an empty `Sub` returns an explicit error.

### Performance

- `crdt.Set.Len` no longer materializes the full `Items()` slice.

## v5.2.0

### Added

- `CRDT[T].Reverse` for undo/redo: applies a delta's inverse locally and returns
  it as a new delta with a fresh timestamp, safe to propagate to peers.

### Fixed

- `deep-gen` no longer emits imports of `internal/engine` in generated code,
  which compiled inside the module but broke for downstream users.

## v5.1.1

### Fixed

- `customDiffPatch.summary()` delegates to the inner patch's `Summary()`.

## v5.1.0

### Added

- `crdt.Counter` (PN-Counter), `crdt.Set` (OR-Set) and `crdt.Map` (LWW-Map).

## v5.0.1

### Fixed

- Path navigation, map-nested writes, and `crdt.Text` convergence.

## v5.0.0

Major rewrite introducing code generation, a flat operation model, and
type-safe selectors.

### Architecture

- **Flat operation model**: `Patch[T]` is now a plain `[]Operation` rather than a recursive tree. Operations have `Kind`, `Path` (JSON Pointer), `Old`, `New`, `If`, and `Unless` fields.
- **Code generation**: `cmd/deep-gen` produces `*_deep.go` files with reflection-free `Patch`, `Diff`, `Equal`, and `Clone` methods, several times faster than the reflection fallback (see the README for measured figures).
- **Reflection fallback**: Types without generated code fall through to the internal reflection engine automatically.

### New API (`github.com/brunoga/deep/v5`)

| Function | Description |
|---|---|
| `Diff[T](a, b T) (Patch[T], error)` | Compare two values; returns error for unsupported types |
| `Apply[T](*T, Patch[T], ...ApplyOption) error` | Apply a patch; returns `*ApplyError` with `Unwrap() []error` |
| `Equal[T](a, b T) bool` | Deep equality |
| `Clone[T](v T) T` | Deep copy (formerly `Copy`) |
| `Set[T,V](Path[T,V], V) Op` | Typed replace operation constructor |
| `Add[T,V](Path[T,V], V) Op` | Typed add operation constructor |
| `Remove[T,V](Path[T,V]) Op` | Typed remove operation constructor |
| `Move[T,V](from, to Path[T,V]) Op` | Typed move operation constructor |
| `Copy[T,V](from, to Path[T,V]) Op` | Typed copy operation constructor |
| `Edit[T](*T) *Builder[T]` | Returns a fluent patch builder |
| `Merge[T](base, other, resolver)` | Deduplicate ops by path; resolver called on conflicts, otherwise other wins |
| `Field[T,V](selector)` | Type-safe path from a selector function |
| `At[T,S,E](Path[T,S], int) Path[T,E]` | Extend a slice-field path to an element by index |
| `MapKey[T,M,K,V](Path[T,M], K) Path[T,V]` | Extend a map-field path to a value by key |
| `WithLogger(*slog.Logger) ApplyOption` | Pass a logger to a single Apply call |
| `ParseJSONPatch[T]([]byte) (Patch[T], error)` | Parse RFC 6902 + deep extensions back into a Patch |
| `ConflictResolver` (interface) | Implement `Resolve(path string, local, remote any) any` to customize `Merge` |

**`Patch[T]` methods:**

| Method | Description |
|---|---|
| `Patch.IsEmpty() bool` | Reports whether the patch has no operations |
| `Patch.AsStrict() Patch[T]` | Returns a copy with strict Old-value verification enabled |
| `Patch.WithGuard(*Condition) Patch[T]` | Returns a copy with a global guard condition set |
| `Patch.Reverse() Patch[T]` | Returns the inverse patch (undo) |
| `Patch.ToJSONPatch() ([]byte, error)` | Serialize to RFC 6902 JSON Patch with deep extensions |
| `Patch.String() string` | Human-readable summary of operations |

### `condition` package (`github.com/brunoga/deep/v5/condition`)

Public package used directly by generated `*_deep.go` files. Most callers will not need to import it directly.

- `Condition` — Serializable predicate struct (`Op`, `Path`, `Value`, `Sub`).
- `Evaluate(root reflect.Value, c *Condition) (bool, error)` — Evaluate a condition against a value.
- `CheckType(v any, typeName string) bool` — Runtime type name check (used in generated code).
- `ToPredicate() / FromPredicate()` — Convert `Condition` to/from the JSON Patch wire-format map.
- `Eq`, `Ne`, `Gt`, `Ge`, `Lt`, `Le`, `Exists`, `In`, `Matches`, `Type`, `And`, `Or`, `Not` — Condition operator constants.

### Condition / Guard system

- `Condition` struct with `Op`, `Path`, `Value`, `Sub` fields (serializable predicates).
- Patch-level guard set via `Patch.Guard` field or `patch.WithGuard(c)`.
- Per-operation conditions via `Operation.If` / `Operation.Unless`.
- Builder helpers: `Eq`, `Ne`, `Gt`, `Ge`, `Lt`, `Le`, `Exists`, `In`, `Matches`, `Type`, `And`, `Or`, `Not`.
- Per-op conditions attached to `Op` values via `Op.If` / `Op.Unless`; passed to the builder via `Builder.With`.

### CRDTs (`github.com/brunoga/deep/v5/crdt`)

- `CRDT[T]` — Concurrency-safe CRDT wrapper. Create with `NewCRDT(initial, nodeID)`. Key methods: `Edit(fn)`, `ApplyDelta(delta)`, `Merge(other)`, `View()`. JSON-serializable.
- `Delta[T]` — A timestamped set of changes produced by `CRDT.Edit`; send to peers and apply with `CRDT.ApplyDelta`.
- `LWW[T]` — Embeddable Last-Write-Wins register. Update with `Set(v, ts)`; accepts write only if `ts` is strictly newer.
- `Text` (`[]TextRun`) — Convergent collaborative text. Merge concurrent edits with `MergeTextRuns(a, b)`.

**`github.com/brunoga/deep/v5/crdt/hlc`**

- `Clock` — Per-node HLC state. Create with `NewClock(nodeID)`. Methods: `Now()`, `Update(remote)`, `Reserve(n)`.
- `HLC` — Timestamp struct (wall time + logical counter + node ID). Total ordering via `Compare` / `After`.
