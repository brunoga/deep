# Deep v5: The High-Performance Type-Safe Synchronization Toolkit

`deep` is a comprehensive Go library for comparing, cloning, and synchronizing complex data structures. v5 introduces a revolutionary architecture centered on **Code Generation** and **Type-Safe Selectors**, delivering up to **26x** performance improvements over traditional reflection-based libraries.

## Key Features

- **🚀 Extreme Performance**: Reflection-free operations via `deep-gen` (10x-20x faster than v4).
- **🛡️ Compile-Time Safety**: Type-safe field selectors replace brittle string paths.
- **📦 Data-Oriented**: Patches are pure, flat data structures, natively serializable to JSON/Gob.
- **🔄 Integrated Causality**: Native support for HLC (Hybrid Logical Clocks) and LWW (Last-Write-Wins).
- **🧩 First-Class CRDTs**: Built-in support for `Text` and `LWW[T]` convergent registers.
- **🤝 Standard Compliant**: Export to RFC 6902 JSON Patch with advanced predicate extensions.
- **🎛️ Hybrid Architecture**: Optimized generated paths with a robust reflection safety net.

## Performance Comparison (v5 Generated vs v4 Reflection)

Benchmarks performed on typical struct models (`User` with IDs, Names, Slices):

| Operation | v4 (Reflection) | v5 (Generated) | Speedup |
| :--- | :--- | :--- | :--- |
| **Apply Patch** | 726 ns/op | **50 ns/op** | **14.5x** |
| **Diff + Apply** | 2,391 ns/op | **270 ns/op** | **8.8x** |
| **Clone (Copy)** | 1,872 ns/op | **290 ns/op** | **6.4x** |
| **Equality** | 202 ns/op | **84 ns/op** | **2.4x** |

## Quick Start

### 1. Define your models
```go
type User struct {
    ID    int            `json:"id"`
    Name  string         `json:"name"`
    Roles []string       `json:"roles"`
    Score map[string]int `json:"score"`
}
```

### 2. Generate optimized code
```bash
go run github.com/brunoga/deep/v5/cmd/deep-gen -type=User .
```

### 3. Use the Type-Safe API
```go
import "github.com/brunoga/deep/v5"

u1 := User{ID: 1, Name: "Alice", Roles: []string{"user"}}
u2 := User{ID: 1, Name: "Bob", Roles: []string{"user", "admin"}}

// State-based Diffing
patch := v5.Diff(u1, u2)

// Operation-based Building (Fluent API)
builder := v5.Edit(&u1)
v5.Set(builder, v5.Field(func(u *User) *string { return &u.Name }), "Alice Smith")
patch2 := builder.Build()

// Application
v5.Apply(&u1, patch)
```

## Advanced Features

### Integrated CRDTs
Convert any field into a convergent register:
```go
type Document struct {
    Title   v5.LWW[string] // Native Last-Write-Wins
    Content v5.Text        // Collaborative Text CRDT
}
```

### Conditional Patching
Apply changes only if specific business rules are met:
```go
builder.Set(v5.Field(func(u *User) *string { return &u.Name }), "New Name").
    If(v5.Eq(v5.Field(func(u *User) *int { return &u.ID }), 1))
```

### Standard Interop
Export your v5 patches to standard RFC 6902 JSON Patch format:
```go
jsonData, _ := patch.ToJSONPatch()
// Output: [{"op":"replace","path":"/name","value":"Bob"}]
```

## Architecture: Why v5?

v4 used a **Recursive Tree Patch** model. Every field was a nested patch object. While flexible, this caused high memory allocations and made serialization difficult.

v5 uses a **Flat Operation Model**. A patch is a simple slice of `Operations`. This makes patches:
1. **Portable**: Trivially serializable to any format.
2. **Fast**: Iterating a slice is much faster than traversing a tree.
3. **Composable**: Merging two patches is a stateless operation.

## License
Apache 2.0
