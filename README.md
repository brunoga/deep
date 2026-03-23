# Deep v5: The High-Performance Type-Safe Synchronization Toolkit

`deep` is a comprehensive Go library for comparing, cloning, and synchronizing complex data structures. Deep introduces a revolutionary architecture centered on **Code Generation** and **Type-Safe Selectors**, delivering up to **26x** performance improvements over traditional reflection-based libraries.

## Key Features

- **Extreme Performance**: Reflection-free operations via `deep-gen` (10x-20x faster than v4).
- **Compile-Time Safety**: Type-safe field selectors replace brittle string paths.
- **Data-Oriented**: Patches are pure, flat data structures, natively serializable to JSON/Gob.
- **Integrated Causality**: Native support for HLC (Hybrid Logical Clocks) and LWW (Last-Write-Wins).
- **First-Class CRDTs**: Built-in support for `Text` and `LWW[T]` convergent registers.
- **Standard Compliant**: Export to RFC 6902 JSON Patch with advanced predicate extensions.
- **Hybrid Architecture**: Optimized generated paths with a robust reflection safety net.

## Performance Comparison (Deep Generated vs v4 Reflection)

Benchmarks performed on typical struct models (`User` with IDs, Names, Slices):

| Operation | v4 (Reflection) | Deep (Generated) | Speedup |
| :--- | :--- | :--- | :--- |
| **Apply Patch** | 726 ns/op | **50 ns/op** | **14.5x** |
| **Diff + Apply** | 2,391 ns/op | **270 ns/op** | **8.8x** |
| **Clone** | 1,872 ns/op | **290 ns/op** | **6.4x** |
| **Equality** | 202 ns/op | **84 ns/op** | **2.4x** |

Run `go test -bench=. ./...` to reproduce. `BenchmarkApplyGenerated` uses generated code;
`BenchmarkApplyReflection` uses the fallback path on a type with no generated code.

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

Add a `go:generate` directive to your source file:

```go
//go:generate go run github.com/brunoga/deep/v5/cmd/deep-gen -type=User .
```

Then run:

```bash
go generate ./...
```

This writes `user_deep.go` in the same directory. Commit it alongside your source.

### 3. Use the Type-Safe API

```go
import deep "github.com/brunoga/deep/v5"

u1 := User{ID: 1, Name: "Alice", Roles: []string{"user"}}
u2 := User{ID: 1, Name: "Bob", Roles: []string{"user", "admin"}}

// State-based Diffing
patch, err := deep.Diff(u1, u2)
if err != nil {
    log.Fatal(err)
}

// Operation-based Building (Fluent, Type-Safe API)
namePath := deep.Field(func(u *User) *string { return &u.Name })
patch2 := deep.Edit(&u1).
    With(deep.Set(namePath, "Alice Smith")).
    Build()

// Application
if err := deep.Apply(&u1, patch); err != nil {
    log.Fatal(err)
}
```

## Advanced Features

### Integrated CRDTs

Convert any field into a convergent register:

```go
type Document struct {
    Title   crdt.LWW[string] // Native Last-Write-Wins
    Content crdt.Text        // Collaborative Text CRDT
}
```

### Conditional Patching

Apply changes only if specific business rules are met:

```go
namePath := deep.Field(func(u *User) *string { return &u.Name })
idPath   := deep.Field(func(u *User) *int    { return &u.ID   })

patch := deep.Edit(&u).
    With(deep.Set(namePath, "New Name").If(deep.Eq(idPath, 1))).
    Build()
```

Apply a patch only if a global guard condition holds:

```go
patch = patch.WithGuard(deep.Gt(deep.Field(func(u *User) *int { return &u.ID }), 0))
```

### Observability

Embed `OpLog` operations in a patch to emit structured trace messages during `Apply`.
Route them to any `*slog.Logger` — useful for request-scoped loggers, test capture, or
tracing without touching your model types:

```go
namePath := deep.Field(func(u *User) *string { return &u.Name })

patch := deep.Edit(&u).
    Log("starting update").
    With(deep.Set(namePath, "Alice Smith")).
    Log("update complete").
    Build()

logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
deep.Apply(&u, patch, deep.WithLogger(logger))
// {"level":"INFO","msg":"deep log","message":"starting update","path":"/"}
// {"level":"INFO","msg":"deep log","message":"update complete","path":"/"}
```

When no logger is provided, `slog.Default()` is used — so existing `slog.SetDefault`
configuration is respected without any extra wiring.

### Standard Interop

Export your Deep patches to standard RFC 6902 JSON Patch format:

```go
jsonData, err := patch.ToJSONPatch()
// Output: [{"op":"replace","path":"/name","value":"Bob"}]
```

> **JSON deserialization note**: When a patch is JSON-encoded and then decoded, numeric
> values in `Operation.Old` and `Operation.New` are unmarshaled as `float64` (standard
> Go JSON behavior). Generated `Patch` methods handle this automatically with
> numeric coercion. If you use the reflection fallback, be aware of this when inspecting
> `Old`/`New` directly.

## Architecture: Why v5?

v4 used a **Recursive Tree Patch** model. Every field was a nested patch object. While flexible, this caused high memory allocations and made serialization difficult.

Deep uses a **Flat Operation Model**. A patch is a simple slice of `Operations`. This makes patches:
1. **Portable**: Trivially serializable to any format.
2. **Fast**: Iterating a slice is much faster than traversing a tree.
3. **Composable**: Merging two patches is a stateless operation.

## License
Apache 2.0
