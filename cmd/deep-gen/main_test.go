package main

import (
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestGeneratorOutput runs deep-gen on the internal/testmodels package and
// compares the output against the checked-in golden file.
func TestGeneratorOutput(t *testing.T) {
	// Build the generator binary.
	tmpDir := t.TempDir()
	genBin := filepath.Join(tmpDir, "deep-gen")
	buildCmd := exec.Command("go", "build", "-o", genBin, ".")
	buildCmd.Dir = "."
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("build deep-gen: %v\n%s", err, out)
	}

	// Run generator on testmodels.
	outFile := filepath.Join(tmpDir, "user_deep.go")
	runCmd := exec.Command(genBin, "-type=User,Detail", "-output", outFile, "../../internal/testmodels")
	if out, err := runCmd.CombinedOutput(); err != nil {
		t.Fatalf("run deep-gen: %v\n%s", err, out)
	}

	got, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}

	golden, err := os.ReadFile("../../internal/testmodels/user_deep.go")
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}

	gotStr := strings.TrimSpace(string(got))
	goldenStr := strings.TrimSpace(string(golden))
	if gotStr != goldenStr {
		t.Errorf("generator output does not match golden file\nwant:\n%s\n\ngot:\n%s", goldenStr, gotStr)
	}
}

// TestGeneratedCodeHasNoInternalImports asserts that code emitted by deep-gen
// never imports an internal/... package of this module. Such imports compile
// inside the module but break for any downstream user — see the bug fixed by
// exposing deep.ApplyOpReflection as a public wrapper around the engine's
// reflection fallback.
func TestGeneratedCodeHasNoInternalImports(t *testing.T) {
	tmpDir := t.TempDir()
	genBin := filepath.Join(tmpDir, "deep-gen")
	if out, err := exec.Command("go", "build", "-o", genBin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build deep-gen: %v\n%s", err, out)
	}

	// internal/testmodels is rich enough to exercise the reflection fallback
	// (which is what previously dragged the internal/engine import in).
	outFile := filepath.Join(tmpDir, "user_deep.go")
	if out, err := exec.Command(genBin, "-type=User,Detail", "-output", outFile, "../../internal/testmodels").CombinedOutput(); err != nil {
		t.Fatalf("run deep-gen: %v\n%s", err, out)
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, outFile, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse generated file: %v", err)
	}

	const modulePrefix = "github.com/brunoga/deep/v5/"
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
}
