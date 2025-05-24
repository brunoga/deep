package deep

import (
	"reflect"
	"testing"

	"github.com/brunoga/deep/patch"
)

// Test structures
type TestPerson struct {
	Name     string
	Age      int
	Address  *TestAddress
	Skills   []string
	Contacts map[string]string
}

type TestAddress struct {
	Street string
	City   string
	Zip    string
}

func TestPatch(t *testing.T) {
	// Test basic Patch functionality with different operations
	t.Run("Basic operations", func(t *testing.T) {
		// Source object to patch
		src := TestPerson{
			Name:   "John Doe",
			Age:    30,
			Skills: []string{"go", "python"},
			Contacts: map[string]string{
				"email": "john@example.com",
				"phone": "123-456-7890",
			},
		}

		// Create a patch with multiple operations
		p := patch.New[TestPerson]()
		p = p.Replace("/Name", "Jane Doe")
		p = p.Add("/Skills/-", "javascript")
		p = p.Remove("/Contacts/phone")
		p = p.Add("/Address", &TestAddress{Street: "123 Main St", City: "Anytown", Zip: "12345"})

		// Apply the patch
		result, err := Patch(src, p)
		if err != nil {
			t.Fatalf("Patch() error: %v", err)
		}

		// Verify results
		if result.Name != "Jane Doe" {
			t.Errorf("Name = %q, want %q", result.Name, "Jane Doe")
		}
		if len(result.Skills) != 3 || result.Skills[2] != "javascript" {
			t.Errorf("Skills = %v, want %v", result.Skills, []string{"go", "python", "javascript"})
		}
		if _, ok := result.Contacts["phone"]; ok {
			t.Errorf("Contacts should not contain 'phone'")
		}
		if result.Address == nil || result.Address.Street != "123 Main St" {
			t.Errorf("Address not properly added")
		}

		// Original object should be unchanged
		if src.Name != "John Doe" || len(src.Skills) != 2 || src.Address != nil {
			t.Errorf("Original object was modified")
		}
	})

	// Test that errors are properly propagated
	t.Run("Error handling", func(t *testing.T) {
		// Test invalid path - should panic during patch creation
		func() {
			defer func() {
				if r := recover(); r == nil {
					t.Errorf("Expected panic for invalid path, but didn't get one")
				}
			}()
			// This should panic
			p := patch.New[TestPerson]().Replace("/InvalidField", "value")
			_ = p // Prevent unused variable warning
		}()

		// Test type mismatch - should panic during patch creation
		func() {
			defer func() {
				if r := recover(); r == nil {
					t.Errorf("Expected panic for type mismatch, but didn't get one")
				}
			}()
			// This should panic
			p := patch.New[TestPerson]().Replace("/Age", "not-an-int")
			_ = p // Prevent unused variable warning
		}()
	})

	// Test complex operations like move and copy
	t.Run("Move and copy", func(t *testing.T) {
		src := TestPerson{
			Name: "John",
			Age:  30,
			Contacts: map[string]string{
				"email": "john@example.com",
			},
		}

		p := patch.New[TestPerson]()
		p = p.Copy("/Name", "/Contacts/copied") // Do this FIRST
		p = p.Move("/Name", "/Contacts/name")   // Then do this

		result, err := Patch(src, p)
		if err != nil {
			t.Fatalf("Patch() error: %v", err)
		}

		if result.Name != "" {
			t.Errorf("Name should be empty after move, got %q", result.Name)
		}
		if result.Contacts["name"] != "John" {
			t.Errorf("Contacts[name] = %q, want %q", result.Contacts["name"], "John")
		}
		if result.Contacts["copied"] != "John" {
			t.Errorf("Contacts[copied] = %q, want %q", result.Contacts["copied"], "John")
		}
	})

	// Test the test operation
	t.Run("Test operation", func(t *testing.T) {
		src := TestPerson{
			Name: "John",
			Age:  30,
		}

		// Test operation that succeeds
		p := patch.New[TestPerson]().Test("/Name", "John")
		_, err := Patch(src, p)
		if err != nil {
			t.Errorf("Test operation should succeed: %v", err)
		}

		// Test operation that fails
		p = patch.New[TestPerson]().Test("/Name", "Jane")
		_, err = Patch(src, p)
		if err == nil {
			t.Errorf("Test operation should fail")
		}
	})

	// Additional test for errors during patch application
	t.Run("Apply errors", func(t *testing.T) {
		src := TestPerson{
			Name:    "John",
			Address: nil, // Address is nil
		}

		// Try to add to a field in a nil pointer - should return an error during Apply
		p := patch.New[TestPerson]().Add("/Address/Street", "Main St")
		_, err := Patch(src, p)
		if err == nil {
			t.Errorf("Expected error when adding to field of nil pointer, got nil")
		}
	})
}

func TestMustPatch(t *testing.T) {
	// Test that MustPatch works correctly
	t.Run("Success case", func(t *testing.T) {
		src := TestPerson{Name: "John", Age: 30}

		// This should not panic
		result := MustPatch(src, patch.New[TestPerson]().Replace("/Name", "Jane"))

		if result.Name != "Jane" {
			t.Errorf("MustPatch result incorrect: got %q, want %q", result.Name, "Jane")
		}
	})

	// Test that MustPatch panics on error
	t.Run("Panic case", func(t *testing.T) {
		src := TestPerson{Name: "John", Age: 30}

		// This should panic
		defer func() {
			if r := recover(); r == nil {
				t.Errorf("MustPatch should have panicked but didn't")
			}
		}()

		// Invalid path that should cause a panic
		_ = MustPatch(src, patch.New[TestPerson]().Replace("/InvalidField", "value"))
	})
}

func TestPatchRoundTrip(t *testing.T) {
	// Test a complete round-trip: create source → modify → diff → apply patch to source
	src := TestPerson{
		Name:   "John Doe",
		Age:    30,
		Skills: []string{"go", "python"},
		Address: &TestAddress{
			Street: "123 Main St",
			City:   "Anytown",
		},
	}

	// Create modified version
	modified := TestPerson{
		Name:   "John Smith",
		Age:    31,
		Skills: []string{"go", "rust", "python"},
		Address: &TestAddress{
			Street: "123 Main St",
			City:   "Othertown", // Changed
		},
	}

	// Create a diff
	p, err := Diff(src, modified)
	if err != nil {
		t.Fatalf("Diff() error: %v", err)
	}

	// Apply the patch
	result, err := Patch(src, p)
	if err != nil {
		t.Fatalf("Patch() error: %v", err)
	}

	// Verify the result matches the modified version
	if !reflect.DeepEqual(result, modified) {
		t.Errorf("Round-trip patching failed:\nGot:  %+v\nWant: %+v", result, modified)
	}
}

// Define a concrete type for complex testing
type ComplexStruct struct {
	Meta     map[string]any
	Items    []map[string]any
	Pointers []*TestPerson
}

func TestComplexPatching(t *testing.T) {
	// Test patching with complex nested structures
	src := ComplexStruct{
		Meta: map[string]any{
			"version": 1,
			"config": map[string]any{
				"enabled": true,
				"limits":  []int{10, 20, 30},
			},
		},
		Items: []map[string]any{
			{"id": 1, "name": "first"},
			{"id": 2, "name": "second"},
		},
		Pointers: []*TestPerson{
			{Name: "Alice", Age: 25},
			{Name: "Bob", Age: 30},
		},
	}

	// Create a patch with operations on deeply nested structures
	p := patch.New[ComplexStruct]()
	p = p.Replace("/Meta/version", 2)

	// Replace the entire config map instead of just the limits array
	p = p.Replace("/Meta/config", map[string]any{
		"enabled": true,
		"limits":  []int{10, 20, 30, 40},
	})

	p = p.Replace("/Items/1/name", "updated")
	p = p.Add("/Items/-", map[string]any{"id": 3, "name": "third"})
	p = p.Replace("/Pointers/0/Age", 26)

	// Apply the patch
	result, err := Patch(src, p)
	if err != nil {
		t.Fatalf("Patch() error: %v", err)
	}

	// Verify results
	resultMeta := result.Meta
	if resultMeta["version"] != 2 {
		t.Errorf("Meta.version = %v, want 2", resultMeta["version"])
	}

	configMap := resultMeta["config"].(map[string]any)
	limits := configMap["limits"].([]int)
	if len(limits) != 4 || limits[3] != 40 {
		t.Errorf("Meta.config.limits = %v, want [10, 20, 30, 40]", limits)
	}

	items := result.Items
	if len(items) != 3 {
		t.Errorf("Items length = %d, want 3", len(items))
	}
	if items[1]["name"] != "updated" {
		t.Errorf("Items[1].name = %v, want 'updated'", items[1]["name"])
	}
	if items[2]["name"] != "third" {
		t.Errorf("Items[2].name = %v, want 'third'", items[2]["name"])
	}

	if result.Pointers[0].Age != 26 {
		t.Errorf("Pointers[0].Age = %d, want 26", result.Pointers[0].Age)
	}
}
