package deep

import (
	"reflect"
	"testing"
)

// Test structures
type Person struct {
	Name     string
	Age      int
	Address  *Address
	Tags     []string
	Metadata map[string]interface{}
}

type Address struct {
	Street string
	City   string
	Zip    string
}

func TestDiff(t *testing.T) {
	// Test person struct changes
	t.Run("Person tests", func(t *testing.T) {
		tests := []struct {
			name      string
			src       Person
			dst       Person
			wantOps   int
			wantPaths []string
		}{
			{
				name:    "No changes",
				src:     Person{Name: "John", Age: 30},
				dst:     Person{Name: "John", Age: 30},
				wantOps: 0,
			},
			{
				name:      "Simple field change",
				src:       Person{Name: "John", Age: 30},
				dst:       Person{Name: "Jane", Age: 30},
				wantOps:   1,
				wantPaths: []string{"/Name"},
			},
			{
				name:      "Multiple field changes",
				src:       Person{Name: "John", Age: 30},
				dst:       Person{Name: "Jane", Age: 32},
				wantOps:   2,
				wantPaths: []string{"/Name", "/Age"},
			},
			{
				name:      "Nil to non-nil pointer",
				src:       Person{Name: "John", Address: nil},
				dst:       Person{Name: "John", Address: &Address{Street: "Main St"}},
				wantOps:   1,
				wantPaths: []string{"/Address"},
			},
			{
				name:      "Nested struct change",
				src:       Person{Address: &Address{Street: "Main St", City: "Anytown"}},
				dst:       Person{Address: &Address{Street: "Main St", City: "Othertown"}},
				wantOps:   1,
				wantPaths: []string{"/Address/City"},
			},
			{
				name:      "Slice add",
				src:       Person{Tags: []string{"a", "b"}},
				dst:       Person{Tags: []string{"a", "b", "c"}},
				wantOps:   1,
				wantPaths: []string{"/Tags/-"},
			},
			{
				name:      "Slice remove",
				src:       Person{Tags: []string{"a", "b", "c"}},
				dst:       Person{Tags: []string{"a", "b"}},
				wantOps:   1,
				wantPaths: []string{"/Tags/2"},
			},
			{
				name:      "Map changes",
				src:       Person{Metadata: map[string]interface{}{"key1": "val1", "key2": 2}},
				dst:       Person{Metadata: map[string]interface{}{"key1": "val1", "key3": "val3"}},
				wantOps:   2,
				wantPaths: []string{"/Metadata/key2", "/Metadata/key3"},
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				// Create a proper typed diff
				p, err := Diff(tt.src, tt.dst)
				if err != nil {
					t.Fatalf("Diff() error = %v", err)
				}

				// Check number of operations
				if len(p) != tt.wantOps {
					t.Errorf("Got %d operations, want %d", len(p), tt.wantOps)
				}

				// Check paths if specified
				if tt.wantPaths != nil {
					gotPaths := make([]string, len(p))
					for i, op := range p {
						gotPaths[i] = op.Path
					}

					// Check if all expected paths are present
					for _, wantPath := range tt.wantPaths {
						found := false
						for _, gotPath := range gotPaths {
							if gotPath == wantPath {
								found = true
								break
							}
						}
						if !found {
							t.Errorf("Expected path %q not found in %v", wantPath, gotPaths)
						}
					}
				}

				// Apply the patch and check result
				if tt.wantOps > 0 {
					patchedVal, err := Patch(tt.src, p)
					if err != nil {
						t.Errorf("Failed to apply patch: %v", err)
					} else if !reflect.DeepEqual(patchedVal, tt.dst) {
						t.Errorf("Patched value does not match destination:\nGot:  %+v\nWant: %+v", patchedVal, tt.dst)
					}
				}
			})
		}
	})

	// Test primitive type changes
	t.Run("Primitive types", func(t *testing.T) {
		// String replacement
		strSrc := "hello"
		strDst := "world"
		p, err := Diff(strSrc, strDst)
		if err != nil {
			t.Fatalf("Diff() error = %v", err)
		}
		if len(p) != 1 {
			t.Errorf("Expected 1 operation, got %d", len(p))
		}
		if p[0].Path != "" {
			t.Errorf("Expected root path, got %s", p[0].Path)
		}
		patchedStr, err := Patch(strSrc, p)
		if err != nil {
			t.Errorf("Failed to apply patch: %v", err)
		} else if patchedStr != strDst {
			t.Errorf("Patched value does not match destination:\nGot:  %v\nWant: %v", patchedStr, strDst)
		}

		// Integer replacement
		intSrc := 42
		intDst := 99
		p1, err := Diff(intSrc, intDst)
		if err != nil {
			t.Fatalf("Diff() error = %v", err)
		}
		if len(p1) != 1 {
			t.Errorf("Expected 1 operation, got %d", len(p1))
		}
		patchedInt, err := Patch(intSrc, p1)
		if err != nil {
			t.Errorf("Failed to apply patch: %v", err)
		} else if patchedInt != intDst {
			t.Errorf("Patched value does not match destination:\nGot:  %v\nWant: %v", patchedInt, intDst)
		}
	})

	// Test slice changes
	t.Run("Slice tests", func(t *testing.T) {
		src := []string{"a", "b", "c"}
		dst := []string{"a", "modified", "c", "new"}

		p, err := Diff(src, dst)
		if err != nil {
			t.Fatalf("Diff() error = %v", err)
		}

		// Should have 2 operations: replace "b" with "modified" and add "new"
		if len(p) != 2 {
			t.Errorf("Expected 2 operations, got %d", len(p))
		}

		patchedSlice, err := Patch(src, p)
		if err != nil {
			t.Errorf("Failed to apply patch: %v", err)
		} else if !reflect.DeepEqual(patchedSlice, dst) {
			t.Errorf("Patched value does not match destination:\nGot:  %v\nWant: %v", patchedSlice, dst)
		}
	})

	// Test map changes
	t.Run("Map tests", func(t *testing.T) {
		src := map[string]int{"a": 1, "b": 2, "c": 3}
		dst := map[string]int{"a": 1, "b": 20, "d": 4}

		p, err := Diff(src, dst)
		if err != nil {
			t.Fatalf("Diff() error = %v", err)
		}

		// Should have 3 operations: replace "b", remove "c", add "d"
		if len(p) != 3 {
			t.Errorf("Expected 3 operations, got %d", len(p))
		}

		patchedMap, err := Patch(src, p)
		if err != nil {
			t.Errorf("Failed to apply patch: %v", err)
		} else if !reflect.DeepEqual(patchedMap, dst) {
			t.Errorf("Patched value does not match destination:\nGot:  %v\nWant: %v", patchedMap, dst)
		}
	})
}
