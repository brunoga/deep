# Examples

Every directory here is a self-contained, runnable program built around one
concept:

```bash
go run ./examples/config_manager
```

They are meant to be read as much as run — each prints its intermediate state so
you can follow what the library did and why.

## Start here

If you are new to the library, these four cover the shape of everything else:

1. **[`config_manager`](config_manager)** — the core loop: diff two values,
   apply the patch, roll it back with `Reverse`.
2. **[`nested_structs`](nested_structs)** — how paths address fields, including
   nested and embedded structs, and how a diff pinpoints what changed.
3. **[`policy_engine`](policy_engine)** — conditions travelling inside the
   patch, so the rule is enforced wherever it is applied.
4. **[`json_interop`](json_interop)** — what a patch looks like on the wire, in
   both the native format and RFC 6902.

## Core operations and patches

| Example | Shows |
| :--- | :--- |
| [`config_manager`](config_manager) | `Diff` → `Apply` → `Reverse` for rollback |
| [`state_management`](state_management) | An undo stack built by keeping each edit's reverse patch |
| [`nested_structs`](nested_structs) | Paths into nested structs; embedded fields addressed by type name; a selector targeting one nested field |
| [`slice_paths`](slice_paths) | `At` for positional elements; why a length-changing diff becomes one whole-slice replace while a same-length one stays per-index |
| [`keyed_inventory`](keyed_inventory) | `deep:"key"` — elements addressed by identity, so reordering produces no operations and a changed element diffs down to the changed field |
| [`struct_map_keys`](struct_map_keys) | Non-string map keys rendered into paths |
| [`move_copy_ops`](move_copy_ops) | `Move` (empties the source) versus `Copy` (leaves it intact) |
| [`multi_error`](multi_error) | `Apply` collecting every failure instead of stopping at the first; unwrapping `ApplyError` |

## Tags, conditions, and safety

| Example | Shows |
| :--- | :--- |
| [`atomic_config`](atomic_config) | `deep:"readonly"` rejecting writes; `deep:"atomic"` replacing a struct as one unit |
| [`ignored_fields`](ignored_fields) | `json:"-"` and `deep:"-"` keeping secrets out of patches — and the sharp edge that invisible means invisible to `Clone` too |
| [`policy_engine`](policy_engine) | A patch-level `Guard` built from `And`/`Or`/`Matches`: all-or-nothing |
| [`conditional_ops`](conditional_ops) | Per-operation `If`/`Unless`: one patch, each operation deciding for itself |
| [`concurrent_updates`](concurrent_updates) | Strict mode as optimistic locking — a stale patch is rejected |
| [`three_way_merge`](three_way_merge) | `Merge` with a custom `ConflictResolver` |
| [`reflection_fallback`](reflection_fallback) | What the reflection engine handles that generated code cannot: unexported fields and cyclic structures |

## Transport and interop

| Example | Shows |
| :--- | :--- |
| [`json_interop`](json_interop) | Native JSON, RFC 6902 export, and ingesting an externally-produced JSON Patch with `ParseJSONPatch` |
| [`http_patch_api`](http_patch_api) | A patch-driven HTTP `PATCH` endpoint, client and server |
| [`websocket_sync`](websocket_sync) | Broadcasting per-tick state deltas to a client |
| [`audit_logging`](audit_logging) | A diff read as an audit trail, plus `OpLog` tracing through an injected `slog.Logger` |

## CRDTs

| Example | Shows |
| :--- | :--- |
| [`crdt_sync`](crdt_sync) | Two nodes exchanging deltas and converging |
| [`crdt_undo_redo`](crdt_undo_redo) | Distributed undo/redo via `Reverse`, and why redelivering a delta is harmless |
| [`crdt_containers`](crdt_containers) | `Counter`, `Set` (add-wins) and `Map` (last-write-wins per key) |
| [`crdt_list`](crdt_list) | `List[T]`: concurrent insertions and deletions in a sequence all survive, where an ordinary slice would keep one writer's version |
| [`lww_fields`](lww_fields) | Per-field `LWW[T]` registers resolving a genuine write conflict |
| [`text_sync`](text_sync) | Collaborative text: concurrent edits across a partition, merged with `MergeTextRuns` |

## Generated code

Examples whose model types have a `//go:generate` directive ship the generated
`*_deep.go` file alongside, exercising the reflection-free fast path; the rest
run on the reflection engine. Both are the same API — `go generate ./...`
regenerates every one of them.
