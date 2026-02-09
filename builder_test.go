package deep

import (
	"testing"
)

func TestBuilder_Basic(t *testing.T) {
	type S struct {
		A int
		B string
	}
	b := NewBuilder[S]()
	root := b.Root()
	node, err := root.Field("A")
	if err != nil {
		t.Fatalf("Field A failed: %v", err)
	}
	node.Set(1, 2)
	patch, err := b.Build()
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}
	val := S{A: 1, B: "test"}
	patch.Apply(&val)
	if val.A != 2 {
		t.Errorf("Expected A=2, got %d", val.A)
	}
	if val.B != "test" {
		t.Errorf("Expected B=test, got %s", val.B)
	}
}

func TestBuilder_Validation(t *testing.T) {
	type S struct {
		A int
	}
	b := NewBuilder[S]()
	root := b.Root()
	_, err := root.Field("NonExistent")
	if err == nil {
		t.Error("Expected error for non-existent field")
	}
	node, err := root.Field("A")
	if err != nil {
		t.Fatalf("Field A failed: %v", err)
	}
	// Note: Set no longer returns error directly for type validation
	// as it follows the builder pattern. Users should ensure types match
	// or we could add error tracking to the builder.
	node.Set("string", 2)
	
	err = node.Add(0, 1)
	if err == nil {
		t.Error("Expected error for Add on non-slice node")
	}
}

func TestBuilder_Nested(t *testing.T) {
	type Child struct {
		Name string
	}
	type Parent struct {
		Kids []Child
	}
	b := NewBuilder[Parent]()
	root := b.Root()
	kidsNode, err := root.Field("Kids")
	if err != nil {
		t.Fatalf("Field Kids failed: %v", err)
	}
	err = kidsNode.Add(0, Child{Name: "NewKid"})
	if err != nil {
		t.Fatalf("Add failed: %v", err)
	}
	kid0, err := kidsNode.Index(0)
	if err != nil {
		t.Fatalf("Index failed: %v", err)
	}
	nameNode, err := kid0.Field("Name")
	if err != nil {
		t.Fatalf("Field Name failed: %v", err)
	}
	nameNode.Set("Old", "Modified")
	
	patch, err := b.Build()
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}
	val := Parent{
		Kids: []Child{{Name: "Old"}},
	}
	patch.Apply(&val)
	if len(val.Kids) != 2 {
		t.Fatalf("Expected 2 kids, got %d", len(val.Kids))
	}
	if val.Kids[0].Name != "NewKid" {
		t.Errorf("Expected first kid to be NewKid, got %s", val.Kids[0].Name)
	}
	if val.Kids[1].Name != "Modified" {
		t.Errorf("Expected second kid to be Modified, got %s", val.Kids[1].Name)
	}
}

func TestBuilder_Map(t *testing.T) {
	b := NewBuilder[map[string]int]()
	root := b.Root()
	err := root.AddMapEntry("new", 100)
	if err != nil {
		t.Fatalf("AddMapEntry failed: %v", err)
	}
	err = root.Delete("old", 50)
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	keyNode, err := root.MapKey("mod")
	if err != nil {
		t.Fatalf("MapKey failed: %v", err)
	}
	keyNode.Set(10, 20)
	
	patch, err := b.Build()
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}
	m := map[string]int{"old": 50, "mod": 10}
	patch.Apply(&m)
	if m["new"] != 100 {
		t.Errorf("Expected new=100")
	}
	if _, ok := m["old"]; ok {
		t.Errorf("Expected old to be deleted")
	}
	if m["mod"] != 20 {
		t.Errorf("Expected mod=20")
	}
}

func TestBuilder_Elem(t *testing.T) {
	type S struct {
		Val int
	}
	s := S{Val: 10}
	p := &s // Pointer to struct

	b := NewBuilder[*S]()
	root := b.Root()

	// Drill down through pointer
	node, err := root.Elem().Field("Val")
	if err != nil {
		t.Fatalf("Elem/Field failed: %v", err)
	}
	node.Set(10, 20)

	patch, err := b.Build()
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	if err := patch.ApplyChecked(&p); err != nil {
		t.Fatalf("ApplyChecked failed: %v", err)
	}

	if p.Val != 20 {
		t.Errorf("Expected Val=20, got %d", p.Val)
	}
}

func TestBuilder_Array(t *testing.T) {
	type Arr [3]int
	a := Arr{1, 2, 3}

	b := NewBuilder[Arr]()
	root := b.Root()

	// Modify index 1
	node, err := root.Index(1)
	if err != nil {
		t.Fatalf("Index(1) failed: %v", err)
	}
	node.Set(2, 20)

	patch, err := b.Build()
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	if err := patch.ApplyChecked(&a); err != nil {
		t.Fatalf("ApplyChecked failed: %v", err)
	}

	if a[1] != 20 {
		t.Errorf("Expected a[1]=20, got %d", a[1])
	}
}

func TestBuilder_ErrorPaths(t *testing.T) {
	// Delete on non-container
	b1 := NewBuilder[int]()
	err := b1.Root().Delete(0, 0)
	if err == nil {
		t.Error("Expected error for Delete on int")
	}

	// Delete with wrong index type for slice
	b2 := NewBuilder[[]int]()
	err = b2.Root().Delete("string_key", 0)
	if err == nil {
		t.Error("Expected error for non-int delete on slice")
	}

	// Add on non-slice
	b3 := NewBuilder[map[string]int]()
	err = b3.Root().Add(0, 1)
	if err == nil {
		t.Error("Expected error for Add on map")
	}

	// AddMapEntry on non-map
	b4 := NewBuilder[[]int]()
	err = b4.Root().AddMapEntry("k", 1)
	if err == nil {
		t.Error("Expected error for AddMapEntry on slice")
	}
}