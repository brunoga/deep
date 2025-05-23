package patch

import (
	"reflect"
	"testing"
)

// Test structures
type SimpleStruct struct {
	Int    int
	String string
	Bool   bool
}

type NestedStruct struct {
	Simple     SimpleStruct
	Pointer    *SimpleStruct
	unexported string
}

type ComplexStruct struct {
	Int       int
	String    string
	Bool      bool
	IntSlice  []int
	StrMap    map[string]string
	IntMap    map[int]string
	FloatMap  map[float64]string
	BoolMap   map[bool]string
	Nested    NestedStruct
	NestedPtr *NestedStruct
	Interface interface{}
	PtrSlice  []*SimpleStruct
	Array     [3]int
}

// Test validatePath
func TestValidatePath(t *testing.T) {
	tests := []struct {
		name      string
		path      string
		valueType reflect.Type
		wantErr   bool
	}{
		{"Root path", "", nil, false},
		{"Simple field", "/Int", nil, false},
		{"Nested field", "/Nested/Simple/Int", nil, false},
		{"Invalid field", "/NonExistent", nil, true},
		{"Invalid nested", "/Nested/NonExistent", nil, true},
		{"Map access", "/StrMap/key", nil, false},
		{"Slice access", "/IntSlice/0", nil, false},
		{"Append notation", "/IntSlice/-", nil, false},
		{"Invalid append", "/Int/-", nil, true},
		{"Type mismatch", "/Int", reflect.TypeOf("string"), true},
		{"Type match", "/Int", reflect.TypeOf(42), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := validatePath[ComplexStruct](tt.path, tt.valueType)
			if (err != nil) != tt.wantErr {
				t.Errorf("validatePath() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// Test Add operation
func TestPatch_Add(t *testing.T) {
	// Test adding to different parts of a structure
	p := New[ComplexStruct]()

	// Should not panic
	p = p.Add("/Int", 42)
	p = p.Add("/String", "hello")
	p = p.Add("/Bool", true)
	p = p.Add("/IntSlice/0", 1)
	p = p.Add("/IntSlice/-", 2)
	p = p.Add("/StrMap/key", "value")
	p = p.Add("/Nested/Simple/Int", 42)

	// Test that we have the right number of operations
	if len(p) != 7 {
		t.Fatalf("expected 7 operations, got %d", len(p))
	}

	// Verify the operations
	for _, op := range p {
		if op.Op != OperationTypeAdd {
			t.Errorf("expected Add operation, got %s", op.Op)
		}
	}

	// Test panics for invalid paths
	testPanics(t, "invalid path should panic", func() {
		p.Add("/NonExistent", 42)
	})

	// Test type mismatch
	testPanics(t, "type mismatch should panic", func() {
		p.Add("/Int", "not an int")
	})
}

// Test Remove operation
func TestPatch_Remove(t *testing.T) {
	p := New[ComplexStruct]()

	// Should not panic
	p = p.Remove("/Int")
	p = p.Remove("/IntSlice/0")
	p = p.Remove("/StrMap/key")
	p = p.Remove("/Nested/Simple/Int")

	// Test that we have the right number of operations
	if len(p) != 4 {
		t.Fatalf("expected 4 operations, got %d", len(p))
	}

	// Verify the operations
	for _, op := range p {
		if op.Op != OperationTypeRemove {
			t.Errorf("expected Remove operation, got %s", op.Op)
		}
		if op.Value != nil {
			t.Errorf("expected nil Value for Remove, got %v", op.Value)
		}
	}

	// Test panics for invalid paths
	testPanics(t, "invalid path should panic", func() {
		p.Remove("/NonExistent")
	})
}

// Test Replace operation
func TestPatch_Replace(t *testing.T) {
	p := New[ComplexStruct]()

	// Should not panic
	p = p.Replace("/Int", 42)
	p = p.Replace("/String", "hello")
	p = p.Replace("/IntSlice/0", 1)
	p = p.Replace("/StrMap/key", "value")

	// Test that we have the right number of operations
	if len(p) != 4 {
		t.Fatalf("expected 4 operations, got %d", len(p))
	}

	// Verify the operations
	for _, op := range p {
		if op.Op != OperationTypeReplace {
			t.Errorf("expected Replace operation, got %s", op.Op)
		}
	}

	// Test panics for invalid paths
	testPanics(t, "invalid path should panic", func() {
		p.Replace("/NonExistent", 42)
	})

	// Test type mismatch
	testPanics(t, "type mismatch should panic", func() {
		p.Replace("/Int", "not an int")
	})
}

// Test Move operation
func TestPatch_Move(t *testing.T) {
	p := New[ComplexStruct]()

	// Should not panic
	p = p.Move("/Int", "/Nested/Simple/Int")
	p = p.Move("/IntSlice/0", "/IntSlice/1")
	p = p.Move("/StrMap/key", "/StrMap/newKey")

	// Test that we have the right number of operations
	if len(p) != 3 {
		t.Fatalf("expected 3 operations, got %d", len(p))
	}

	// Verify the operations
	for _, op := range p {
		if op.Op != OperationTypeMove {
			t.Errorf("expected Move operation, got %s", op.Op)
		}
	}

	// Test panics for invalid source path
	testPanics(t, "invalid source path should panic", func() {
		p.Move("/NonExistent", "/Int")
	})

	// Test panics for invalid destination path
	testPanics(t, "invalid destination path should panic", func() {
		p.Move("/Int", "/NonExistent")
	})

	// Test panics for type mismatch
	testPanics(t, "type mismatch should panic", func() {
		p.Move("/String", "/Int")
	})
}

// Test Copy operation
func TestPatch_Copy(t *testing.T) {
	p := New[ComplexStruct]()

	// Should not panic
	p = p.Copy("/Int", "/Nested/Simple/Int")
	p = p.Copy("/IntSlice/0", "/IntSlice/1")
	p = p.Copy("/StrMap/key", "/StrMap/newKey")

	// Test that we have the right number of operations
	if len(p) != 3 {
		t.Fatalf("expected 3 operations, got %d", len(p))
	}

	// Verify the operations
	for _, op := range p {
		if op.Op != OperationTypeCopy {
			t.Errorf("expected Copy operation, got %s", op.Op)
		}
	}

	// Test panics for invalid source path
	testPanics(t, "invalid source path should panic", func() {
		p.Copy("/NonExistent", "/Int")
	})

	// Test panics for invalid destination path
	testPanics(t, "invalid destination path should panic", func() {
		p.Copy("/Int", "/NonExistent")
	})

	// Test panics for type mismatch
	testPanics(t, "type mismatch should panic", func() {
		p.Copy("/String", "/Int")
	})
}

// Test Test operation
func TestPatch_Test(t *testing.T) {
	p := New[ComplexStruct]()

	// Should not panic
	p = p.Test("/Int", 42)
	p = p.Test("/String", "hello")
	p = p.Test("/Bool", true)

	// Test that we have the right number of operations
	if len(p) != 3 {
		t.Fatalf("expected 3 operations, got %d", len(p))
	}

	// Verify the operations
	for _, op := range p {
		if op.Op != OperationTypeTest {
			t.Errorf("expected Test operation, got %s", op.Op)
		}
	}

	// Test panics for invalid paths
	testPanics(t, "invalid path should panic", func() {
		p.Test("/NonExistent", 42)
	})

	// Test type mismatch
	testPanics(t, "type mismatch should panic", func() {
		p.Test("/Int", "not an int")
	})
}

// Test the actual application of patches
func TestPatch_Apply(t *testing.T) {
	// Test adding and replacing values
	t.Run("Add and Replace", func(t *testing.T) {
		target := &ComplexStruct{
			Int:      0,
			String:   "",
			IntSlice: []int{1, 2, 3},
			StrMap:   map[string]string{"key": "value"},
		}

		p := New[ComplexStruct]()
		p = p.Add("/Int", 42)
		p = p.Add("/String", "hello")
		p = p.Add("/IntSlice/-", 4)
		p = p.Replace("/StrMap/key", "newValue")

		err := p.Apply(target)
		if err != nil {
			t.Fatalf("Apply failed: %v", err)
		}

		// Check values
		if target.Int != 42 {
			t.Errorf("expected Int=42, got %d", target.Int)
		}
		if target.String != "hello" {
			t.Errorf("expected String='hello', got %q", target.String)
		}
		if len(target.IntSlice) != 4 || target.IntSlice[3] != 4 {
			t.Errorf("expected IntSlice=[1,2,3,4], got %v", target.IntSlice)
		}
		if target.StrMap["key"] != "newValue" {
			t.Errorf("expected StrMap[key]='newValue', got %q", target.StrMap["key"])
		}
	})

	// Test removing values
	t.Run("Remove", func(t *testing.T) {
		target := &ComplexStruct{
			IntSlice: []int{1, 2, 3},
			StrMap:   map[string]string{"key1": "value1", "key2": "value2"},
		}

		p := New[ComplexStruct]()
		p = p.Remove("/IntSlice/1")
		p = p.Remove("/StrMap/key1")

		err := p.Apply(target)
		if err != nil {
			t.Fatalf("Apply failed: %v", err)
		}

		// Check values
		if len(target.IntSlice) != 2 || target.IntSlice[0] != 1 || target.IntSlice[1] != 3 {
			t.Errorf("expected IntSlice=[1,3], got %v", target.IntSlice)
		}
		if len(target.StrMap) != 1 || target.StrMap["key2"] != "value2" {
			t.Errorf("expected StrMap={key2:value2}, got %v", target.StrMap)
		}
	})

	// Test moving values
	t.Run("Move", func(t *testing.T) {
		target := &ComplexStruct{
			Int:      42,
			IntSlice: []int{1, 2, 3},
			StrMap:   map[string]string{"key1": "value1", "key2": "value2"},
		}

		p := New[ComplexStruct]()
		p = p.Move("/Int", "/Nested/Simple/Int")
		p = p.Move("/StrMap/key1", "/StrMap/key3")

		err := p.Apply(target)
		if err != nil {
			t.Fatalf("Apply failed: %v", err)
		}

		// Check values
		if target.Int != 0 {
			t.Errorf("expected Int=0, got %d", target.Int)
		}
		if target.Nested.Simple.Int != 42 {
			t.Errorf("expected Nested.Simple.Int=42, got %d", target.Nested.Simple.Int)
		}
		if target.StrMap["key1"] != "" {
			t.Errorf("expected StrMap[key1] to be removed, got %q", target.StrMap["key1"])
		}
		if target.StrMap["key3"] != "value1" {
			t.Errorf("expected StrMap[key3]='value1', got %q", target.StrMap["key3"])
		}
	})

	// Test copying values
	t.Run("Copy", func(t *testing.T) {
		target := &ComplexStruct{
			Int:      42,
			IntSlice: []int{1, 2, 3},
			StrMap:   map[string]string{"key1": "value1"},
		}

		p := New[ComplexStruct]()
		p = p.Copy("/Int", "/Nested/Simple/Int")
		p = p.Copy("/StrMap/key1", "/StrMap/key2")

		err := p.Apply(target)
		if err != nil {
			t.Fatalf("Apply failed: %v", err)
		}

		// Check values
		if target.Int != 42 {
			t.Errorf("expected Int=42, got %d", target.Int)
		}
		if target.Nested.Simple.Int != 42 {
			t.Errorf("expected Nested.Simple.Int=42, got %d", target.Nested.Simple.Int)
		}
		if target.StrMap["key1"] != "value1" {
			t.Errorf("expected StrMap[key1]='value1', got %q", target.StrMap["key1"])
		}
		if target.StrMap["key2"] != "value1" {
			t.Errorf("expected StrMap[key2]='value1', got %q", target.StrMap["key2"])
		}
	})

	// Test test operation
	t.Run("Test Success", func(t *testing.T) {
		target := &ComplexStruct{
			Int:    42,
			String: "hello",
		}

		p := New[ComplexStruct]()
		p = p.Test("/Int", 42)
		p = p.Test("/String", "hello")

		err := p.Apply(target)
		if err != nil {
			t.Errorf("Test should pass but got: %v", err)
		}
	})

	t.Run("Test Failure", func(t *testing.T) {
		target := &ComplexStruct{
			Int:    42,
			String: "hello",
		}

		p := New[ComplexStruct]()
		p = p.Test("/Int", 43)

		err := p.Apply(target)
		if err == nil {
			t.Error("Test should fail but passed")
		}
	})
}

// Test applying patches to nested and complex structures
func TestPatch_ComplexApply(t *testing.T) {
	target := &ComplexStruct{
		Int:      1,
		String:   "original",
		Bool:     true,
		IntSlice: []int{1, 2, 3},
		StrMap:   map[string]string{"key": "value"},
		IntMap:   map[int]string{1: "one", 2: "two"},
		FloatMap: map[float64]string{1.1: "one point one"},
		BoolMap:  map[bool]string{true: "true", false: "false"},
		Nested: NestedStruct{
			Simple: SimpleStruct{
				Int:    10,
				String: "nested",
				Bool:   false,
			},
			Pointer: &SimpleStruct{
				Int:    20,
				String: "pointer",
				Bool:   true,
			},
		},
		NestedPtr: &NestedStruct{
			Simple: SimpleStruct{
				Int:    30,
				String: "nested pointer",
				Bool:   false,
			},
		},
		Interface: "interface value",
		PtrSlice:  []*SimpleStruct{{Int: 1}, {Int: 2}},
		Array:     [3]int{1, 2, 3},
	}

	// Create a complex patch that exercises many paths
	p := New[ComplexStruct]()
	p = p.Add("/IntSlice/-", 4)
	p = p.Replace("/Int", 42)
	p = p.Replace("/Nested/Simple/Int", 100)
	// Fix: correct path for accessing a field in a pointer
	p = p.Replace("/Nested/Pointer/String", "updated pointer")
	p = p.Replace("/NestedPtr/Simple/Bool", true)
	p = p.Remove("/IntMap/1")
	p = p.Copy("/IntSlice/0", "/IntSlice/4")
	p = p.Move("/String", "/StrMap/original")
	p = p.Add("/Interface", "new interface value")

	err := p.Apply(target)
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}

	// Check results
	checks := []struct {
		name     string
		got      interface{}
		expected interface{}
	}{
		{"Int", target.Int, 42},
		{"String", target.String, ""},
		{"IntSlice length", len(target.IntSlice), 5},
		{"IntSlice[3]", target.IntSlice[3], 4},
		{"IntSlice[4]", target.IntSlice[4], 1},
		{"StrMap[original]", target.StrMap["original"], "original"},
		{"IntMap length", len(target.IntMap), 1},
		{"Nested.Simple.Int", target.Nested.Simple.Int, 100},
		{"Nested.Pointer.String", target.Nested.Pointer.String, "updated pointer"},
		{"NestedPtr.Simple.Bool", target.NestedPtr.Simple.Bool, true},
		{"Interface", target.Interface, "new interface value"},
	}

	for _, check := range checks {
		if !reflect.DeepEqual(check.got, check.expected) {
			t.Errorf("%s: got %v, expected %v", check.name, check.got, check.expected)
		}
	}
}

// Test error cases with Apply
func TestPatch_ApplyErrors(t *testing.T) {
	tests := []struct {
		name   string
		target ComplexStruct
		patch  func() Patch[ComplexStruct]
	}{
		{
			name:   "Invalid path",
			target: ComplexStruct{},
			patch: func() Patch[ComplexStruct] {
				p := New[ComplexStruct]()
				return append(p, Operation{
					Op:   OperationTypeAdd,
					Path: "/NonExistent",
				})
			},
		},
		{
			name:   "Type mismatch",
			target: ComplexStruct{},
			patch: func() Patch[ComplexStruct] {
				p := New[ComplexStruct]()
				return append(p, Operation{
					Op:    OperationTypeAdd,
					Path:  "/Int",
					Value: "not an int",
				})
			},
		},
		{
			name:   "Out of bounds",
			target: ComplexStruct{IntSlice: []int{1}},
			patch: func() Patch[ComplexStruct] {
				p := New[ComplexStruct]()
				return append(p, Operation{
					Op:    OperationTypeAdd,
					Path:  "/IntSlice/10",
					Value: 42,
				})
			},
		},
		{
			name:   "Missing map key",
			target: ComplexStruct{StrMap: map[string]string{}},
			patch: func() Patch[ComplexStruct] {
				p := New[ComplexStruct]()
				return append(p, Operation{
					Op:   OperationTypeRemove,
					Path: "/StrMap/notfound",
				})
			},
		},
		{
			name:   "Test failure",
			target: ComplexStruct{Int: 41},
			patch: func() Patch[ComplexStruct] {
				p := New[ComplexStruct]()
				return append(p, Operation{
					Op:    OperationTypeTest,
					Path:  "/Int",
					Value: 42,
				})
			},
		},
		{
			name:   "Invalid operation type",
			target: ComplexStruct{},
			patch: func() Patch[ComplexStruct] {
				p := New[ComplexStruct]()
				return append(p, Operation{
					Op:   OperationType("invalid"),
					Path: "/Int",
				})
			},
		},
		{
			name:   "Move invalid source",
			target: ComplexStruct{},
			patch: func() Patch[ComplexStruct] {
				p := New[ComplexStruct]()
				return append(p, Operation{
					Op:   OperationTypeMove,
					From: "/NonExistent",
					Path: "/Int",
				})
			},
		},
		{
			name:   "Copy invalid source",
			target: ComplexStruct{},
			patch: func() Patch[ComplexStruct] {
				p := New[ComplexStruct]()
				return append(p, Operation{
					Op:   OperationTypeCopy,
					From: "/NonExistent",
					Path: "/Int",
				})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := tt.patch()
			target := tt.target
			err := p.Apply(&target)
			if err == nil {
				t.Error("Expected error but got nil")
			}
		})
	}
}

// Test adding to different map key types
func TestPatch_MapKeyTypes(t *testing.T) {
	target := &ComplexStruct{
		StrMap:   map[string]string{},
		IntMap:   map[int]string{},
		FloatMap: map[float64]string{},
		BoolMap:  map[bool]string{},
	}

	p := New[ComplexStruct]()
	p = p.Add("/StrMap/key", "str value")
	p = p.Add("/IntMap/42", "int value")
	p = p.Add("/FloatMap/3.14", "float value")
	p = p.Add("/BoolMap/true", "bool value")

	err := p.Apply(target)
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}

	checks := []struct {
		name     string
		got      string
		expected string
	}{
		{"StrMap[key]", target.StrMap["key"], "str value"},
		{"IntMap[42]", target.IntMap[42], "int value"},
		{"FloatMap[3.14]", target.FloatMap[3.14], "float value"},
		{"BoolMap[true]", target.BoolMap[true], "bool value"},
	}

	for _, check := range checks {
		if check.got != check.expected {
			t.Errorf("%s: got %q, expected %q", check.name, check.got, check.expected)
		}
	}
}

// Test unexported fields (should work with internal.DisableRO)
func TestPatch_UnexportedFields(t *testing.T) {
	type structWithUnexported struct {
		Exported   string
		unexported string
	}

	target := &structWithUnexported{
		Exported:   "exported",
		unexported: "original",
	}

	// Create a patch to update the unexported field
	// This won't pass validation because validatePath can't see unexported fields
	// But we can create the operation manually for testing
	p := Patch[structWithUnexported]{
		Operation{
			Op:    OperationTypeReplace,
			Path:  "/unexported",
			Value: "modified",
		},
	}

	// This will fail at runtime since we can't access unexported fields
	err := p.Apply(target)
	if err == nil {
		t.Log("Note: Your internal.DisableRO allows modifying unexported fields")
		if target.unexported != "modified" {
			t.Errorf("unexported: expected 'modified', got '%s'", target.unexported)
		}
	} else {
		t.Log("Expected error accessing unexported field:", err)
	}
}

// Helper function for testing panics
func testPanics(t *testing.T, name string, f func()) {
	t.Helper()
	defer func() {
		if r := recover(); r == nil {
			t.Error(name)
		}
	}()
	f()
}
