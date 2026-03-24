package engine

import "github.com/brunoga/deep/v5/condition"

// Operation represents a single change within a Patch.
type Operation struct {
	Kind   OpKind               `json:"k"`
	Path   string               `json:"p"`
	Old    any                  `json:"o,omitempty"`
	New    any                  `json:"n,omitempty"`
	If     *condition.Condition `json:"if,omitempty"`
	Unless *condition.Condition `json:"un,omitempty"`
	// Strict is stamped from Patch.Strict at apply time; not serialized.
	Strict bool `json:"-"`
}
