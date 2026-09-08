package conformance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	deep "github.com/brunoga/deep/v6"
	"github.com/brunoga/deep/v6/condition"
)

// Case is one conformance case, as it appears on disk.
type Case struct {
	Name string `json:"name"`
	// Keys names the identity field of each keyed array, by the array's path.
	// Go reads this from a `deep:"key"` struct tag; other languages have to be
	// told, so the corpus carries it.
	Keys map[string]string `json:"keys,omitempty"`
	// Before is the document the patch applies to; After is what this
	// implementation produces.
	Before json.RawMessage `json:"before"`
	Patch  json.RawMessage `json:"patch"`
	After  json.RawMessage `json:"after"`
	// Applied, Skipped and Failed are the per-operation outcome counts.
	Applied int `json:"applied"`
	Skipped int `json:"skipped"`
	Failed  int `json:"failed"`
	// Reversible says whether applying the reverse of Patch to After returns
	// to Before. Patches whose operations lack recorded old values do not.
	Reversible bool `json:"reversible"`
}

func doc() Doc {
	return Doc{
		Title:  "quarterly report",
		Status: "open",
		Score:  3,
		Meta:   Meta{Owner: "ana", Level: 2},
		Items: []Item{
			{ID: "a1", Label: "draft", Count: 1},
			{ID: "b2", Label: "review", Count: 2},
		},
		Tags:   []string{"x", "y"},
		Fields: map[string]string{"region": "north", "odd/key~here": "yes"},
	}
}

// build assembles a case from a starting document and a patch, recording what
// this implementation does with it.
func build(t *testing.T, name string, before Doc, patch deep.Patch[Doc]) Case {
	t.Helper()
	after := deep.Clone(before)
	res, _ := deep.ApplyWithResult(&after, patch)
	applied, skipped, failed := res.Counts()

	// Reversibility is a property of the case, not an assumption: check it
	// rather than claiming it.
	reversible := false
	if failed == 0 && skipped == 0 {
		back := deep.Clone(after)
		if err := deep.Apply(&back, patch.Reverse()); err == nil {
			reversible = deep.Equal(back, before)
		}
	}

	marshal := func(v any) json.RawMessage {
		data, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return data
	}
	return Case{
		Name: name, Before: marshal(before), Patch: marshal(patch), After: marshal(after),
		Applied: applied, Skipped: skipped, Failed: failed, Reversible: reversible,
		Keys: map[string]string{"/items": "id"},
	}
}

// diffCase builds a case from two documents, which is how a patch usually
// comes to exist.
func diffCase(t *testing.T, name string, before Doc, edit func(*Doc)) Case {
	t.Helper()
	after := deep.Clone(before)
	edit(&after)
	patch, err := deep.Diff(before, after)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return build(t, name, before, patch)
}

func cases(t *testing.T) []Case {
	t.Helper()
	base := doc()
	path := func(p string) deep.Path[Doc, any] { return deep.PathString[Doc, any](p) }

	out := []Case{
		diffCase(t, "scalar replace", base, func(d *Doc) { d.Status = "closed" }),
		diffCase(t, "nested object field", base, func(d *Doc) { d.Meta.Level = 9 }),
		diffCase(t, "keyed element field", base, func(d *Doc) { d.Items[1].Count = 42 }),
		diffCase(t, "keyed element added", base, func(d *Doc) {
			d.Items = append(d.Items, Item{ID: "c3", Label: "final", Count: 3})
		}),
		diffCase(t, "keyed element removed", base, func(d *Doc) { d.Items = d.Items[:1] }),
		diffCase(t, "keyed elements reordered", base, func(d *Doc) {
			d.Items[0], d.Items[1] = d.Items[1], d.Items[0]
		}),
		diffCase(t, "plain slice changed", base, func(d *Doc) { d.Tags = []string{"x", "z", "w"} }),
		diffCase(t, "map value changed", base, func(d *Doc) { d.Fields["region"] = "south" }),
		diffCase(t, "map key added", base, func(d *Doc) { d.Fields["shift"] = "night" }),
		diffCase(t, "map key removed", base, func(d *Doc) { delete(d.Fields, "region") }),
		diffCase(t, "escaped map key changed", base, func(d *Doc) { d.Fields["odd/key~here"] = "no" }),
		diffCase(t, "several fields at once", base, func(d *Doc) {
			d.Title = "annual report"
			d.Score = 10
			d.Meta.Owner = "bo"
			d.Items[0].Done = true
			d.Fields["region"] = "east"
		}),
		diffCase(t, "empties a map", base, func(d *Doc) { d.Fields = map[string]string{} }),
		diffCase(t, "no change at all", base, func(d *Doc) {}),
	}

	// Conditions: one that holds, one that does not.
	out = append(out,
		build(t, "condition holds", base, deep.Patch[Doc]{Operations: []deep.Operation{{
			Kind: deep.OpReplace, Path: "/status", Old: "open", New: "closed",
			If: &condition.Condition{Path: "/score", Op: condition.Lt, Value: 5},
		}}}),
		build(t, "condition does not hold", base, deep.Patch[Doc]{Operations: []deep.Operation{{
			Kind: deep.OpReplace, Path: "/status", Old: "open", New: "closed",
			If: &condition.Condition{Path: "/score", Op: condition.Gt, Value: 5},
		}}}),
		build(t, "unless holds", base, deep.Patch[Doc]{Operations: []deep.Operation{{
			Kind: deep.OpReplace, Path: "/status", New: "closed",
			Unless: &condition.Condition{Path: "/meta/owner", Op: condition.Eq, Value: "ana"},
		}}}),
		build(t, "guard met", base, deep.Patch[Doc]{
			Guard:      &condition.Condition{Path: "/status", Op: condition.Eq, Value: "open"},
			Operations: []deep.Operation{{Kind: deep.OpReplace, Path: "/score", Old: 3, New: 7}},
		}),
		build(t, "guard not met", base, deep.Patch[Doc]{
			Guard:      &condition.Condition{Path: "/status", Op: condition.Eq, Value: "closed"},
			Operations: []deep.Operation{{Kind: deep.OpReplace, Path: "/score", Old: 3, New: 7}},
		}),
		build(t, "composed guard", base, deep.Patch[Doc]{
			Guard: deep.And(
				deep.Eq(path("/status"), any("open")),
				deep.Exists(path("/meta/owner")),
			),
			Operations: []deep.Operation{{Kind: deep.OpReplace, Path: "/title", New: "guarded"}},
		}),
		build(t, "in and type conditions", base, deep.Patch[Doc]{Operations: []deep.Operation{{
			Kind: deep.OpReplace, Path: "/title", New: "matched",
			If: deep.And(
				deep.In(path("/status"), []any{"open", "pending"}),
				deep.Type(path("/score"), "number"),
			),
		}}}),
		build(t, "matches condition", base, deep.Patch[Doc]{Operations: []deep.Operation{{
			Kind: deep.OpReplace, Path: "/title", New: "matched",
			If: deep.Matches(path("/meta/owner"), "^a"),
		}}}),
	)

	// Strict mode: the check that passes, and the one that finds stale state.
	out = append(out,
		build(t, "strict check passes", base, deep.Patch[Doc]{
			Strict:     true,
			Operations: []deep.Operation{{Kind: deep.OpReplace, Path: "/status", Old: "open", New: "closed"}},
		}),
		build(t, "strict check fails", base, deep.Patch[Doc]{
			Strict:     true,
			Operations: []deep.Operation{{Kind: deep.OpReplace, Path: "/status", Old: "stale", New: "closed"}},
		}),
	)

	// Hand-built structural operations.
	out = append(out,
		build(t, "add map key", base, deep.Patch[Doc]{Operations: []deep.Operation{
			{Kind: deep.OpAdd, Path: "/fields/extra", New: "value"},
		}}),
		build(t, "remove map key", base, deep.Patch[Doc]{Operations: []deep.Operation{
			{Kind: deep.OpRemove, Path: "/fields/region", Old: "north"},
		}}),
		build(t, "copy between map keys", base, deep.Patch[Doc]{Operations: []deep.Operation{
			{Kind: deep.OpCopy, Path: "/fields/copy", From: "/fields/region"},
		}}),
		build(t, "move between map keys", base, deep.Patch[Doc]{Operations: []deep.Operation{
			{Kind: deep.OpMove, Path: "/fields/moved", From: "/fields/region"},
		}}),
		build(t, "log operation", base, deep.Patch[Doc]{Operations: []deep.Operation{
			{Kind: deep.OpLog, Path: "/", New: "nothing changes"},
			{Kind: deep.OpReplace, Path: "/score", Old: 3, New: 4},
		}}),
		build(t, "operation on a missing path", base, deep.Patch[Doc]{Operations: []deep.Operation{
			{Kind: deep.OpReplace, Path: "/fields/absent/deeper", New: "x"},
		}}),
	)
	return out
}

// The corpus is committed, and this test regenerates it: a change to the
// patch semantics that alters what other implementations must match shows up
// as a diff in the working tree rather than as a silent divergence.
func TestCorpusIsCurrent(t *testing.T) {
	dir := filepath.Join("..", "..", "testdata", "conformance", "patch")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	generated := map[string]bool{}
	for _, c := range cases(t) {
		name := fileName(c.Name)
		generated[name] = true
		data, err := json.MarshalIndent(c, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		data = append(data, '\n')

		path := filepath.Join(dir, name)
		existing, err := os.ReadFile(path)
		if err == nil && string(existing) == string(data) {
			continue
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Errorf("conformance case %q regenerated — commit %s", c.Name, path)
	}

	// A case that disappears must take its file with it, or the JavaScript
	// side keeps checking against semantics this one no longer has.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !generated[e.Name()] {
			t.Errorf("stale conformance case %s — delete it", filepath.Join(dir, e.Name()))
		}
	}
}

func fileName(caseName string) string {
	out := make([]rune, 0, len(caseName)+5)
	for _, r := range caseName {
		if r == ' ' {
			out = append(out, '-')
			continue
		}
		out = append(out, r)
	}
	return string(out) + ".json"
}
