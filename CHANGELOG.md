# Changelog

## v5.0.0 (in development)

Major rewrite. Breaking changes from v4.

### Architecture

- **Flat operation model**: `Patch[T]` is now a plain `[]Operation` rather than a recursive tree. Operations have `Kind`, `Path` (JSON Pointer), `Old`, `New`, `Timestamp`, `If`, and `Unless` fields.
- **Code generation**: `cmd/deep-gen` produces `*_deep.go` files with reflection-free `ApplyOperation`, `Diff`, `Equal`, `Copy`, and `EvaluateCondition` methods — typically 10–15x faster than the reflection fallback.
- **Reflection fallback**: Types without generated code fall through to the v4-based internal engine automatically.

### New API (`github.com/brunoga/deep/v5`)

| Function | Description |
|---|---|
| `Diff[T](a, b T) (Patch[T], error)` | Compare two values; returns error for unsupported types |
| `Apply[T](*T, Patch[T]) error` | Apply a patch; returns `*ApplyError` with `Unwrap() []error` |
| `Equal[T](a, b T) bool` | Deep equality |
| `Copy[T](v T) T` | Deep copy |
| `Edit[T](*T) *Builder[T]` | Fluent patch builder |
| `Merge[T](base, other, resolver)` | Merge two patches with LWW or custom resolution |
| `Field[T,V](selector)` | Type-safe path from a selector function |
| `Register[T]()` | Register types for gob serialization |
| `Logger() *slog.Logger` | Concurrent-safe logger accessor |
| `SetLogger(*slog.Logger)` | Replace the logger (concurrent-safe) |

### Condition / Guard system

- `Condition` struct with `Op`, `Path`, `Value`, `Apply` fields (serializable predicates).
- Patch-level guard set via `Patch.Guard` field or `patch.WithGuard(c)`.
- Per-operation conditions via `Operation.If` / `Operation.Unless`.
- Builder helpers: `Eq`, `Ne`, `Gt`, `Ge`, `Lt`, `Le`, `Exists`, `In`, `Matches`, `Type`, `And`, `Or`, `Not`, `Log`.

### CRDTs

- `LWW[T]` — Last-Write-Wins register with HLC timestamp.
- `crdt.Text` — Collaborative text CRDT (`[]TextRun`).
- `crdt/hlc.HLC` — Hybrid Logical Clock for causality ordering.

### Breaking changes from v4

- Import path: `github.com/brunoga/deep/v4` → `github.com/brunoga/deep/v5`
- `Diff` now returns `(Patch[T], error)` instead of `Patch[T]`.
- `Patch` is now generic (`Patch[T]`); patches are not cross-type compatible.
- `Patch.Condition` renamed to `Patch.Guard`; `WithCondition` → `WithGuard`.
- `Logger` changed from a package-level variable to `Logger() *slog.Logger` (concurrent-safe).
- `cond/` package moved to `internal/cond/`; no longer part of the public API.
- `deep-gen` now writes output to `{type}_deep.go` by default instead of stdout.
- `OpAdd` on slices sets by index rather than inserting; true insertion is not supported for unkeyed slices.
