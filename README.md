# Deep Copy and Patch Library for Go

`deep` is a powerful, reflection-based library for creating deep copies, calculating differences (diffs), and patching complex Go data structures. It supports cyclic references, unexported fields, and custom type-specific behaviors.

## Features

*   **Deep Copy**: Recursively copies structs, maps, slices, arrays, pointers, and interfaces.
*   **Deep Diff**: Calculates the difference between two objects, producing a `Patch`.
*   **Patch Application**: Applies patches to objects to transform them from state A to state B.
*   **Patch Reversal**: Generates a reverse patch to undo changes (`Apply(Reverse(patch))`).
*   **Conditional Patching**: Apply patches only if specific logical conditions are met (`ApplyChecked`, `WithCondition`).
*   **Cross-Field Logic**: Conditions can compare fields against literals OR against other fields.
*   **Flexible Consistency**: Choose between strict "old-value" matching or flexible application based on custom conditions.
*   **Local Node Conditions**: Attach conditions to specific fields or elements during manual construction.
*   **Manual Patch Builder**: Construct valid patches manually using a fluent API with on-the-fly type validation.
*   **Unexported Fields**: Handles unexported struct fields transparently.
*   **Cycle Detection**: Correctly handles circular references in both Copy and Diff operations.

## Installation

```bash
go get github.com/brunoga/deep
```

## Usage

### Deep Copy

```go
import "github.com/brunoga/deep"

type Config struct {
    Name    string
    Version int
    Meta    map[string]any
}

src := Config{Name: "App", Version: 1, Meta: map[string]any{"env": "prod"}}
dst, err := deep.Copy(src)
if err != nil {
    panic(err)
}
```

### Deep Diff and Patch

Calculate the difference between two objects and apply it.

```go
oldConf := Config{Name: "App", Version: 1}
newConf := Config{Name: "App", Version: 2}

// Calculate Diff
patch := deep.Diff(oldConf, newConf)

// Check if there are changes
if patch != nil {
    fmt.Println("Changes found:", patch) 
    // Output: Struct{ Version: 1 -> 2 }

    // Apply to a target (must be a pointer)
    target := oldConf
    patch.Apply(&target)
    // target.Version is now 2
}
```

### Conditional Patching and Consistency

Patches support sophisticated validation before application.

#### 1. Strict Consistency (Default)
By default, `ApplyChecked` ensures that the target's current values exactly match the `old` values recorded during the `Diff`. If the target has diverged, application fails.

```go
// Fails if target.Version != 1
err := patch.ApplyChecked(&target)
```

#### 2. Flexible Consistency
You can disable strict matching to apply changes even if the object has changed, as long as your custom conditions pass.

```go
// Disable "old-value" checks, rely only on custom conditions
patch = patch.WithStrict(false)
```

#### 3. Custom Conditions (Literal & Cross-Field)
Create complex rules using the `Condition` DSL.

```go
// Literals: Apply only if "Version" is greater than 0
cond1, _ := deep.ParseCondition[Config]("Version > 0")

// Cross-Field: Apply only if "CurrentScore" is less than "MaxScore"
cond2, _ := deep.ParseCondition[Config]("CurrentScore < MaxScore")

patchWithCond := patch.WithCondition(deep.And(cond1, cond2))
err = patchWithCond.ApplyChecked(&target)
```

**Supported Condition Syntax:**
*   **Comparisons**: `==`, `!=`, `>`, `<`, `>=`, `<=`
*   **Logic**: `AND`, `OR`, `NOT`, `(...)`
*   **Paths**: `Field`, `Field.SubField`, `Slice[0]`, `Map.Key`
*   **RHS**: Can be a literal (`'string'`, `123`, `true`) OR another path (`OtherField.Sub`)

### Manual Patch Builder

Construct patches programmatically and attach local field-level conditions.

```go
builder := deep.NewBuilder[Config]()
root := builder.Root()

// Set a field with a LOCAL condition (checked only for this specific field)
root.Field("Version").
    Set(1, 2).
    WithCondition(deep.Less[int]("", 10)) // Only if current version < 10

patch, err := builder.Build()
if err == nil {
    // Patches from Builder are also Strict by default
    patch.ApplyChecked(&myConfig)
}
```

### Patch Serialization

Patches can be serialized to JSON or Gob format, including all attached conditions.

```go
// JSON Marshal
data, err := json.Marshal(patch)

// Unmarshal
newPatch := deep.NewPatch[Config]()
err = json.Unmarshal(data, newPatch)
```

### Reversing a Patch

Undo changes by creating a reverse patch.

```go
patch := deep.Diff(stateA, stateB)

// Apply forward
patch.Apply(&stateA) // stateA matches stateB

// Reverse
reversePatch := patch.Reverse()
reversePatch.Apply(&stateA) // stateA is back to original
```

## Advanced

### Custom Copier

Types can implement the `Copier[T]` interface to define custom copy behavior.

```go
type SecureToken string

func (t SecureToken) Copy() (SecureToken, error) {
    return "", nil // Don't copy tokens
}
```

### Unexported Fields

The library uses `unsafe` operations to read and write unexported fields. This is essential for true deep copying and patching of opaque structs but relies on internal runtime structures.

## License

Apache 2.0