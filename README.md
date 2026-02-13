# Deep: High-Performance Data Manipulation for Go

`deep` is a high-performance, reflection-based engine for manipulating complex Go data structures. It provides recursive deep copying, semantic equality checks, and structural diffing to produce optimized patches.

V3 is designed for high-throughput applications, featuring a zero-allocation diffing engine and tag-aware operations.

## Installation

```bash
go get github.com/brunoga/deep/v3
```

---

## Core Features

### 1. Deep Copy
**Justification:** Standard assignment in Go performing shallow copies. `deep.Copy` creates a completely decoupled clone, correctly handling pointers, slices, maps, and private fields (via unsafe).

```go
dst, err := deep.Copy(src)
```
*   **Recursive**: Clones the entire object graph.
*   **Cycle Detection**: Safely handles self-referencing structures.
*   **Unexported Fields**: Optionally clones private struct fields.
*   **Example**: [Config Management](./examples/config_manager/main.go)

### 2. Semantic Equality (`Equal[T]`)
**Justification:** `reflect.DeepEqual` is slow and lacks control. `deep.Equal` is a tag-aware, cache-optimized replacement that is up to 30% faster and respects library-specific struct tags.

```go
if deep.Equal(objA, objB) {
    // Logically equal, respecting deep:"-" tags
}
```
*   **Tag Awareness**: Skips fields marked with `deep:"-"`.
*   **Short-Circuiting**: Immediately returns true for identical pointer addresses.
*   **Performance**: Uses a global reflection cache to minimize lookup overhead.

### 3. Structural Diff & Patch
**Justification:** Efficiently synchronizing state between nodes or auditing changes requires knowing *what* changed, not just that *something* changed. `deep.Diff` produces a semantic `Patch` representing the minimum set of operations to transform one value into another.

```go
// Generate patch
patch := deep.Diff(oldState, newState)

// Inspect changes
fmt.Println(patch.Summary()) 

// Apply to target
err := patch.ApplyChecked(&oldState)
```
*   **Move & Copy Detection**: Identifies relocated values to minimize patch size.
*   **Three-Way Merge**: Merges independent patches with conflict detection.
*   **JSON Standard**: Native export to RFC 6902 (JSON Patch).
*   **Examples**: [Move Detection](./examples/move_detection/main.go), [Three-Way Merge](./examples/three_way_merge/main.go)

---

## Advanced Capabilities

### Conflict Resolution & CRDTs
**Justification:** In distributed systems, state often diverges. `deep` provides first-class support for state convergence using Hybrid Logical Clocks (HLC).

*   **LWW Resolver**: Automatic "Last-Write-Wins" resolution at the field level.
*   **Example**: [CRDT Synchronization](./examples/crdt_sync/main.go)

### Struct Tag Control
Fine-grained control over library behavior:
*   `deep:"-"`: Completely ignore field.
*   `deep:"key"`: Identity field for slice alignment (Myers' Diff).
*   `deep:"readonly"`: Field can be diffed but not modified by patches.
*   `deep:"atomic"`: Treat complex fields as scalar values.

---

## Performance Optimization

v3.0 is built for performance-critical hot paths:
*   **Zero-Allocation Engine**: Uses `sync.Pool` for internal transient structures.
*   **Lazy Allocation**: Maps and slices in patches are only allocated if changes are found.
*   **Manual Release**: Use `patch.Release()` to return patch resources to the pool.

---

## Version History

### v1.0.0: The Foundation
*   Initial recursive **Deep Copy** implementation.
*   Basic **Deep Diff** producing Add/Remove/Replace operations.
*   Support for standard Go types (Slices, Maps, Structs, Pointers).

### v2.0.0: Synchronization & Standards
*   **JSON Pointer (RFC 6901)**: Standardized all path navigation.
*   **Keyed Slice Alignment**: Integrated identity-based matching into Myers' Diff.
*   **Human-Readable Summaries**: Added `Patch.Summary()` for audit logging.
*   **HLC & CRDT**: Introduced Hybrid Logical Clocks and LWW conflict resolution.
*   **Multi-Error Reporting**: `ApplyChecked` reports all validation failures at once.

### v3.0.0: High-Performance Engine (Current)
*   **Zero-Allocation Engine**: Comprehensive refactor to use object pooling and path stacks.
*   **`deep.Equal[T]`**: High-performance, tag-aware replacement for `reflect.DeepEqual`.
*   **Move & Copy Detection**: Semantic detection of relocated values during `Diff`.
*   **Custom Type Registry**: Support for registering specialized diffing logic for external types.
*   **Pointer Identity Optimization**: Massive speedup via immediate short-circuiting for identical pointers.
*   **Memory Efficiency**: Up to 80% reduction in memory overhead for large structural comparisons.

---

## License
Apache 2.0
