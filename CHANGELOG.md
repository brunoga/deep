# Changelog

## v5.0.0 (in development)

Major rewrite. Breaking changes from v4.

### Architecture

- **Flat operation model**: `Patch[T]` is now a plain `[]Operation` rather than a recursive tree. Operations have `Kind`, `Path` (JSON Pointer), `Old`, `New`, `If`, and `Unless` fields.
- **Code generation**: `cmd/deep-gen` produces `*_deep.go` files with reflection-free `Patch`, `Diff`, `Equal`, and `Clone` methods — typically 10–15x faster than the reflection fallback.
- **Reflection fallback**: Types without generated code fall through to the v4-based internal engine automatically.

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
- `EvaluateCondition(root reflect.Value, c *Condition) (bool, error)` — Evaluate a condition against a value.
- `CheckType(v any, typeName string) bool` — Runtime type name check (used in generated code).
- `ToPredicate() / FromPredicate()` — Convert `Condition` to/from the JSON Patch wire-format map.
- `CondEq`, `CondNe`, `CondGt`, `CondGe`, `CondLt`, `CondLe`, `CondExists`, `CondIn`, `CondMatches`, `CondType`, `CondAnd`, `CondOr`, `CondNot` — Condition operator constants.

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

### Breaking changes from v4

- Import path: `github.com/brunoga/deep/v4` → `github.com/brunoga/deep/v5`
- `Diff` now returns `(Patch[T], error)` instead of `Patch[T]`.
- `Patch` is now generic (`Patch[T]`); patches are not cross-type compatible.
- `Patch.Condition` renamed to `Patch.Guard`; `WithCondition` → `WithGuard`.
- Global `Logger`/`SetLogger` removed; pass `WithLogger(l)` as an `Apply` option for per-call logging.
- `cond/` package removed; conditions live in `github.com/brunoga/deep/v5/condition`.
- `deep-gen` now writes output to `{type}_deep.go` by default instead of stdout.
- `OpAdd` on slices sets by index rather than inserting; true insertion is not supported for unkeyed slices.
- `Copy[T](v T) T` renamed to `Clone[T](v T) T`; `Copy` is now the patch-op constructor `Copy[T,V](from, to Path[T,V]) Op`.
- `Builder.Set/Add/Remove/Move/Copy` methods removed; use `Builder.With(deep.Set(...), ...)` instead.
- `Builder.If/Unless` methods removed; attach per-op conditions on the `Op` value before passing to `With`.
