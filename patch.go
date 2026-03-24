package deep

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/brunoga/deep/v5/condition"
	"github.com/brunoga/deep/v5/internal/engine"
)

// ApplyError represents one or more errors that occurred during patch application.
type ApplyError struct {
	Errors []error
}

func (e *ApplyError) Error() string {
	if len(e.Errors) == 1 {
		return e.Errors[0].Error()
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%d errors during apply:\n", len(e.Errors)))
	for _, err := range e.Errors {
		b.WriteString("- " + err.Error() + "\n")
	}
	return b.String()
}

// Unwrap implements the errors.Join interface, allowing errors.Is and errors.As
// to inspect individual errors within the ApplyError.
func (e *ApplyError) Unwrap() []error {
	return e.Errors
}

// OpKind represents the type of operation in a patch.
type OpKind = engine.OpKind

const (
	OpAdd     = engine.OpAdd
	OpRemove  = engine.OpRemove
	OpReplace = engine.OpReplace
	OpMove    = engine.OpMove
	OpCopy    = engine.OpCopy
	OpLog     = engine.OpLog
)

// Patch is a pure data structure representing a set of changes to type T.
// It is designed to be easily serializable and manipulatable.
type Patch[T any] struct {
	// _ is a zero-size phantom field that binds T into the struct's type identity.
	// It prevents Patch[Foo] from being assignable to Patch[Bar] at compile time
	// without contributing any size or alignment to the struct.
	_ [0]T

	// Guard is a global Condition that must be satisfied before any operation
	// in this patch is applied. Set via WithGuard or Builder.Guard.
	Guard *condition.Condition `json:"cond,omitempty"`

	// Operations is a flat list of changes.
	Operations []Operation `json:"ops"`

	// Strict mode enables Old value verification.
	Strict bool `json:"strict,omitempty"`
}

// Operation is an alias for the internal engine operation type.
//
// Note: after JSON round-trip, numeric Old/New values become float64.
type Operation = engine.Operation

// IsEmpty reports whether the patch contains no operations.
func (p Patch[T]) IsEmpty() bool {
	return len(p.Operations) == 0
}

// AsStrict returns a new patch with strict mode enabled.
// When strict mode is on, every Replace and Remove operation verifies the
// current value matches Op.Old before applying; mismatches return an error.
func (p Patch[T]) AsStrict() Patch[T] {
	p.Strict = true
	return p
}

// WithGuard returns a new patch with the global guard condition set.
func (p Patch[T]) WithGuard(c *condition.Condition) Patch[T] {
	p.Guard = c
	return p
}

// String returns a human-readable summary of the patch operations.
func (p Patch[T]) String() string {
	if len(p.Operations) == 0 {
		return "No changes."
	}
	var b strings.Builder
	for i, op := range p.Operations {
		if i > 0 {
			b.WriteByte('\n')
		}
		switch op.Kind {
		case OpAdd:
			b.WriteString(fmt.Sprintf("Add %s: %v", op.Path, op.New))
		case OpRemove:
			b.WriteString(fmt.Sprintf("Remove %s (was %v)", op.Path, op.Old))
		case OpReplace:
			b.WriteString(fmt.Sprintf("Replace %s: %v -> %v", op.Path, op.Old, op.New))
		case OpMove:
			b.WriteString(fmt.Sprintf("Move %v to %s", op.Old, op.Path))
		case OpCopy:
			b.WriteString(fmt.Sprintf("Copy %v to %s", op.Old, op.Path))
		case OpLog:
			b.WriteString(fmt.Sprintf("Log %s: %v", op.Path, op.New))
		}
	}
	return b.String()
}

// Reverse returns a new patch that undoes the changes in this patch.
func (p Patch[T]) Reverse() Patch[T] {
	res := Patch[T]{
		Strict: p.Strict,
	}
	for i := len(p.Operations) - 1; i >= 0; i-- {
		op := p.Operations[i]
		rev := Operation{
			Path: op.Path,
		}
		switch op.Kind {
		case OpAdd:
			rev.Kind = OpRemove
			rev.Old = op.New
		case OpRemove:
			rev.Kind = OpAdd
			rev.New = op.Old
		case OpReplace:
			rev.Kind = OpReplace
			rev.Old = op.New
			rev.New = op.Old
		case OpMove:
			rev.Kind = OpMove
			// op.Old for Move was the fromPath string.
			// To reverse, we move back from current Path to op.Old Path.
			rev.Path = fmt.Sprintf("%v", op.Old)
			rev.Old = op.Path
		case OpCopy:
			// Undoing a copy means removing the copied value at the target path
			rev.Kind = OpRemove
			rev.Old = op.New
		}
		res.Operations = append(res.Operations, rev)
	}
	return res
}

// ToJSONPatch returns a JSON Patch representation compatible with RFC 6902
// and the github.com/brunoga/jsonpatch extensions.
func (p Patch[T]) ToJSONPatch() ([]byte, error) {
	var res []map[string]any

	// If there is a global condition, we prepend a no-op test operation
	// that carries the condition. github.com/brunoga/jsonpatch supports this.
	if p.Guard != nil {
		res = append(res, map[string]any{
			"op":   "test",
			"path": "/",
			"if":   p.Guard.ToPredicate(),
		})
	}

	for _, op := range p.Operations {
		m := map[string]any{
			"op":   op.Kind.String(),
			"path": op.Path,
		}

		switch op.Kind {
		case OpAdd, OpReplace:
			m["value"] = op.New
		case OpMove, OpCopy:
			m["from"] = op.Old
		case OpLog:
			m["value"] = op.New // log message
		}

		if op.If != nil {
			m["if"] = op.If.ToPredicate()
		}
		if op.Unless != nil {
			m["unless"] = op.Unless.ToPredicate()
		}

		res = append(res, m)
	}

	return json.Marshal(res)
}

// ParseJSONPatch parses a JSON Patch document (RFC 6902 plus deep extensions)
// back into a Patch[T]. This is the inverse of Patch.ToJSONPatch().
func ParseJSONPatch[T any](data []byte) (Patch[T], error) {
	var ops []map[string]any
	if err := json.Unmarshal(data, &ops); err != nil {
		return Patch[T]{}, fmt.Errorf("ParseJSONPatch: %w", err)
	}
	res := Patch[T]{}
	for _, m := range ops {
		opStr, _ := m["op"].(string)
		path, _ := m["path"].(string)

		// Global condition is encoded as a test op on "/" with an "if" predicate.
		if opStr == "test" && path == "/" {
			if ifPred, ok := m["if"].(map[string]any); ok {
				res.Guard = condition.FromPredicate(ifPred)
			}
			continue
		}

		op := Operation{Path: path}

		// Per-op conditions
		if ifPred, ok := m["if"].(map[string]any); ok {
			op.If = condition.FromPredicate(ifPred)
		}
		if unlessPred, ok := m["unless"].(map[string]any); ok {
			op.Unless = condition.FromPredicate(unlessPred)
		}

		switch opStr {
		case "add":
			op.Kind = OpAdd
			op.New = m["value"]
		case "remove":
			op.Kind = OpRemove
		case "replace":
			op.Kind = OpReplace
			op.New = m["value"]
		case "move":
			op.Kind = OpMove
			op.Old = m["from"]
		case "copy":
			op.Kind = OpCopy
			op.Old = m["from"]
		case "log":
			op.Kind = OpLog
			op.New = m["value"]
		default:
			continue // unknown op, skip
		}

		res.Operations = append(res.Operations, op)
	}
	return res, nil
}

// Edit returns a Builder for constructing a Patch[T]. The target argument is
// used only for type inference and is not stored; the builder produces a
// standalone Patch, not a live view of the target.
func Edit[T any](_ *T) *Builder[T] {
	return &Builder[T]{}
}

// Op is a pending patch operation. Obtain one from [Set], [Add], [Remove],
// [Move], or [Copy]; attach per-operation conditions with [Op.If] or
// [Op.Unless] before passing to [Builder.With].
type Op struct {
	op Operation
}

// If attaches a condition that must hold for this operation to be applied.
func (o Op) If(c *condition.Condition) Op {
	o.op.If = c
	return o
}

// Unless attaches a condition that must NOT hold for this operation to be applied.
func (o Op) Unless(c *condition.Condition) Op {
	o.op.Unless = c
	return o
}

// Set returns a type-safe replace operation.
func Set[T, V any](p Path[T, V], val V) Op {
	return Op{op: Operation{Kind: OpReplace, Path: p.String(), New: val}}
}

// Add returns a type-safe add (insert) operation.
func Add[T, V any](p Path[T, V], val V) Op {
	return Op{op: Operation{Kind: OpAdd, Path: p.String(), New: val}}
}

// Remove returns a type-safe remove operation.
func Remove[T, V any](p Path[T, V]) Op {
	return Op{op: Operation{Kind: OpRemove, Path: p.String()}}
}

// Move returns a type-safe move operation that relocates the value at from to to.
// Both paths must share the same value type V.
func Move[T, V any](from, to Path[T, V]) Op {
	return Op{op: Operation{Kind: OpMove, Path: to.String(), Old: from.String()}}
}

// Copy returns a type-safe copy operation that duplicates the value at from to to.
// Both paths must share the same value type V.
func Copy[T, V any](from, to Path[T, V]) Op {
	return Op{op: Operation{Kind: OpCopy, Path: to.String(), Old: from.String()}}
}

// Builder constructs a [Patch] via a fluent chain.
type Builder[T any] struct {
	global *condition.Condition
	ops    []Operation
}

// Guard sets the global guard condition on the patch. If Guard has already been
// called, the new condition is ANDed with the existing one rather than
// replacing it — calling Guard twice is equivalent to Guard(And(c1, c2)).
func (b *Builder[T]) Guard(c *condition.Condition) *Builder[T] {
	if b.global == nil {
		b.global = c
	} else {
		b.global = And(b.global, c)
	}
	return b
}

// With appends one or more operations to the patch being built.
// Obtain operations from the typed constructors [Set], [Add], [Remove],
// [Move], and [Copy]; per-operation conditions can be attached with
// [Op.If] and [Op.Unless] before passing here.
func (b *Builder[T]) With(ops ...Op) *Builder[T] {
	for _, o := range ops {
		b.ops = append(b.ops, o.op)
	}
	return b
}

// Log appends a log operation.
func (b *Builder[T]) Log(msg string) *Builder[T] {
	b.ops = append(b.ops, Operation{
		Kind: OpLog,
		Path: "/",
		New:  msg,
	})
	return b
}

// Build assembles and returns the completed Patch.
func (b *Builder[T]) Build() Patch[T] {
	return Patch[T]{
		Guard:      b.global,
		Operations: b.ops,
	}
}

// Eq creates an equality condition.
func Eq[T, V any](p Path[T, V], val V) *condition.Condition {
	return &condition.Condition{Path: p.String(), Op: condition.Eq, Value: val}
}

// Ne creates a non-equality condition.
func Ne[T, V any](p Path[T, V], val V) *condition.Condition {
	return &condition.Condition{Path: p.String(), Op: condition.Ne, Value: val}
}

// Gt creates a greater-than condition.
func Gt[T, V any](p Path[T, V], val V) *condition.Condition {
	return &condition.Condition{Path: p.String(), Op: condition.Gt, Value: val}
}

// Ge creates a greater-than-or-equal condition.
func Ge[T, V any](p Path[T, V], val V) *condition.Condition {
	return &condition.Condition{Path: p.String(), Op: condition.Ge, Value: val}
}

// Lt creates a less-than condition.
func Lt[T, V any](p Path[T, V], val V) *condition.Condition {
	return &condition.Condition{Path: p.String(), Op: condition.Lt, Value: val}
}

// Le creates a less-than-or-equal condition.
func Le[T, V any](p Path[T, V], val V) *condition.Condition {
	return &condition.Condition{Path: p.String(), Op: condition.Le, Value: val}
}

// Exists creates a condition that checks if a path exists.
func Exists[T, V any](p Path[T, V]) *condition.Condition {
	return &condition.Condition{Path: p.String(), Op: condition.Exists}
}

// In creates a condition that checks if a value is in a list.
func In[T, V any](p Path[T, V], vals []V) *condition.Condition {
	return &condition.Condition{Path: p.String(), Op: condition.In, Value: vals}
}

// Matches creates a regex condition.
func Matches[T, V any](p Path[T, V], regex string) *condition.Condition {
	return &condition.Condition{Path: p.String(), Op: condition.Matches, Value: regex}
}

// Type creates a type-check condition.
func Type[T, V any](p Path[T, V], typeName string) *condition.Condition {
	return &condition.Condition{Path: p.String(), Op: condition.Type, Value: typeName}
}

// And combines multiple conditions with logical AND.
func And(conds ...*condition.Condition) *condition.Condition {
	return &condition.Condition{Op: condition.And, Sub: conds}
}

// Or combines multiple conditions with logical OR.
func Or(conds ...*condition.Condition) *condition.Condition {
	return &condition.Condition{Op: condition.Or, Sub: conds}
}

// Not inverts a condition.
func Not(c *condition.Condition) *condition.Condition {
	return &condition.Condition{Op: condition.Not, Sub: []*condition.Condition{c}}
}
