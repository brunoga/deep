package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// models are the packages deep-gen is run against by the tests below, with the
// checked-in file each run must reproduce.
var models = []struct {
	name   string
	dir    string
	types  string
	golden string
}{
	{"testmodels", "../../internal/testmodels", "User,Detail", "../../internal/testmodels/user_deep.go"},
	// Fields whose types come from other packages: *time.Time and friends.
	{"external", "../../internal/testmodels/external", "Job,Stage", "../../internal/testmodels/external/job_deep.go"},
	// Structural shapes: embedded fields, keyed slices, nested and
	// pointer-valued maps.
	{"shapes", "../../internal/testmodels/shapes", "Doc,Meta,Entry,Payload", "../../internal/testmodels/shapes/doc_deep.go"},
}

// buildGenerator builds deep-gen and returns the path to the binary.
func buildGenerator(t *testing.T) string {
	t.Helper()
	genBin := filepath.Join(t.TempDir(), "deep-gen")
	if out, err := exec.Command("go", "build", "-o", genBin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build deep-gen: %v\n%s", err, out)
	}
	return genBin
}

// generate runs deep-gen and returns the path of the file it wrote.
func generate(t *testing.T, genBin, dir, types string) string {
	t.Helper()
	outFile := filepath.Join(t.TempDir(), "out_deep.go")
	if out, err := exec.Command(genBin, "-type="+types, "-output", outFile, dir).CombinedOutput(); err != nil {
		t.Fatalf("run deep-gen on %s: %v\n%s", dir, err, out)
	}
	return outFile
}

// TestGeneratorOutput runs deep-gen on the test model packages and compares the
// output against the checked-in golden files.
func TestGeneratorOutput(t *testing.T) {
	genBin := buildGenerator(t)

	for _, m := range models {
		t.Run(m.name, func(t *testing.T) {
			got, err := os.ReadFile(generate(t, genBin, m.dir, m.types))
			if err != nil {
				t.Fatalf("read output: %v", err)
			}
			golden, err := os.ReadFile(m.golden)
			if err != nil {
				t.Fatalf("read golden: %v", err)
			}

			gotStr := strings.TrimSpace(string(got))
			goldenStr := strings.TrimSpace(string(golden))
			if gotStr != goldenStr {
				t.Errorf("generator output does not match %s\nwant:\n%s\n\ngot:\n%s", m.golden, goldenStr, gotStr)
			}
		})
	}
}

// TestGeneratedCodeHasNoInternalImports asserts that code emitted by deep-gen
// never imports an internal/... package of this module. Such imports compile
// inside the module but break for any downstream user — see the bug fixed by
// exposing deep.ApplyOpReflection as a public wrapper around the engine's
// reflection fallback.
func TestGeneratedCodeHasNoInternalImports(t *testing.T) {
	genBin := buildGenerator(t)

	for _, m := range models {
		t.Run(m.name, func(t *testing.T) {
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, generate(t, genBin, m.dir, m.types), nil, parser.ImportsOnly)
			if err != nil {
				t.Fatalf("parse generated file: %v", err)
			}

			const modulePrefix = "github.com/brunoga/deep/v6/"
			for _, imp := range file.Imports {
				path, err := strconv.Unquote(imp.Path.Value)
				if err != nil {
					t.Fatalf("unquote import %q: %v", imp.Path.Value, err)
				}
				if !strings.HasPrefix(path, modulePrefix) {
					continue
				}
				rel := strings.TrimPrefix(path, modulePrefix)
				if rel == "internal" || strings.HasPrefix(rel, "internal/") || strings.Contains(rel, "/internal/") {
					t.Errorf("generated code imports internal package %q; expose a public wrapper instead", path)
				}
			}
		})
	}
}

// TestResolveType pins how field types are classified: what the generator can
// name, what it knows has generated methods, and what it must leave to the
// reflection fallback.
func TestResolveType(t *testing.T) {
	const src = `package p

import (
	clock "time"
	"time"

	"github.com/brunoga/deep/v6/crdt"
)

type Detail struct{ A int }

type Priority int

type T struct {
	Ptr     *time.Time
	Val     time.Time
	Aliased clock.Time
	Times   []time.Time
	PtrTime *[]*time.Time
	Details []Detail
	DetailP *Detail
	Detail  Detail
	Owners  map[string]*Detail
	Prio    Priority
	Prios   []Priority
	Names   []string
	Notes   crdt.Text
	Title   crdt.LWW[string]
	Grid    [2]int
	Ch      chan int
	Fn      func() error
	Iface   any
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "p.go", src, 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	idx := pkgIndex{
		structs: map[string]bool{"Detail": true, "T": true},
		scalars: map[string]bool{"Priority": true},
	}

	want := map[string]typeInfo{
		"Ptr":     {name: "*time.Time", pointeeKnown: true, quals: []string{"time"}},
		"Val":     {name: "time.Time", knownValue: true, quals: []string{"time"}},
		"Aliased": {name: "clock.Time", knownValue: true, quals: []string{"clock"}},
		"Times":   {name: "[]time.Time", isCollection: true, elemKnownValue: true, quals: []string{"time"}},
		"PtrTime": {name: "*[]*time.Time", quals: []string{"time"}},
		"Details": {name: "[]Detail", isCollection: true, elemGenerated: true},
		"DetailP": {name: "*Detail", isStruct: true},
		"Detail":  {name: "Detail", isStruct: true},
		"Owners":  {name: "map[string]*Detail", isCollection: true, elemGenerated: true},
		"Prio":    {name: "Priority", comparable: true},
		"Prios":   {name: "[]Priority", isCollection: true, elemComparable: true},
		"Names":   {name: "[]string", isCollection: true, elemComparable: true},
		"Notes":   {name: "crdt.Text", isText: true},
		"Title":   {name: "crdt.LWW[string]", quals: []string{"crdt"}},
		"Grid":    {name: "[2]int"},
		"Ch":      {}, // channels are left to the reflection fallback
		"Fn":      {},
		"Iface":   {name: "any"},
	}

	imports := fileImports(file)
	var target *ast.StructType
	ast.Inspect(file, func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSpec)
		if !ok || ts.Name.Name != "T" {
			return true
		}
		target, _ = ts.Type.(*ast.StructType)
		return false
	})
	if target == nil {
		t.Fatal("struct T not found")
	}

	seen := 0
	for _, field := range target.Fields.List {
		name := field.Names[0].Name
		expected, ok := want[name]
		if !ok {
			t.Errorf("field %s: no expectation declared", name)
			continue
		}
		seen++
		if got := resolveType(field.Type, imports, idx); !reflect.DeepEqual(got, expected) {
			t.Errorf("resolveType(%s) = %+v, want %+v", name, got, expected)
		}
	}
	if seen != len(want) {
		t.Errorf("checked %d fields, expected %d", seen, len(want))
	}
}
