package deep_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestExamplesAreDocumented asserts that every example directory is listed in
// both the root README and examples/README.md, and that neither lists an
// example that no longer exists. Documentation in this repository has drifted
// from reality before; a new example is only finished once it is findable.
func TestExamplesAreDocumented(t *testing.T) {
	entries, err := os.ReadDir("examples")
	if err != nil {
		t.Fatalf("read examples dir: %v", err)
	}

	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		t.Fatal("no example directories found")
	}

	for _, doc := range []struct {
		path string
		link func(string) string
	}{
		{"README.md", func(n string) string { return "(examples/" + n + ")" }},
		{filepath.Join("examples", "README.md"), func(n string) string { return "(" + n + ")" }},
	} {
		data, err := os.ReadFile(doc.path)
		if err != nil {
			t.Fatalf("read %s: %v", doc.path, err)
		}
		text := string(data)

		for _, name := range names {
			if !strings.Contains(text, doc.link(name)) {
				t.Errorf("%s does not link to example %q", doc.path, name)
			}
		}

		// Catch links left behind by a renamed or deleted example.
		for _, line := range strings.Split(text, "\n") {
			for _, prefix := range []string{"](examples/", "]("} {
				rest := line
				for {
					i := strings.Index(rest, prefix)
					if i < 0 {
						break
					}
					rest = rest[i+len(prefix):]
					end := strings.IndexByte(rest, ')')
					if end < 0 {
						break
					}
					target := rest[:end]
					rest = rest[end:]
					// Only consider bare example-style targets.
					if target == "" || strings.ContainsAny(target, "/.:#") {
						continue
					}
					if doc.path == "README.md" && !strings.HasPrefix(prefix, "](examples/") {
						continue
					}
					if !contains(names, target) {
						t.Errorf("%s links to %q, which is not an example directory", doc.path, target)
					}
				}
			}
		}
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
