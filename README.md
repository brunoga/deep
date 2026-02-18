# Deep: High-Performance Data Manipulation for Go

`deep` is a high-performance, reflection-based engine for manipulating complex Go data structures. It provides recursive deep copying, semantic equality checks, and structural diffing to produce optimized patches.

V4 focuses on API ergonomics with a fluent patch builder and advanced conflict resolution for distributed systems.

## Installation

```bash
go get github.com/brunoga/deep/v4
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
patch, err := deep.Diff(oldState, newState)

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

### Fluent Patch Builder
V4 introduces a fluent API for manual patch construction, allowing for intuitive navigation and modification of data structures without manual path management.

```go
builder := deep.NewPatchBuilder[MyStruct]()
builder.Field("Profile").Field("Age").Set(30, 31)
builder.Field("Tags").Add(0, "new-tag")
patch, err := builder.Build()
```

### Advanced Conflict Resolution
For distributed systems and CRDTs, `deep` allows you to intercept and resolve conflicts dynamically. The resolver has access to both the **current** value at the target path and the **proposed** value.

```go
type MyResolver struct{}

func (r *MyResolver) Resolve(path string, op deep.OpKind, key, prevKey any, current, proposed reflect.Value) (reflect.Value, bool) {
    // Custom logic: e.g., semantic 3-way merge or timestamp-based LWW
    return proposed, true 
}

err := patch.ApplyResolved(&state, &MyResolver{})
```

### Struct Tag Control
Fine-grained control over library behavior:
*   `deep:"-"`: Completely ignore field.
*   `deep:"key"`: Identity field for slice alignment (Myers' Diff).
*   `deep:"readonly"`: Field can be diffed but not modified by patches.
*   `deep:"atomic"`: Treat complex fields as scalar values.

---

## Performance Optimization

Built for performance-critical hot paths:
*   **Zero-Allocation Engine**: Uses `sync.Pool` for internal transient structures during diffing.
*   **Reflection Cache**: Global cache for type metadata to eliminate repetitive lookups.
*   **Lazy Allocation**: Maps and slices in patches are only allocated if changes are found.

---

## Version History

### v4.0.0: Ergonomics & Context (Current)
*   **Fluent Patch Builder**: Merged `Node` into `PatchBuilder` for a cleaner, chainable API.
*   **Context-Aware Resolution**: `ConflictResolver` now receives both `current` and `proposed` values and can return a merged result.
*   **Strict JSON Pointers**: Removed dot-notation support in favor of strict RFC 6901 compliance.
*   **Simplified Registry**: Global `RegisterCustom*` functions for easier extension.

### v3.0.0: High-Performance Engine
*   **Zero-Allocation Engine**: Refactored to use object pooling.
*   **`deep.Equal[T]`**: High-performance, tag-aware replacement for `reflect.DeepEqual`.
*   **Move & Copy Detection**: Semantic detection of relocated values during `Diff`.

### v2.0.0: Synchronization & Standards
*   **JSON Pointer (RFC 6901)**: Standardized path navigation.
*   **Keyed Slice Alignment**: Integrated identity-based matching into Myers' Diff.
*   **HLC & CRDT**: Introduced Hybrid Logical Clocks and LWW conflict resolution.

### v1.0.0: The Foundation
*   Initial recursive **Deep Copy** and **Deep Diff** implementation.

---

## License
Apache 2.0
