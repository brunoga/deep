# Deep Copy and Patch for Go (v3.0)

`deep` is a high-performance, reflection-based engine for manipulating complex Go data structures. It provides recursive deep copying, structural diffing to produce semantic patches, and robust support for distributed state convergence.

## Key Features

*   **Zero-Allocation Diff Engine**: Optimized with object pooling and lazy allocation to minimize GC pressure during large structural comparisons.
*   **High-Performance Equality**: `deep.Equal[T]` provides a tag-aware, cache-optimized replacement for `reflect.DeepEqual`.
*   **Three-Way Merge**: Combine independent patches derived from a common base with hierarchical conflict detection.
*   **Move & Copy Detection**: Automatically identifies relocated values within a structure, emitting efficient `move` and `copy` operations.
*   **CRDT Support**: First-class support for state convergence using Hybrid Logical Clocks (HLC) and Last-Write-Wins (LWW) resolvers.
*   **JSON Pointer & Patch**: Full adherence to RFC 6901 (Pointer) and export support for RFC 6902 (Patch).
*   **Advanced Control**: Precise manipulation via `deep` struct tags (`-`, `readonly`, `atomic`) and custom diff registries.

---

## Installation

```bash
go get github.com/brunoga/deep/v2
```

---

## Core Usage

### 1. Deep Copy
Create a completely decoupled clone of any value.
```go
dst, err := deep.Copy(src)
```

### 2. Semantic Diff & Apply
Generate a patch representing the difference between two states and apply it to a target.
```go
// 1. Calculate the difference
patch := deep.Diff(oldState, newState)

if patch != nil {
    fmt.Println(patch.Summary()) // Human readable description
    
    // 2. Apply to a target (must be a pointer)
    err := patch.ApplyChecked(&oldState)
}
```

### 3. High-Performance Equality
Use `deep.Equal` for faster, tag-aware equality checks.
```go
if deep.Equal(objA, objB) {
    // Objects are logically equal (respecting deep:"-" tags)
}
```

---

## Advanced Synchronization

### Three-Way Merge
Merge changes from multiple sources into a single, consistent state.
```go
merged, conflicts, err := deep.Merge(patchA, patchB)
```
*   **Example**: [Three-Way Merge](./examples/three_way_merge/main.go)

### Distributed State (CRDT)
Converge state across distributed nodes without a central authority.
*   **Example**: [CRDT Synchronization](./examples/crdt_sync/main.go)

### Move & Copy Detection
The `Differ` can be configured to detect relocated values, significantly reducing patch size for large re-orders.
```go
patch := deep.Diff(old, new, deep.DiffDetectMoves(true))
```
*   **Example**: [Move Detection](./examples/move_detection/main.go)

---

## Configuration & Control

### Struct Tags
Control library behavior at the field level:
*   `deep:"-"`: Ignore the field for both Copy and Diff.
*   `deep:"readonly"`: Field can be diffed but never modified by a patch.
*   `deep:"atomic"`: Treat complex types as scalars (replace entirely if changed).

### Custom Types
Register specialized logic for types you don't control (e.g., `time.Time`).
*   **Example**: [Custom Types](./examples/custom_types/main.go)

---

## Performance Notes

v3.0 is designed for high-throughput applications:
*   **Object Pooling**: Internal structures like `diffContext` and patch objects are pooled. Use `patch.Release()` after application to return resources to the pool.
*   **Reflection Cache**: Metadata is cached per type to eliminate redundant reflection lookups.
*   **Short-Circuiting**: Pointer identity is checked immediately to bypass recursion for identical branches.

## License
Apache 2.0
