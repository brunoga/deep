# Deep Copy and Patch for Go

`deep` is a high-performance, reflection-based library for manipulating complex Go data structures. It provides three primary capabilities: recursive deep copying, structural diffing to produce patches, and a fluent API for manual patch construction.

## Features

*   **Deep Copy**: Full recursive cloning of structs, maps, slices, and pointers.
*   **Deep Diff**: Calculate the semantic difference between two objects.
*   **Rich Patching**: Apply patches with atomicity, move/copy operations, and logging.
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

## Core Concepts

### The Patch Model
A `Patch[T]` is a tree of operations. Unlike simple key-value maps, `deep` patches understand the structure of your data. A single patch can contain replacements, slice insertions/deletions, map manipulations, and even data movement between paths.

### Consistency Modes
*   **Strict (Default)**: `ApplyChecked` ensures the target value matches the `old` value recorded during the `Diff`. If the target has changed since the diff was taken, the patch fails.
*   **Flexible**: Disable strict checking using `patch.WithStrict(false)` to apply changes regardless of the current value, relying instead on custom Conditions.

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

### Advanced Operations
The builder supports more than just "Set":
*   **Move**: `Root().Field("Backup").Move("/Active")`
*   **Copy**: `Root().Field("Template").Copy("/Target")`
*   **Test**: `Root().Field("Status").Test("ready")` (Fails patch if value doesn't match)
*   **Log**: `Root().Log("Applying update...")` (Prints to stdout during application)

---

## Conditional Patching

### Condition DSL
You can attach logic to any node in a patch using a string-based DSL via `ParseCondition` or `AddCondition`.

**Syntax Examples:**
*   `"Version > 5"` (Literal comparison)
*   `"Stock < MinAlertThreshold"` (Cross-field comparison)
*   `"Network.Port == 8080 AND Status == 'active'"` (Logical groups)
*   `"NOT (Tags[0] == 'internal')"` (Slice access)

### Soft Conditions (If/Unless)
Standard conditions fail the entire patch. Soft conditions simply skip a specific operation while allowing the rest of the patch to proceed.

```go
builder.Root().Field("BetaFeatures").
    If(deep.Equal[Config]("Tier", "premium")).
    Put(true)
```

---

## Interoperability

### JSON Pointer (RFC 6901)
Use standard pointers to navigate or query your structures:
```go
// Both the DSL and the Builder support JSON pointers
cond, _ := deep.ParseCondition[Config]("/network/settings/port > 1024")
builder.Root().Navigate("/meta/tags/0").Put("new")
```

### JSON Patch (RFC 6902)
Export any `deep.Patch` to a standard JSON Patch array:
```go
jsonBytes, err := patch.ToJSONPatch()
```

---

## Advanced Options

### Ignoring Paths
Ignore specific fields during a diff or copy (e.g., timestamps or secrets):
```go
// Works for both Copy and Diff
dst, _ := deep.Copy(src, deep.IgnorePath("SecretToken"))
patch := deep.Diff(old, new, deep.IgnorePath("UpdatedAt"))
```

### Skipping Unsupported Types
Tell `Copy` to zero-out types it cannot handle (like functions or channels) instead of returning an error:
```go
dst, _ := deep.Copy(src, deep.SkipUnsupported())
```

---

## Technical Details

### Unexported Fields
`deep` uses `unsafe` pointers to read and write unexported struct fields. This is required for true deep copying of third-party or internal types where fields are not public.

### Cycle Detection
The library tracks pointers during recursive operations. Circular references are handled correctly without entering infinite loops.

### Custom Copiers
Types can control their own cloning logic by implementing `Copier[T]`:
```go
type Token string
func (t Token) Copy() (Token, error) { return "REDACTED", nil }
```

## License
Apache 2.0
