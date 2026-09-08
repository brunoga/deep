// Package conformance generates the cross-language corpus: cases produced by
// this implementation, with the result it produces, so another implementation
// can check that it agrees.
//
// The model types carry generated code deliberately. The generator emits
// paths using JSON names ("/status"), which is what a JSON document's keys
// are called in every language; the reflection engine emits Go field names
// ("/Status"), which only Go can resolve. Generated code is therefore the
// interop contract, and the corpus is generated through it.
package conformance

//go:generate go run github.com/brunoga/deep/v6/cmd/deep-gen -type=Doc,Item,Meta,Odd -output model_deep.go .

// Item is an element with an identity: diffs address it as /items/<id>.
type Item struct {
	ID    string `deep:"key" json:"id"`
	Label string `json:"label"`
	Count int    `json:"count"`
	Done  bool   `json:"done"`
}

// Meta is a nested object.
type Meta struct {
	Owner string `json:"owner"`
	Level int    `json:"level"`
}

// Plain is deliberately absent from the go:generate list above, so a diff of
// it goes through the reflection engine. Its cases prove that both engines
// name fields the same way — a reflection engine that named them the Go way
// would produce patches no other language could apply.
type Plain struct {
	Label  string         `json:"label"`
	Depth  int            `json:"depth"`
	Nested PlainNested    `json:"nested"`
	Values map[string]int `json:"values"`
	// No json tag: there is nothing to call this but its Go name.
	Untagged string
	// Kept out of the document, and so out of patches, equality and clones.
	Secret string `json:"-"`
}

// PlainNested is likewise ungenerated.
type PlainNested struct {
	Owner string `json:"owner"`
}

// Odd carries JSON names that need RFC 6901 escaping when they become path
// tokens. It is generated, so its case covers the generator's escaping; the
// reflection engine's is covered by a unit test in the root package. Both
// must produce the same paths, or a patch means one thing to a Go peer and
// another to everyone else.
type Odd struct {
	Ratio int    `json:"a/b"`
	Tilde int    `json:"c~d"`
	Plain string `json:"plain"`
}

// Doc is the corpus model: scalars, a nested struct, a keyed slice, a plain
// slice, and a map — one of each shape a patch has to address.
//
// No field uses `omitempty`, deliberately. It makes a zero value and an
// absent field the same bytes, so a patch that sets a field to its zero
// produces JSON a receiver cannot distinguish from one where the field was
// never there — and a JavaScript replica then disagrees with Go about the
// document while both applied the same operations. A model that syncs across
// languages should leave it off; see docs/wire-patch.md.
type Doc struct {
	Title  string            `json:"title"`
	Status string            `json:"status"`
	Score  int               `json:"score"`
	Meta   Meta              `json:"meta"`
	Items  []Item            `json:"items"`
	Tags   []string          `json:"tags"`
	Fields map[string]string `json:"fields"`
}
