# Changelog

All notable changes to this project are documented here, newest first.

> **Releasing:** update this file as part of every release — add the new
> version's entry before tagging, so the tag, the GitHub release notes, and this
> file always agree.

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
