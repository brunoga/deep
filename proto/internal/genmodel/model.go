// Package genmodel is a deep-gen'd struct holding protobuf messages, proving
// that generated code hands family-owned fields to the family: a one-field
// change to the message diffs to one operation naming that field, not a
// whole-message replace.
package genmodel

//go:generate go run github.com/brunoga/deep/v6/cmd/deep-gen -type=Doc -output doc_deep.go .

import "google.golang.org/protobuf/types/known/structpb"

type Doc struct {
	Name string           `json:"name"`
	Meta *structpb.Struct `json:"meta"`
}
