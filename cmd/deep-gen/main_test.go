package main

import (
	"os"
	"os/exec"
	"path/filepath"
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
