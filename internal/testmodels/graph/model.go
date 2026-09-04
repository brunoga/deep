// Package graph holds models whose types can reach themselves: directly, and
// through every reference kind the generator emits code for — pointers, slices
// of pointers, slices of values, maps of pointers and maps of values.
//
// Types like these are why generated Clone carries a deep.CloneMemo, generated
// Equal a deep.VisitSet and generated Diff a deep.DiffMemo. The generator
// decides that per type by looking for cycles and multiply-reachable references
// in the package's type graph, so this package also keeps a type where neither
// is possible: it must still generate the plain, memo-free code.
package graph

//go:generate go run github.com/brunoga/deep/v6/cmd/deep-gen -type=Node,Edge,Label -output node_deep.go .

// Label cannot reach a cycle: nothing it holds leads back to a Label. Its
// generated methods take no memo.
type Label struct {
	Text string `json:"text"`
	Rank int    `json:"rank"`
}

// Edge reaches a cycle without naming itself: it points at a Node, and a Node
// holds Edges.
type Edge struct {
	Weight int   `json:"weight"`
	To     *Node `json:"to"`
}

// Node reaches itself five different ways, so every reference-handling branch
// of the generated code has to thread the memo.
type Node struct {
	Name     string            `json:"name"`
	Label    Label             `json:"label"`    // value struct, not itself recursive
	Next     *Node             `json:"next"`     // directly
	Peers    []*Node           `json:"peers"`    // through a slice of pointers
	Children map[string]*Node  `json:"children"` // through a map of pointers
	Edges    []Edge            `json:"edges"`    // through a slice of values
	Links    map[string]Edge   `json:"links"`    // through a map of values
	Tags     map[string]string `json:"tags"`     // plain reference types, no cycle
	Scores   []int             `json:"scores"`
}
