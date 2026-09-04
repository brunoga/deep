// Package shapes holds models exercising structural shapes that the generator
// has historically mishandled: embedded fields, keyed slices, nested maps,
// pointer-valued maps, and map keys needing RFC 6901 escaping.
package shapes

//go:generate go run github.com/brunoga/deep/v6/cmd/deep-gen -type=Doc,Meta,Entry,Payload -output doc_deep.go .

// Meta is embedded in Doc; embedded fields are addressed by type name.
type Meta struct {
	Version int    `json:"version"`
	Author  string `json:"author"`
}

// Entry is a keyed slice element.
type Entry struct {
	ID string `deep:"key" json:"id"`
	N  int    `json:"n"`
}

// Payload is deliberately non-comparable (it carries a slice) and used
// atomically: atomic fields must diff as a whole-value replace, never with ==.
type Payload struct {
	Bytes []byte `json:"bytes"`
	Label string `json:"label"`
}

type Doc struct {
	Meta                              // embedded
	Entries []Entry                   `json:"entries"`
	Nested  map[string]map[string]int `json:"nested"`
	Stages  map[string]*Meta          `json:"stages"`
	Side    *Meta                     `json:"side"`
	Blob    Payload                   `json:"blob" deep:"atomic"`
	Scores  map[string]int            `json:"scores"`
	Name    string                    `json:"name"`
}
