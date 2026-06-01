package engine

import "github.com/brunoga/deep/v5/condition"

// Operation represents a single change within a Patch.
//
// Field semantics by Kind:
//   - OpAdd:     Path = target;        New = added value.
//   - OpRemove:  Path = target;        Old = removed value (prior).
//   - OpReplace: Path = target;        Old = prior value;          New = replacement.
//   - OpMove:    Path = destination;   From = source path;         Old = displaced value at Path (optional).
//   - OpCopy:    Path = destination;   From = source path;         Old = displaced value at Path (optional).
//   - OpLog:     Path = scope;         New = log message.
//
// Old for OpMove/OpCopy was previously the source-path string; that role now
// belongs to From, freeing Old to carry the prior destination value
// (necessary for full Reverse fidelity when the destination was non-empty).
type Operation struct {
	Kind   OpKind               `json:"k"`
	Path   string               `json:"p"`
	From   string               `json:"f,omitempty"`
	Old    any                  `json:"o,omitempty"`
	New    any                  `json:"n,omitempty"`
	If     *condition.Condition `json:"if,omitempty"`
	Unless *condition.Condition `json:"un,omitempty"`
	// Strict is stamped from Patch.Strict at apply time; not serialized.
	Strict bool `json:"-"`
}
