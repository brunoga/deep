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
	// OpAlias makes Path hold the same object From resolves to — sharing,
	// where OpCopy makes an independent deep copy. Diff emits it for the second
	// and later routes to a value referenced more than once.
	OpAlias = engine.OpAlias
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
			b.WriteString(fmt.Sprintf("Move %s to %s", op.From, op.Path))
		case OpCopy:
			b.WriteString(fmt.Sprintf("Copy %s to %s", op.From, op.Path))
		case OpAlias:
			b.WriteString(fmt.Sprintf("Alias %s to %s", op.From, op.Path))
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
		// OpLog has no state effect; its inverse is itself a no-op. Skip rather
		// than emit a zero-valued Operation (which would default to OpAdd).
		if op.Kind == OpLog {
			continue
		}
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
			rev.Path = op.From
			rev.From = op.Path
			// If the destination had a displaced value at apply-time, restore
			// it via Old; reversing the move strands that value otherwise.
			if op.Old != nil {
				rev.New = op.Old
			}
		case OpCopy, OpAlias:
			// If we know the prior destination value, restore it with Replace;
			// otherwise the destination was empty pre-copy so Remove suffices.
			// (Reversing an alias never needs to be an alias itself: the prior
			// destination value is a plain value to put back.)
			if op.Old != nil {
				rev.Kind = OpReplace
				rev.Old = op.New
				rev.New = op.Old
			} else {
				rev.Kind = OpRemove
				rev.Old = op.New
			}
		}
		res.Operations = append(res.Operations, rev)
	}
	restoreSiblingAddOrder(res.Operations)
	return res
}

// restoreSiblingAddOrder puts runs of adds into one collection back in the
// order they had before the operations were reversed.
//
// Reversing the order is what makes a patch undo correctly when its operations
// depend on each other: a chain of moves has to be walked backwards. A run of
// adds into one collection is the opposite case. Applying an add to a keyed
// slice appends, so reversing the run reverses the sequence it rebuilds —
// undoing the removal of [a b c] put back [c b a].
//
// Sibling adds cannot depend on each other, so restoring their order is safe
// where it is not necessary: for a map it makes no difference at all.
func restoreSiblingAddOrder(ops []Operation) {
	for i := 0; i < len(ops); {
		parent, ok := parentPath(ops[i].Path)
		if !ok || ops[i].Kind != OpAdd {
			i++
			continue
		}
		j := i + 1
		for j < len(ops) && ops[j].Kind == OpAdd {
			p, ok := parentPath(ops[j].Path)
			if !ok || p != parent {
				break
			}
			j++
		}
		for l, r := i, j-1; l < r; l, r = l+1, r-1 {
			ops[l], ops[r] = ops[r], ops[l]
		}
		i = j
	}
}

// parentPath returns the path of the collection or struct an operation's path
// sits inside. It reports false for a root-level path, which has no parent to
// share with a sibling.
func parentPath(path string) (string, bool) {
	i := strings.LastIndex(path, "/")
	if i <= 0 {
		return "", false
	}
	return path[:i], true
}

// ToJSONPatch returns a JSON Patch representation compatible with RFC 6902
// and the github.com/brunoga/jsonpatch extensions.
//
// A strict patch (see [Patch.AsStrict]) emits a test operation carrying the
// expected Old value before each replace and remove — RFC 6902's native way
// of expressing precondition checks — so strictness survives the round-trip
// through [ParseJSONPatch].
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
		if p.Strict && (op.Kind == OpReplace || op.Kind == OpRemove) && op.Old != nil {
			res = append(res, map[string]any{
				"op":    "test",
				"path":  op.Path,
				"value": op.Old,
			})
		}
		m := map[string]any{
			"op":   op.Kind.String(),
			"path": op.Path,
		}

		switch op.Kind {
		case OpAdd, OpReplace:
			m["value"] = op.New
		case OpMove, OpCopy:
			m["from"] = op.From
		case OpAlias:
			// RFC 6902 has no aliasing — JSON values have no identity to
			// share. "copy" is the closest faithful translation: the values
			// come out right, the sharing does not survive the trip.
			m["op"] = "copy"
			m["from"] = op.From
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
//
// Wire conventions:
//
//   - A leading {"op":"test","path":"/","if":<predicate>} entry is interpreted
//     as the global Patch.Guard rather than a regular test op, mirroring what
//     ToJSONPatch emits. To round-trip a regular test op at "/", attach it via
//     Builder rather than serialising it as the document's first entry.
//   - A {"op":"test","path":<p>,"value":<v>} entry immediately followed by a
//     replace or remove at the same path becomes that operation's Old value
//     and marks the patch strict, mirroring what ToJSONPatch emits for a
//     strict patch.
//   - Any other test op — and any unknown op — is dropped: the operation model
//     has no general test kind.
func ParseJSONPatch[T any](data []byte) (Patch[T], error) {
	var ops []map[string]any
	if err := json.Unmarshal(data, &ops); err != nil {
		return Patch[T]{}, fmt.Errorf("ParseJSONPatch: %w", err)
	}
	res := Patch[T]{}
	var pendingTest map[string]any // last test-with-value entry, awaiting its op
	for i, m := range ops {
		opStr, _ := m["op"].(string)
		path, _ := m["path"].(string)

		// Global condition encoding: ONLY the leading entry (matched as
		// op==test, path=="/", "if" present) is lifted into Guard.
		if i == 0 && opStr == "test" && path == "/" {
			if ifPred, ok := m["if"].(map[string]any); ok {
				res.Guard = condition.FromPredicate(ifPred)
				continue
			}
		}

		// Strict Old-value encoding: remember a test-with-value entry and
		// attach it to the operation that follows at the same path.
		if opStr == "test" {
			if _, ok := m["value"]; ok {
				pendingTest = m
				continue
			}
		}
		test := pendingTest
		pendingTest = nil

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
			if s, ok := m["from"].(string); ok {
				op.From = s
			}
		case "copy":
			op.Kind = OpCopy
			if s, ok := m["from"].(string); ok {
				op.From = s
			}
		case "log":
			op.Kind = OpLog
			op.New = m["value"]
		default:
			continue // unknown op, skip
		}

		if test != nil && path == test["path"] && (op.Kind == OpReplace || op.Kind == OpRemove) {
			op.Old = test["value"]
			res.Strict = true
		}

		res.Operations = append(res.Operations, op)
	}
	return res, nil
}

// NewPatch returns a Builder for constructing a Patch[T].
//
//	p := deep.NewPatch[Listing]().
//		With(deep.Set(pricePath, 2499).If(deep.Eq(pricePath, 1999))).
//		Build()
//
// The builder produces a standalone patch, not a live view of anything.
func NewPatch[T any]() *Builder[T] {
	return &Builder[T]{}
}

// Edit returns a Builder for constructing a Patch[T]. The target argument is
// used only for type inference and is not stored; the builder produces a
// standalone Patch, not a live view of the target.
//
// Deprecated: use [NewPatch], which infers T from the type argument and does
// not need a value to point at. Edit's parameter exists only so that T could be
// inferred from a variable, which meant writing deep.Edit[Listing](nil) when
// there was no variable to hand.
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
	return Op{op: Operation{Kind: OpMove, Path: to.String(), From: from.String()}}
}

// Copy returns a type-safe copy operation that duplicates the value at from to to.
// Both paths must share the same value type V.
func Copy[T, V any](from, to Path[T, V]) Op {
	return Op{op: Operation{Kind: OpCopy, Path: to.String(), From: from.String()}}
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
