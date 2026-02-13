# Deep Copy and Patch for Go (v2.0)

`deep` is a high-performance, reflection-based library for manipulating complex Go data structures. It provides recursive deep copying, structural diffing to produce patches, a fluent API for manual patch construction, and first-class support for distributed state synchronization.

## New in v2.0
*   **Stateful Differ**: Move and copy detection with custom type registration.
*   **Three-Way Merge**: Combine independent patches derived from a common base.
*   **JSON Pointer Standard**: Full adherence to RFC 6901 for all path handling.
*   **Multi-Error Reporting**: Detailed reports of all conflicts during patch application.
*   **Map Key Normalization**: Semantic matching for complex map keys via the `Keyer` interface.

---

## Core Features

### 1. Deep Copy
Create a decoupled clone of any value.
```go
dst, err := deep.Copy(src)
```

### 2. Deep Diff and Apply
Transform an object from one state to another using efficient, semantic patches.
```go
// 1. Calculate the difference
patch := deep.Diff(oldConfig, newConfig)

if patch != nil {
    fmt.Println(patch.Summary()) // Human readable summary
    
    // 2. Apply to a target (must be a pointer)
    err := patch.ApplyChecked(&oldConfig)
}
```

### 3. Distributed State (CRDT)
granular, field-level convergence for complex structures using Hybrid Logical Clocks (HLC).
*   **Example**: [CRDT Synchronization](./examples/crdt_sync/main.go)

### 4. Three-Way Merge
Merge independent changes from multiple users or nodes.
*   **Example**: [Three-Way Merge](./examples/three_way_merge/main.go)

---

## Advanced Usage

### Move & Copy Detection
The stateful `Differ` automatically detects when complex values are relocated within a structure, emitting efficient `copy` operations.
*   **Example**: [Move Detection](./examples/move_detection/main.go)

### Custom Type Registry
Register specialized diffing logic for types you don't control (e.g., `time.Time`).
*   **Example**: [Custom Types](./examples/custom_types/main.go)

### Map Key Normalization
Use the `Keyer` interface to provide semantic identity for complex map keys, ensuring logical updates even when non-canonical fields change.
*   **Example**: [Key Normalization](./examples/key_normalization/main.go)

### Validation & Multi-Error
Use `ApplyChecked` to validate state before modification. v2.0 returns all errors at once via `ApplyError`.
*   **Example**: [Multi-Error Reporting](./examples/multi_error/main.go)

---

## Technical Details

### JSON Pointer (RFC 6901)
Standardized navigation for both dot-notation and pointer styles:
```go
builder.Root().Navigate("/network/settings/port").Put(8080)
```

### JSON Patch (RFC 6902)
Export any `deep.Patch` to a standard JSON Patch array for web interoperability:
```go
jsonBytes, err := patch.ToJSONPatch()
```

### Performance: Reflection Cache
Metadata (field offsets, tags) is cached per type to minimize reflection overhead in hot loops.

## License
Apache 2.0
