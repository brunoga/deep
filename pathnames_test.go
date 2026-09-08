package deep_test

import (
	"encoding/json"
	"strings"
	"testing"

	deep "github.com/brunoga/deep/v6"
)

// tagged has no generated code, so it exercises the reflection engine.
type tagged struct {
	Name     string `json:"name"`
	Nested   inner  `json:"nested"`
	Untagged string
	Secret   string `json:"-"`
	Derived  string `deep:"-" json:"derived"`
}

type inner struct {
	Level int `json:"level"`
}

// Paths name fields the way the document does, so a patch produced by the
// reflection engine addresses the same field as one produced by generated
// code or by a type-safe selector.
func TestReflectionPathsUseJSONNames(t *testing.T) {
	a := tagged{Name: "ana", Nested: inner{Level: 1}, Untagged: "u"}
	b := tagged{Name: "bo", Nested: inner{Level: 2}, Untagged: "v"}

	p, err := deep.Diff(a, b)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, op := range p.Operations {
		got[op.Path] = true
	}
	for _, want := range []string{"/name", "/nested/level"} {
		if !got[want] {
			t.Errorf("missing %s in %v", want, got)
		}
	}
	// A field with no json tag keeps its Go name: there is nothing else to
	// call it.
	if !got["/Untagged"] {
		t.Errorf("untagged field should keep its Go name: %v", got)
	}
}

// The motivating case: a server allowlisting a field with the library's own
// selector must admit a patch that changes exactly that field. Before paths
// agreed, this refused the patch whenever the type had no generated code.
func TestSelectorPathsMatchDiffPaths(t *testing.T) {
	a := tagged{Name: "ana"}
	b := tagged{Name: "bo"}
	namePath := deep.Field(func(v *tagged) *string { return &v.Name })

	p, err := deep.Diff(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if p.Operations[0].Path != namePath.String() {
		t.Fatalf("diff says %q, selector says %q", p.Operations[0].Path, namePath.String())
	}

	target := a
	if err := deep.Apply(&target, p, deep.WithAllowedPaths(namePath.String())); err != nil {
		t.Fatalf("allowlist built from a selector refused the patch: %v", err)
	}
	if target.Name != "bo" {
		t.Fatalf("name = %q", target.Name)
	}
}

// A field kept out of the document is kept out of the patch, whichever engine
// produced it. The library's own example advertises this for a password hash;
// it was true only of generated code until the reflection engine read the tag
// as well.
func TestJSONDashIsInvisibleToReflection(t *testing.T) {
	a := tagged{Name: "same", Secret: "hash-OLD", Derived: "x"}
	b := tagged{Name: "same", Secret: "hash-NEW", Derived: "y"}

	p, err := deep.Diff(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if !p.IsEmpty() {
		t.Fatalf("ignored fields produced operations: %s", p)
	}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "hash-") {
		t.Fatalf("a secret travelled inside the patch: %s", data)
	}

	if !deep.Equal(a, b) {
		t.Error("values differing only in ignored fields should compare equal")
	}
	// Invisible means invisible to Clone too — the sharp edge the tag carries.
	if c := deep.Clone(a); c.Secret != "" || c.Derived != "" {
		t.Errorf("clone carried ignored fields: %+v", c)
	}
}

// Patches written before paths agreed still apply: the appliers accept a Go
// field name as well as a JSON one, so a stored audit log keeps working.
func TestLegacyGoNamePathsStillApply(t *testing.T) {
	target := tagged{Name: "ana", Nested: inner{Level: 1}}
	legacy := deep.Patch[tagged]{Operations: []deep.Operation{
		{Kind: deep.OpReplace, Path: "/Name", Old: "ana", New: "bo"},
		{Kind: deep.OpReplace, Path: "/Nested/Level", Old: 1, New: 9},
	}}
	if err := deep.Apply(&target, legacy); err != nil {
		t.Fatalf("legacy patch: %v", err)
	}
	if target.Name != "bo" || target.Nested.Level != 9 {
		t.Fatalf("legacy patch did not apply: %+v", target)
	}

	// Including through the wire, where values arrive still encoded.
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	var wire deep.Patch[tagged]
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
	fresh := tagged{Name: "ana", Nested: inner{Level: 1}}
	if err := deep.Apply(&fresh, wire); err != nil {
		t.Fatalf("legacy patch from the wire: %v", err)
	}
	if fresh.Name != "bo" || fresh.Nested.Level != 9 {
		t.Fatalf("legacy wire patch did not apply: %+v", fresh)
	}
}

// Conditions, which resolve paths rather than emit them, work with either
// form — so a guard written against the old names keeps holding.
func TestConditionsAcceptBothNameForms(t *testing.T) {
	v := tagged{Name: "ana"}
	jsonForm := deep.Patch[tagged]{Operations: []deep.Operation{{Kind: deep.OpReplace, Path: "/name", New: "x"}}}.
		WithGuard(deep.Eq(deep.PathString[tagged, string]("/name"), "ana"))
	goForm := deep.Patch[tagged]{Operations: []deep.Operation{{Kind: deep.OpReplace, Path: "/name", New: "y"}}}.
		WithGuard(deep.Eq(deep.PathString[tagged, string]("/Name"), "ana"))

	for name, p := range map[string]deep.Patch[tagged]{"json": jsonForm, "go": goForm} {
		target := v
		if err := deep.Apply(&target, p); err != nil {
			t.Errorf("%s-named guard: %v", name, err)
		}
		if target.Name == "ana" {
			t.Errorf("%s-named guard did not hold", name)
		}
	}
}

// escapeNeeded has no generated code, so this exercises the reflection
// engine. A JSON tag is arbitrary text, and one holding "/" or "~" has to be
// escaped into the path or it addresses a different place — or nothing.
type escapeNeeded struct {
	Ratio int    `json:"a/b"`
	Tilde int    `json:"c~d"`
	Plain string `json:"plain"`
}

func TestJSONNamesAreEscapedIntoPaths(t *testing.T) {
	a := escapeNeeded{Ratio: 1, Tilde: 1, Plain: "a"}
	b := escapeNeeded{Ratio: 2, Tilde: 2, Plain: "b"}

	p, err := deep.Diff(a, b)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, op := range p.Operations {
		got[op.Path] = true
	}
	for _, want := range []string{"/a~1b", "/c~0d", "/plain"} {
		if !got[want] {
			t.Errorf("missing %s in %v", want, got)
		}
	}

	// The round trip is the point: an unescaped "/a/b" addresses a field "a"
	// holding a "b", which is nothing, and the apply fails halfway.
	target := a
	if err := deep.Apply(&target, p); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if target != b {
		t.Fatalf("diff and apply did not round-trip: %+v", target)
	}

	back := target
	if err := deep.Apply(&back, p.Reverse()); err != nil {
		t.Fatalf("reverse: %v", err)
	}
	if back != a {
		t.Fatalf("reverse did not return to the start: %+v", back)
	}
}

// json:"-," is not json:"-": encoding/json reads it as a field genuinely
// named "-", and deep has to agree about what the document contains.
type dashNamed struct {
	Kept    string `json:"-,"`
	Skipped string `json:"-"`
}

func TestDashCommaNamesAFieldRatherThanHidingIt(t *testing.T) {
	a := dashNamed{Kept: "old", Skipped: "secret-old"}
	b := dashNamed{Kept: "new", Skipped: "secret-new"}

	p, err := deep.Diff(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Operations) != 1 || p.Operations[0].Path != "/-" {
		t.Fatalf("want one operation at /-, got %s", p)
	}

	// And the document agrees: encoding/json emits the field as "-" too.
	data, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"-":"old"`) {
		t.Fatalf("encoding/json disagrees about the document: %s", data)
	}
}
