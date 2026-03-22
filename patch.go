package deep

import (
	"encoding/gob"
	"encoding/json"
	"fmt"
	"github.com/brunoga/deep/v5/crdt/hlc"
	"github.com/brunoga/deep/v5/internal/engine"
	"strings"
)

func init() {
	gob.Register(&Condition{})
	gob.Register(Operation{})
	gob.Register(hlc.HLC{})
}

// Register registers the Patch and LWW types for T with the gob package.
// It also registers []T and map[string]T because gob requires concrete types
// to be registered when they appear inside interface-typed fields (such as
// Operation.Old / Operation.New). Call Register[T] for every type T that
// will flow through those fields during gob encoding.
func Register[T any]() {
	gob.Register(Patch[T]{})
	gob.Register(LWW[T]{})
	gob.Register([]T{})
	gob.Register(map[string]T{})
}

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
	// in this patch is applied. Set via WithGuard or Builder.Where.
	Guard *Condition `json:"cond,omitempty"`

	// Operations is a flat list of changes.
	Operations []Operation `json:"ops"`

	// Metadata stores optional properties like timestamps or IDs.
	Metadata map[string]any `json:"meta,omitempty"`

	// Strict mode enables Old value verification.
	Strict bool `json:"strict,omitempty"`
}

// Operation represents a single change.
type Operation struct {
	Kind      OpKind     `json:"k"`
	Path      string     `json:"p"` // JSON Pointer path; created via Field selectors.
	Old       any        `json:"o,omitempty"`
	New       any        `json:"n,omitempty"`
	Timestamp *hlc.HLC   `json:"t,omitempty"` // Integrated causality via HLC; nil means no timestamp.
	If        *Condition `json:"if,omitempty"`
	Unless    *Condition `json:"un,omitempty"`

	// Strict is stamped from Patch.Strict at apply time; not serialized.
	Strict bool `json:"-"`
}

// Condition operator constants. Use these when constructing Condition values
// manually. Prefer the typed builder functions (Eq, Ne, And, etc.) where possible.
const (
	CondEq      = "=="
	CondNe      = "!="
	CondGt      = ">"
	CondLt      = "<"
	CondGe      = ">="
	CondLe      = "<="
	CondExists  = "exists"
	CondIn      = "in"
	CondMatches = "matches"
	CondType    = "type"
	CondLog     = "log"
	CondAnd     = "and"
	CondOr      = "or"
	CondNot     = "not"
)

// Condition represents a serializable predicate for conditional application.
type Condition struct {
	Path  string       `json:"p,omitempty"`
	Op    string       `json:"o"` // see Op* constants above
	Value any          `json:"v,omitempty"`
	Sub   []*Condition `json:"apply,omitempty"` // Sub-conditions for logical operators (and, or, not)
}

// NewPatch returns a new, empty patch for type T.
func NewPatch[T any]() Patch[T] {
	return Patch[T]{}
}

// IsEmpty reports whether the patch contains no operations.
func (p Patch[T]) IsEmpty() bool {
	return len(p.Operations) == 0
}

// WithStrict returns a new patch with the strict flag set.
func (p Patch[T]) WithStrict(strict bool) Patch[T] {
	p.Strict = strict
	return p
}

// WithGuard returns a new patch with the global guard condition set.
func (p Patch[T]) WithGuard(c *Condition) Patch[T] {
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
			Path:      op.Path,
			Timestamp: op.Timestamp,
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
			"if":   p.Guard.toPredicateInternal(),
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
			m["if"] = op.If.toPredicateInternal()
		}
		if op.Unless != nil {
			m["unless"] = op.Unless.toPredicateInternal()
		}

		res = append(res, m)
	}

	return json.Marshal(res)
}

func (c *Condition) toPredicateInternal() map[string]any {
	if c == nil {
		return nil
	}

	op := c.Op
	switch op {
	case "==":
		op = "test"
	case "!=":
		// Not equal is a 'not' predicate in some extensions
		return map[string]any{
			"op": "not",
			"apply": []map[string]any{
				{"op": "test", "path": c.Path, "value": c.Value},
			},
		}
	case ">":
		op = "more"
	case ">=":
		op = "more-or-equal"
	case "<":
		op = "less"
	case "<=":
		op = "less-or-equal"
	case "exists":
		op = "defined"
	case "in":
		op = "contains"
	case "log":
		op = "log"
	case "matches":
		op = "matches"
	case "type":
		op = "type"
	case "and", "or", "not":
		res := map[string]any{
			"op": op,
		}
		var apply []map[string]any
		for _, sub := range c.Sub {
			apply = append(apply, sub.toPredicateInternal())
		}
		res["apply"] = apply
		return res
	}

	return map[string]any{
		"op":    op,
		"path":  c.Path,
		"value": c.Value,
	}
}

// fromPredicateInternal is the inverse of toPredicateInternal.
func fromPredicateInternal(m map[string]any) *Condition {
	if m == nil {
		return nil
	}
	op, _ := m["op"].(string)
	path, _ := m["path"].(string)
	value := m["value"]

	switch op {
	case "test":
		return &Condition{Path: path, Op: "==", Value: value}
	case "not":
		// Could be encoded != or a logical not.
		// If it wraps a single test on the same path, treat as !=.
		if apply, ok := m["apply"].([]any); ok && len(apply) == 1 {
			if inner, ok := apply[0].(map[string]any); ok {
				if inner["op"] == "test" {
					innerPath, _ := inner["path"].(string)
					return &Condition{Path: innerPath, Op: "!=", Value: inner["value"]}
				}
			}
		}
		return &Condition{Op: "not", Sub: parseApply(m["apply"])}
	case "more":
		return &Condition{Path: path, Op: ">", Value: value}
	case "more-or-equal":
		return &Condition{Path: path, Op: ">=", Value: value}
	case "less":
		return &Condition{Path: path, Op: "<", Value: value}
	case "less-or-equal":
		return &Condition{Path: path, Op: "<=", Value: value}
	case "defined":
		return &Condition{Path: path, Op: "exists"}
	case "contains":
		return &Condition{Path: path, Op: "in", Value: value}
	case "and", "or":
		return &Condition{Op: op, Sub: parseApply(m["apply"])}
	default:
		// log, matches, type — same op name, pass through
		return &Condition{Path: path, Op: op, Value: value}
	}
}

func parseApply(raw any) []*Condition {
	items, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]*Condition, 0, len(items))
	for _, item := range items {
		if m, ok := item.(map[string]any); ok {
			if c := fromPredicateInternal(m); c != nil {
				out = append(out, c)
			}
		}
	}
	return out
}

// FromJSONPatch parses a JSON Patch document (RFC 6902 plus deep extensions)
// back into a Patch[T]. This is the inverse of Patch.ToJSONPatch().
func FromJSONPatch[T any](data []byte) (Patch[T], error) {
	var ops []map[string]any
	if err := json.Unmarshal(data, &ops); err != nil {
		return Patch[T]{}, fmt.Errorf("FromJSONPatch: %w", err)
	}
	res := Patch[T]{}
	for _, m := range ops {
		opStr, _ := m["op"].(string)
		path, _ := m["path"].(string)

		// Global condition is encoded as a test op on "/" with an "if" predicate.
		if opStr == "test" && path == "/" {
			if ifPred, ok := m["if"].(map[string]any); ok {
				res.Guard = fromPredicateInternal(ifPred)
			}
			continue
		}

		op := Operation{Path: path}

		// Per-op conditions
		if ifPred, ok := m["if"].(map[string]any); ok {
			op.If = fromPredicateInternal(ifPred)
		}
		if unlessPred, ok := m["unless"].(map[string]any); ok {
			op.Unless = fromPredicateInternal(unlessPred)
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

// LWW represents a Last-Write-Wins register for type T.
type LWW[T any] struct {
	Value     T       `json:"v"`
	Timestamp hlc.HLC `json:"t"`
}

// Set updates the register's value and timestamp if ts is after the current
// timestamp. Returns true if the update was accepted.
func (l *LWW[T]) Set(v T, ts hlc.HLC) bool {
	if ts.After(l.Timestamp) {
		l.Value = v
		l.Timestamp = ts
		return true
	}
	return false
}
