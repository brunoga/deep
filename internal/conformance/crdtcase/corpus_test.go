// Package crdtcase generates the CRDT half of the cross-language corpus.
//
// A patch either applies or it does not, so its conformance cases can be
// judged one at a time. A CRDT is different: two replicas that disagree
// produce no error, they simply hold different text forever. The only way to
// check another implementation is to make it replay real exchanges and end up
// where this one did — which is what these cases are.
package crdtcase

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/brunoga/deep/v6/crdt"
	"github.com/brunoga/deep/v6/crdt/hlc"
)

// Step is one edit, applied to the named replica.
type Step struct {
	Node  string `json:"node"`
	Op    string `json:"op"` // "insert" | "delete" | "sync"
	Pos   int    `json:"pos,omitempty"`
	Value string `json:"value,omitempty"`
	Count int    `json:"count,omitempty"`
	// To names the replica a "sync" step sends to; the update travels one way.
	To string `json:"to,omitempty"`
}

// Case is one scenario, with what this implementation made of it.
type Case struct {
	Name  string   `json:"name"`
	Nodes []string `json:"nodes"`
	Steps []Step   `json:"steps"`
	// Exchanges records every update a "sync" step carried, in order, as the
	// exact bytes that went over the wire. A reader replays them: decoding
	// them, applying them, and re-encoding them must all agree.
	Exchanges []string `json:"exchanges"`
	// Text is what each replica holds at the end, and Full is each replica's
	// whole state as one update — the form a joining peer receives.
	Text map[string]string `json:"text"`
	Full map[string]string `json:"full"`
	// StateVectors is each replica's state vector, encoded.
	StateVectors map[string]string `json:"state_vectors"`
	// Walls is the wall time each replica's clock was pinned to. A reader
	// that pins its own clocks the same way allocates the same identifiers,
	// so its edits — not merely its decoding — can be compared byte for byte
	// against this implementation's.
	Walls map[string]int64 `json:"walls"`
}

// scenario runs steps across a set of replicas and records everything a
// second implementation needs to check itself.
func scenario(t *testing.T, name string, nodes []string, steps []Step) Case {
	t.Helper()
	docs := map[string]*crdt.Document{}
	walls := map[string]int64{}
	for i, n := range nodes {
		// Wall times are fixed rather than read from the clock: a corpus whose
		// bytes change every run is not a corpus. Each replica gets its own
		// origin, which is what the real clock's per-start sequence provides.
		walls[n] = int64(1_000_000 + i)
		docs[n] = crdt.DocumentFromText(nil, hlc.NewClockAt(n, walls[n]))
	}

	c := Case{Name: name, Nodes: nodes, Steps: steps, Walls: walls,
		Text: map[string]string{}, Full: map[string]string{}, StateVectors: map[string]string{}}

	for i, step := range steps {
		doc, ok := docs[step.Node]
		if !ok {
			t.Fatalf("%s: step %d names unknown replica %q", name, i, step.Node)
		}
		switch step.Op {
		case "insert":
			doc.Insert(step.Pos, step.Value)
		case "delete":
			doc.Delete(step.Pos, step.Count)
		case "sync":
			to, ok := docs[step.To]
			if !ok {
				t.Fatalf("%s: step %d syncs to unknown replica %q", name, i, step.To)
			}
			update := doc.Since(to.StateVector())
			data, err := update.MarshalBinary()
			if err != nil {
				t.Fatal(err)
			}
			c.Exchanges = append(c.Exchanges, hex.EncodeToString(data))
			to.Apply(update)
		default:
			t.Fatalf("%s: step %d has unknown op %q", name, i, step.Op)
		}
	}

	for _, n := range nodes {
		doc := docs[n]
		c.Text[n] = doc.String()
		full, err := doc.Since(crdt.StateVector{}).MarshalBinary()
		if err != nil {
			t.Fatal(err)
		}
		c.Full[n] = hex.EncodeToString(full)
		sv, err := doc.StateVector().MarshalBinary()
		if err != nil {
			t.Fatal(err)
		}
		c.StateVectors[n] = hex.EncodeToString(sv)
	}
	return c
}

func cases(t *testing.T) []Case {
	t.Helper()
	ab := []string{"a", "b"}
	return []Case{
		scenario(t, "one replica types", []string{"a"}, []Step{
			{Node: "a", Op: "insert", Pos: 0, Value: "hello"},
			{Node: "a", Op: "insert", Pos: 5, Value: " world"},
		}),
		scenario(t, "one replica types and deletes", []string{"a"}, []Step{
			{Node: "a", Op: "insert", Pos: 0, Value: "hello world"},
			{Node: "a", Op: "delete", Pos: 5, Count: 6},
		}),
		scenario(t, "insert in the middle", []string{"a"}, []Step{
			{Node: "a", Op: "insert", Pos: 0, Value: "held"},
			{Node: "a", Op: "insert", Pos: 3, Value: "l"},
		}),
		scenario(t, "one way sync", ab, []Step{
			{Node: "a", Op: "insert", Pos: 0, Value: "from a"},
			{Node: "a", Op: "sync", To: "b"},
		}),
		scenario(t, "concurrent inserts at the same position", ab, []Step{
			{Node: "a", Op: "insert", Pos: 0, Value: "AAA"},
			{Node: "b", Op: "insert", Pos: 0, Value: "BBB"},
			{Node: "a", Op: "sync", To: "b"},
			{Node: "b", Op: "sync", To: "a"},
		}),
		scenario(t, "concurrent inserts at different positions", ab, []Step{
			{Node: "a", Op: "insert", Pos: 0, Value: "shared"},
			{Node: "a", Op: "sync", To: "b"},
			{Node: "a", Op: "insert", Pos: 0, Value: "front "},
			{Node: "b", Op: "insert", Pos: 6, Value: " back"},
			{Node: "a", Op: "sync", To: "b"},
			{Node: "b", Op: "sync", To: "a"},
		}),
		scenario(t, "concurrent edit and delete", ab, []Step{
			{Node: "a", Op: "insert", Pos: 0, Value: "hello world"},
			{Node: "a", Op: "sync", To: "b"},
			{Node: "a", Op: "delete", Pos: 0, Count: 6},
			{Node: "b", Op: "insert", Pos: 11, Value: "!"},
			{Node: "a", Op: "sync", To: "b"},
			{Node: "b", Op: "sync", To: "a"},
		}),
		scenario(t, "deletion inside a run", ab, []Step{
			{Node: "a", Op: "insert", Pos: 0, Value: "abcdef"},
			{Node: "a", Op: "delete", Pos: 2, Count: 2},
			{Node: "a", Op: "sync", To: "b"},
		}),
		scenario(t, "interleaved typing", ab, []Step{
			{Node: "a", Op: "insert", Pos: 0, Value: "a1"},
			{Node: "a", Op: "sync", To: "b"},
			{Node: "b", Op: "insert", Pos: 2, Value: "b1"},
			{Node: "b", Op: "sync", To: "a"},
			{Node: "a", Op: "insert", Pos: 4, Value: "a2"},
			{Node: "a", Op: "sync", To: "b"},
			{Node: "b", Op: "insert", Pos: 6, Value: "b2"},
			{Node: "b", Op: "sync", To: "a"},
		}),
		scenario(t, "text outside the basic plane", []string{"a"}, []Step{
			// Positions are counted in runes, so an implementation counting
			// UTF-16 units puts the second insert in the wrong place.
			{Node: "a", Op: "insert", Pos: 0, Value: "aXb"},
			{Node: "a", Op: "insert", Pos: 2, Value: "Y"},
			{Node: "a", Op: "delete", Pos: 0, Count: 1},
		}),
		scenario(t, "three replicas converge", []string{"a", "b", "c"}, []Step{
			{Node: "a", Op: "insert", Pos: 0, Value: "one"},
			{Node: "b", Op: "insert", Pos: 0, Value: "two"},
			{Node: "c", Op: "insert", Pos: 0, Value: "three"},
			{Node: "a", Op: "sync", To: "b"},
			{Node: "b", Op: "sync", To: "c"},
			{Node: "c", Op: "sync", To: "a"},
			{Node: "a", Op: "sync", To: "b"},
			{Node: "b", Op: "sync", To: "c"},
			{Node: "c", Op: "sync", To: "a"},
		}),
	}
}

func TestCorpusIsCurrent(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "testdata", "conformance", "crdt")
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
		if existing, err := os.ReadFile(path); err == nil && string(existing) == string(data) {
			continue
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		t.Errorf("conformance case %q regenerated — commit %s", c.Name, path)
	}
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
			r = '-'
		}
		out = append(out, r)
	}
	return string(out) + ".json"
}
