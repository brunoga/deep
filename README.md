# Deep Copy and Patch for Go

`deep` is a high-performance, reflection-based library for manipulating complex Go data structures. It provides recursive deep copying, structural diffing to produce patches, a fluent API for manual patch construction, and first-class support for distributed state synchronization (CRDTs).

## Features

*   **Deep Copy**: Full recursive cloning of structs, maps, slices, and pointers.
*   **Deep Diff**: Calculate the semantic difference between two objects.
*   **Rich Patching**: Apply patches with atomicity, move/copy operations, and logging.
*   **Conflict Resolution**: Pluggable resolvers for convergent synchronization (CRDTs).
*   **Conditional Logic**: A built-in DSL for cross-field validation and soft-skipping (`If`/`Unless`).
*   **Standard Compliant**: Full support for JSON Pointer (RFC 6901) and JSON Patch (RFC 6902).
*   **Production Ready**: Handles circular references and unexported fields transparently.

---

## Quick Start

### 1. Deep Copy
Create a decoupled clone of any value.
```go
dst, err := deep.Copy(src)
```

### 2. Diff and Apply
Transform an object from one state to another.
```go
// 1. Calculate the difference
patch := deep.Diff(oldConfig, newConfig)

if patch != nil {
    // 2. Apply to a target (must be a pointer)
    err := patch.ApplyChecked(&oldConfig)
}
```

---

## Distributed State (CRDT)

`deep` includes a first-class CRDT engine for synchronizing complex Go structures across multiple nodes without a central coordinator.

### Why Deep CRDTs?
Most CRDT libraries only handle primitives. `deep` uses its structural awareness to provide **granular, field-level convergence** for your existing Go types.

### Basic Usage
```go
import "github.com/brunoga/deep/v2/crdt"

// 1. Initialize a CRDT wrapper
nodeA := crdt.NewCRDT(Config{Title: "Initial"}, "node-a")

// 2. Edit the state
delta := nodeA.Edit(func(c *Config) {
    c.Title = "Updated Title"
})

// 3. Apply changes from other nodes
nodeB.ApplyDelta(delta)
```

### Semantic Slices
By tagging slice elements with `deep:"key"`, `deep` enables **Yjs-style semantic patching**. This ensures that concurrent insertions into a list interleave correctly rather than overwriting each other or failing due to index shifts.

```go
type Document struct {
    Text []Char `deep:"key"` // Enable semantic list merging
}
```

---

## Core Concepts

### Pluggable Resolution
You can provide custom logic to mediate how patches are applied using the `ConflictResolver` interface. This is how the CRDT package implements Last-Write-Wins (LWW) via Hybrid Logical Clocks (HLC).

```go
// Use a custom resolver to implement business-specific merge rules
err := patch.ApplyResolved(&target, myResolver)
```

### Consistency Modes
*   **Strict (Default)**: `ApplyChecked` ensures the target value matches the `old` value recorded during the `Diff`. If the target has changed since the diff was taken, the patch fails.
*   **Flexible**: Disable strict checking using `patch.WithStrict(false)` to apply changes regardless of the current value.
*   **Resolved**: Use `ApplyResolved` to handle concurrent edits via a custom resolution strategy.

---

## The Manual Patch Builder
While `Diff` is great for sync logic, the `Builder` is ideal for API-driven updates or migrations.

```go
builder := deep.NewBuilder[Config]()

builder.Root().
    Field("Version").Put(2). // "Put" is a blind set (bypasses strict checks)
    Field("Metadata").
        MapKey("env").Set("dev", "prod") // "Set" requires old value for strict check

patch, _ := builder.Build()
patch.ApplyChecked(&myConfig)
```

### Navigation
You can jump to any path using Go-style notation or JSON Pointers:
```go
builder.Root().Navigate("/network/settings/port").Put(8080)
builder.Root().Navigate("Metadata.Tags[0]").Put("admin")
```

---

## Conditional Patching

### Condition DSL
You can attach logic to any node in a patch using a string-based DSL via `ParseCondition` or `AddCondition`.

**Syntax Examples:**
*   `"Version > 5"` (Literal comparison)
*   `"Stock < MinAlertThreshold"` (Cross-field comparison)
*   `"Network.Port == 8080 AND Status == 'active'"` (Logical groups)

---

## Interoperability

### JSON Pointer (RFC 6901)
Use standard pointers to navigate or query your structures:
```go
cond, _ := deep.ParseCondition[Config]("/network/settings/port > 1024")
builder.Root().Navigate("/meta/tags/0").Put("new")
```

### JSON Patch (RFC 6902)
Export any `deep.Patch` to a standard JSON Patch array:
```go
jsonBytes, err := patch.ToJSONPatch()
```

---

## Technical Details

### Hybrid Logical Clocks (HLC)
The CRDT package uses HLCs to provide causal ordering of events without requiring perfect clock synchronization between nodes.

### Unexported Fields
`deep` uses `unsafe` pointers to read and write unexported struct fields. This is required for true deep copying of third-party or internal types.

### Cycle Detection
The library tracks pointers during recursive operations. Circular references are handled correctly without entering infinite loops.

## License
Apache 2.0
