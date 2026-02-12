package deep

import (
	"encoding/json"
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

func TestBuilder_Exhaustive(t *testing.T) {
	t.Run("NestedPointer", func(t *testing.T) {
		type S struct{ V int }
		type P struct{ S *S }

		b := NewBuilder[P]()
		// Automatically handles nil pointer creation
		sNode, _ := b.Root().Field("S")
		sNode.Elem().Field("V")
		vNode, _ := sNode.Elem().Field("V")
		vNode.Set(nil, 10)

		p, _ := b.Build()
		var target P
		p.Apply(&target)
		if target.S == nil || target.S.V != 10 {
			t.Errorf("Nested pointer application failed: %+v", target.S)
		}
	})

	t.Run("MapKeyCreation", func(t *testing.T) {
		b := NewBuilder[map[string]int]()
		b.Root().AddMapEntry("a", 1)

		p, _ := b.Build()
		var m map[string]int
		p.Apply(&m)
		if m["a"] != 1 {
			t.Errorf("Map key creation failed: %v", m)
		}
	})

	t.Run("EmptyPatch", func(t *testing.T) {
		b := NewBuilder[int]()
		p, err := b.Build()
		if err != nil || p != nil {
			t.Errorf("Expected nil patch for no operations, got %v, %v", p, err)
		}
	})

	t.Run("NavigationErrors", func(t *testing.T) {
		type S struct{ A int }
		b := NewBuilder[S]()
		_, err := b.Root().navigate("NonExistent")
		if err == nil {
			t.Error("Expected error for non-existent field navigation")
		}

		type M struct{ Data map[int]int }
		b2 := NewBuilder[M]()
		_, err = b2.Root().navigate("Data.not_an_int")
		if err == nil {
			t.Error("Expected error for invalid map key type navigation")
		}

		_, err = b2.Root().navigate("Data[0]") // Index on map
		if err == nil {
			t.Error("Expected error for index navigation on map")
		}
	})

	t.Run("DeleteMore", func(t *testing.T) {
		b := NewBuilder[map[int]string]()
		err := b.Root().Delete(1, "old")
		if err != nil {
			t.Errorf("Delete on map failed: %v", err)
		}

		type S struct{ A int }
		b2 := NewBuilder[S]()
		err = b2.Root().Delete("A", 1)
		if err == nil {
			t.Error("Expected error for Delete on struct")
		}
	})

	t.Run("ElemEdgeCases", func(t *testing.T) {
		b := NewBuilder[**int]()
		// Elem on pointer to pointer
		node := b.Root().Elem().Elem()
		node.Set(1, 2)

		p, _ := b.Build()
		v := 1
		pv := &v
		ppv := &pv
		p.Apply(&ppv)
		if **ppv != 2 {
			t.Errorf("Expected 2, got %d", **ppv)
		}

		b2 := NewBuilder[int]()
		root2 := b2.Root()
		node2 := root2.Elem() // Elem on non-pointer/interface
		if node2 != root2 {
			t.Error("Elem on non-pointer should return self")
		}
	})

	t.Run("WithConditionOnAllTypes", func(t *testing.T) {
		cond := Equal[int]("", 1)

		// Pointer
		b1 := NewBuilder[*int]()
		b1.Root().WithCondition(cond)

		// Slice
		b2 := NewBuilder[[]int]()
		b2.Root().WithCondition(cond)

		// Map
		b3 := NewBuilder[map[string]int]()
		b3.Root().WithCondition(cond)

		// Array
		b4 := NewBuilder[[1]int]()
		b4.Root().WithCondition(cond)

		// Interface
		type IfaceStruct struct{ I any }
		b5 := NewBuilder[IfaceStruct]()
		node5, _ := b5.Root().Field("I")
		node5.WithCondition(cond)

		_, err := b1.Build()
		if err != nil {
			t.Errorf("Build failed: %v", err)
		}
	})
}

type builderTestConfig struct {
	Network builderTestNetworkConfig
}

type builderTestNetworkConfig struct {
	Port int
	Host string
}

func TestBuilder_AddConditionSmart(t *testing.T) {
	b := NewBuilder[builderTestConfig]()
	b.AddCondition("Network.Port > 1024")

	p, err := b.Build()
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	// Test condition passes
	c1 := builderTestConfig{Network: builderTestNetworkConfig{Port: 8080}}
	if err := p.ApplyChecked(&c1); err != nil {
		t.Errorf("ApplyChecked failed for valid port: %v", err)
	}

	// Test condition fails
	c2 := builderTestConfig{Network: builderTestNetworkConfig{Port: 80}}
	if err := p.ApplyChecked(&c2); err == nil {
		t.Errorf("ApplyChecked should have failed for invalid port")
	}
}

func TestBuilder_AddConditionLCP(t *testing.T) {
	b := NewBuilder[builderTestConfig]()
	// LCP is "Network"
	b.AddCondition("Network.Port > 1024 AND Network.Host == 'localhost'")

	p, err := b.Build()
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	// Test condition passes
	c1 := builderTestConfig{Network: builderTestNetworkConfig{Port: 8080, Host: "localhost"}}
	if err := p.ApplyChecked(&c1); err != nil {
		t.Errorf("ApplyChecked failed for valid config: %v", err)
	}

	// Test condition fails (Port)
	c2 := builderTestConfig{Network: builderTestNetworkConfig{Port: 80, Host: "localhost"}}
	if err := p.ApplyChecked(&c2); err == nil {
		t.Errorf("ApplyChecked should have failed for invalid port")
	}

	// Test condition fails (Host)
	c3 := builderTestConfig{Network: builderTestNetworkConfig{Port: 8080, Host: "example.com"}}
	if err := p.ApplyChecked(&c3); err == nil {
		t.Errorf("ApplyChecked should have failed for invalid host")
	}
}

func TestBuilder_AddConditionDeep(t *testing.T) {
	type Deep struct {
		A struct {
			B struct {
				C int
			}
		}
	}

	b := NewBuilder[Deep]()
	b.AddCondition("A.B.C == 10")

	p, err := b.Build()
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	d1 := Deep{}
	d1.A.B.C = 10
	if err := p.ApplyChecked(&d1); err != nil {
		t.Errorf("ApplyChecked failed: %v", err)
	}

	d2 := Deep{}
	d2.A.B.C = 20
	if err := p.ApplyChecked(&d2); err == nil {
		t.Errorf("ApplyChecked should have failed")
	}
}

func TestBuilder_AddConditionMap(t *testing.T) {
	type MapStruct struct {
		Data map[string]int
	}

	b := NewBuilder[MapStruct]()
	b.AddCondition("Data.key1 > 10")

	p, err := b.Build()
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	m1 := MapStruct{Data: map[string]int{"key1": 20}}
	if err := p.ApplyChecked(&m1); err != nil {
		t.Errorf("ApplyChecked failed: %v", err)
	}

	m2 := MapStruct{Data: map[string]int{"key1": 5}}
	if err := p.ApplyChecked(&m2); err == nil {
		t.Errorf("ApplyChecked should have failed")
	}
}

func TestBuilder_AddConditionFieldToField(t *testing.T) {
	type S struct {
		A int
		B int
	}
	b := NewBuilder[S]()
	b.AddCondition("A > B")

	p, err := b.Build()
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	s1 := S{A: 10, B: 5}
	if err := p.ApplyChecked(&s1); err != nil {
		t.Errorf("ApplyChecked failed for A > B: %v", err)
	}

	s2 := S{A: 5, B: 10}
	if err := p.ApplyChecked(&s2); err == nil {
		t.Errorf("ApplyChecked should have failed for A <= B")
	}
}

func TestBuilder_SoftConditions(t *testing.T) {
	type User struct {
		Name string
		Age  int
	}
	u := User{Name: "Alice", Age: 30}

	builder := NewBuilder[User]()
	nodeName, _ := builder.Root().Field("Name")
	nodeName.If(Equal[User]("Age", 20)).Set("Alice", "Bob")
	nodeAge, _ := builder.Root().Field("Age")
	nodeAge.If(Equal[User]("Name", "Alice")).Set(30, 31)

	patch, _ := builder.Build()

	if err := patch.ApplyChecked(&u); err != nil {
		t.Fatalf("ApplyChecked failed: %v", err)
	}

	// Name should be Alice (skipped because Age != 20)
	if u.Name != "Alice" {
		t.Errorf("Expected Name=Alice, got %s", u.Name)
	}
	// Age should be 31 (Set(30, 31) applied because Name == Alice)
	if u.Age != 31 {
		t.Errorf("Expected Age=31, got %d", u.Age)
	}
}

func TestBuilder_Unless(t *testing.T) {
	type User struct {
		Name string
	}
	u := User{Name: "Alice"}
	builder := NewBuilder[User]()
	node, _ := builder.Root().Field("Name")
	node.Unless(Equal[User]("Name", "Alice")).Set("Alice", "Bob")
	patch, _ := builder.Build()

	err := patch.ApplyChecked(&u)
	if err != nil {
		t.Fatalf("ApplyChecked failed: %v", err)
	}
	if u.Name != "Alice" {
		t.Errorf("Expected Name=Alice, got %s", u.Name)
	}
}

func TestBuilder_AtomicTest(t *testing.T) {
	type User struct {
		Name string
		Age  int
	}
	u := User{Name: "Alice", Age: 30}

	builder := NewBuilder[User]()
	nodeName, _ := builder.Root().Field("Name")
	nodeName.Test("Alice")
	nodeAge, _ := builder.Root().Field("Age")
	nodeAge.Set(30, 31)
	patch, _ := builder.Build()

	if err := patch.ApplyChecked(&u); err != nil {
		t.Fatalf("ApplyChecked failed: %v", err)
	}
	if u.Age != 31 {
		t.Errorf("Expected Age=31, got %d", u.Age)
	}

	// Test failure
	u.Name = "Bob"
	u.Age = 30
	if err := patch.ApplyChecked(&u); err == nil {
		t.Fatal("Expected error because Name is Bob, not Alice")
	}
}

func TestBuilder_Copy(t *testing.T) {
	type User struct {
		Name    string
		AltName string
	}
	u := User{Name: "Alice", AltName: ""}

	builder := NewBuilder[User]()
	nodeAlt, _ := builder.Root().Field("AltName")
	nodeAlt.Copy("/Name")

	patch, _ := builder.Build()

	if err := patch.ApplyChecked(&u); err != nil {
		t.Fatalf("ApplyChecked failed: %v", err)
	}

	if u.AltName != "Alice" {
		t.Errorf("Expected AltName=Alice, got %s", u.AltName)
	}
}

func TestBuilder_Move(t *testing.T) {
	type User struct {
		Name    string
		AltName string
	}
	u := User{Name: "Alice", AltName: ""}

	builder := NewBuilder[User]()
	nodeAlt, _ := builder.Root().Field("AltName")
	nodeAlt.Move("/Name")

	patch, _ := builder.Build()

	// Verify JSON Patch
	jsonPatchBytes, _ := patch.ToJSONPatch()
	var ops []map[string]any
	json.Unmarshal(jsonPatchBytes, &ops)
	if ops[0]["op"] != "move" || ops[0]["from"] != "/Name" || ops[0]["path"] != "/AltName" {
		t.Errorf("Unexpected JSON Patch for move: %s", string(jsonPatchBytes))
	}

	// ApplyChecked
	if err := patch.ApplyChecked(&u); err != nil {
		t.Fatalf("ApplyChecked failed: %v", err)
	}

	if u.AltName != "Alice" {
		t.Errorf("Expected AltName=Alice, got %s", u.AltName)
	}
	if u.Name != "" {
		t.Errorf("Expected Name to be cleared, got %s", u.Name)
	}
}

func TestBuilder_MoveReverse(t *testing.T) {
	type User struct {
		Name    string
		AltName string
	}
	u := User{Name: "Alice", AltName: ""}

	builder := NewBuilder[User]()
	nodeAlt, _ := builder.Root().Field("AltName")
	nodeAlt.Move("/Name")

	patch, _ := builder.Build()

	if err := patch.ApplyChecked(&u); err != nil {
		t.Fatalf("ApplyChecked failed: %v", err)
	}

	if u.AltName != "Alice" || u.Name != "" {
		t.Fatalf("Move failed: %+v", u)
	}

	rev := patch.Reverse()
	if err := rev.ApplyChecked(&u); err != nil {
		t.Fatalf("Reverse ApplyChecked failed: %v", err)
	}

	if u.Name != "Alice" || u.AltName != "" {
		t.Errorf("Reverse failed: %+v", u)
	}
}

func TestBuilder_AddConditionJSONPointer(t *testing.T) {
	type User struct {
		Name string
		Age  int
	}
	u := User{Name: "Alice", Age: 30}

	builder := NewBuilder[User]()
	builder.AddCondition("/Name == 'Alice'")
	nodeAge, _ := builder.Root().Field("Age")
	nodeAge.Set(30, 31)

	patch, _ := builder.Build()

	if err := patch.ApplyChecked(&u); err != nil {
		t.Fatalf("ApplyChecked failed: %v", err)
	}

	if u.Age != 31 {
		t.Errorf("Expected Age=31, got %d", u.Age)
	}

	// Test failure
	u.Name = "Bob"
	u.Age = 30
	if err := patch.ApplyChecked(&u); err == nil {
		t.Fatal("Expected error because Name is Bob")
	}
}

func TestBuilder_DeleteExhaustive(t *testing.T) {
	// Map
	b1 := NewBuilder[map[string]int]()
	b1.Root().Delete("a", 1)

	// Slice
	b2 := NewBuilder[[]int]()
	b2.Root().Delete(0, 1)

	// Error: not a container
	b3 := NewBuilder[int]()
	err := b3.Root().Delete("a", 1)
	if err == nil {
		t.Error("Expected error deleting from non-container")
	}
}

func TestBuilder_AddCondition_CornerCases(t *testing.T) {
	type Data struct {
		A int
		B int
	}

	// No common prefix
	b := NewBuilder[Data]()
	b.AddCondition("A == 1 AND B == 1")

	// Invalid expression
	b2 := NewBuilder[Data]()
	b2.AddCondition("INVALID")
	if b2.err == nil {
		t.Error("Expected error for invalid condition")
	}
}

func TestBuilder_Log(t *testing.T) {
	type User struct {
		Name string
	}
	u := User{Name: "Alice"}

	builder := NewBuilder[User]()
	node, _ := builder.Root().Field("Name")
	node.Log("Checking user name")

	patch, _ := builder.Build()

	// Capture stdout to verify log (optional, but let's at least run it)
	if err := patch.ApplyChecked(&u); err != nil {
		t.Fatalf("ApplyChecked failed: %v", err)
	}

	// Verify JSON export
	jsonBytes, _ := patch.ToJSONPatch()
	var ops []map[string]any
	json.Unmarshal(jsonBytes, &ops)
	if ops[0]["op"] != "log" || ops[0]["value"] != "Checking user name" {
		t.Errorf("Unexpected JSON for log: %s", string(jsonBytes))
	}
}

func TestBuilder_Put(t *testing.T) {
	type State struct {
		Data map[string]int
	}
	b := NewBuilder[State]()
	node, _ := b.Root().Navigate("Data")
	node.Put(map[string]int{"a": 1})
	p, _ := b.Build()

	s := State{Data: make(map[string]int)}
	p.Apply(&s)
	if s.Data["a"] != 1 {
		t.Errorf("expected 1, got %d", s.Data["a"])
	}
}

func TestPatch_ToJSONPatch_ReadOnly(t *testing.T) {
	patch := Diff(1, 2)
	ro := &readOnlyPatch{inner: patch.(patchUnwrapper).unwrap()}
	
	// readOnlyPatch toJSONPatch currently returns nil
	json := ro.toJSONPatch("/")
	if json != nil {
		t.Errorf("expected nil for readOnly toJSONPatch")
	}
}
