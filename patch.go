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

// Register registers the Patch implementation for type T with the gob package.
func Register[T any]() {
	gob.Register(Patch[T]{})
	gob.Register(LWW[T]{})
	// We also register common collection types that might be used in 'any' fields
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
	// Root is the root object type the patch applies to.
	// Used for type safety during Apply.
	_ [0]T

	// Global condition that must be met before applying the patch.
	Condition *Condition `json:"cond,omitempty"`

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
	Path      string     `json:"p"` // Still uses string path for serialization, but created via Selectors.
	Old       any        `json:"o,omitempty"`
	New       any        `json:"n,omitempty"`
	Timestamp hlc.HLC    `json:"t,omitempty"` // Integrated causality
	If        *Condition `json:"if,omitempty"`
	Unless    *Condition `json:"un,omitempty"`
	Strict    bool       `json:"s,omitempty"` // Propagated from Patch
}

// Condition represents a serializable predicate for conditional application.
type Condition struct {
	Path  string       `json:"p,omitempty"`
	Op    string       `json:"o"` // "eq", "ne", "gt", "lt", "exists", "in", "log", "matches", "type", "and", "or", "not"
	Value any          `json:"v,omitempty"`
	Apply []*Condition `json:"apply,omitempty"` // For logical operators
}

// NewPatch returns a new, empty patch for type T.
func NewPatch[T any]() Patch[T] {
	return Patch[T]{}
}

// WithStrict returns a new patch with the strict flag set.
func (p Patch[T]) WithStrict(strict bool) Patch[T] {
	p.Strict = strict
	for i := range p.Operations {
		p.Operations[i].Strict = strict
	}
	return p
}

// WithCondition returns a new patch with the global condition set.
func (p Patch[T]) WithCondition(c *Condition) Patch[T] {
	p.Condition = c
	return p
}

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
	if p.Condition != nil {
		res = append(res, map[string]any{
			"op":   "test",
			"path": "/",
			"if":   p.Condition.toPredicateInternal(),
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
	case "<":
		op = "less"
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
		for _, sub := range c.Apply {
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

// LWW represents a Last-Write-Wins register for type T.
type LWW[T any] struct {
	Value     T       `json:"v"`
	Timestamp hlc.HLC `json:"t"`
}
