package deep

import (
	"encoding/json"
	"testing"

	"github.com/brunoga/deep/v4/cond"
)

func TestBuilder_Basic(t *testing.T) {
	type S struct {
		A int
		B string
	}
	b := NewPatchBuilder[S]()
	b.Field("A").Set(1, 2)
	
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
	b := NewPatchBuilder[S]()
	b.Field("NonExistent")
	_, err := b.Build()
	if err == nil {
		t.Error("Expected error for non-existent field")
	}

	b2 := NewPatchBuilder[S]()
	b2.Field("A").Set("string", 2)
	// Set currently doesn't check type compatibility of 'old' value at build time,
	// but let's check Add which does.
	b2.Field("A").Add(0, 1)
	_, err = b2.Build()
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
	b := NewPatchBuilder[Parent]()
	b.Field("Kids").Add(0, Child{Name: "NewKid"})
	b.Field("Kids").Index(0).Field("Name").Set("Old", "Modified")

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
	b := NewPatchBuilder[map[string]int]()
	b.Add("new", 100).Delete("old", 50)
	b.MapKey("mod").Set(10, 20)

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

	b := NewPatchBuilder[*S]()
	// Drill down through pointer
	b.Elem().Field("Val").Set(10, 20)

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

	b := NewPatchBuilder[Arr]()
	// Modify index 1
	b.Index(1).Set(2, 20)

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
	b1 := NewPatchBuilder[int]()
	b1.Delete(0, 0)
	if _, err := b1.Build(); err == nil {
		t.Error("Expected error for Delete on int")
	}

	// Delete with wrong index type for slice
	b2 := NewPatchBuilder[[]int]()
	b2.Delete("string_key", 0)
	if _, err := b2.Build(); err == nil {
		t.Error("Expected error for non-int delete on slice")
	}

	// Add on non-slice
	b3 := NewPatchBuilder[map[string]int]()
	b3.Add(0, 1)
	if _, err := b3.Build(); err == nil {
		t.Error("Expected error for Add on map")
	}

	// Add on non-map (with string key)
	b4 := NewPatchBuilder[[]int]()
	b4.Add("k", 1)
	if _, err := b4.Build(); err == nil {
		t.Error("Expected error for string Add on slice")
	}
}

func TestBuilder_Exhaustive(t *testing.T) {
	t.Run("NestedPointer", func(t *testing.T) {
		type S struct{ V int }
		type P struct{ S *S }

		b := NewPatchBuilder[P]()
		b.Field("S").Elem().Field("V").Set(nil, 10)

		p, _ := b.Build()
		var target P
		p.Apply(&target)
		if target.S == nil || target.S.V != 10 {
			t.Errorf("Nested pointer application failed: %+v", target.S)
		}
	})

	t.Run("MapKeyCreation", func(t *testing.T) {
		b := NewPatchBuilder[map[string]int]()
		b.Add("a", 1)

		p, _ := b.Build()
		var m map[string]int
		p.Apply(&m)
		if m["a"] != 1 {
			t.Errorf("Map key creation failed: %v", m)
		}
	})

	t.Run("EmptyPatch", func(t *testing.T) {
		b := NewPatchBuilder[int]()
		p, err := b.Build()
		if err != nil || p != nil {
			t.Errorf("Expected nil patch for no operations, got %v, %v", p, err)
		}
	})

	t.Run("NavigationErrors", func(t *testing.T) {
		type S struct{ A int }
		b := NewPatchBuilder[S]()
		b.Navigate("/NonExistent")
		if _, err := b.Build(); err == nil {
			t.Error("Expected error for non-existent field navigation")
		}

		type M struct{ Data map[int]int }
		b2 := NewPatchBuilder[M]()
		b2.Navigate("/Data/not_an_int")
		if _, err := b2.Build(); err == nil {
			t.Error("Expected error for invalid map key type navigation")
		}
	})

	t.Run("DeleteMore", func(t *testing.T) {
		b := NewPatchBuilder[map[int]string]()
		b.Delete(1, "old")
		if _, err := b.Build(); err != nil {
			t.Errorf("Delete on map failed: %v", err)
		}

		type S struct{ A int }
		b2 := NewPatchBuilder[S]()
		b2.Delete("A", 1)
		if _, err := b2.Build(); err == nil {
			t.Error("Expected error for Delete on struct")
		}
	})

	t.Run("ElemEdgeCases", func(t *testing.T) {
		b := NewPatchBuilder[**int]()
		b.Elem().Elem().Set(1, 2)

		p, _ := b.Build()
		v := 1
		pv := &v
		ppv := &pv
		p.Apply(&ppv)
		if **ppv != 2 {
			t.Errorf("Expected 2, got %d", **ppv)
		}

		b2 := NewPatchBuilder[int]()
		b2.Elem()
		if _, err := b2.Build(); err != nil {
			t.Errorf("Elem on non-pointer should not error: %v", err)
		}
	})

	t.Run("WithConditionOnAllTypes", func(t *testing.T) {
		cond := cond.Eq[int]("", 1)

		b1 := NewPatchBuilder[*int]()
		b1.WithCondition(cond)

		b2 := NewPatchBuilder[[]int]()
		b2.WithCondition(cond)

		b3 := NewPatchBuilder[map[string]int]()
		b3.WithCondition(cond)

		b4 := NewPatchBuilder[[1]int]()
		b4.WithCondition(cond)

		type IfaceStruct struct{ I any }
		b5 := NewPatchBuilder[IfaceStruct]()
		b5.Field("I").WithCondition(cond)

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
	b := NewPatchBuilder[builderTestConfig]()
	b.AddCondition("/Network/Port > 1024")

	p, err := b.Build()
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	c1 := builderTestConfig{Network: builderTestNetworkConfig{Port: 8080}}
	if err := p.ApplyChecked(&c1); err != nil {
		t.Errorf("ApplyChecked failed for valid port: %v", err)
	}

	c2 := builderTestConfig{Network: builderTestNetworkConfig{Port: 80}}
	if err := p.ApplyChecked(&c2); err == nil {
		t.Errorf("ApplyChecked should have failed for invalid port")
	}
}

func TestBuilder_AddConditionLCP(t *testing.T) {
	b := NewPatchBuilder[builderTestConfig]()
	b.AddCondition("/Network/Port > 1024 AND /Network/Host == 'localhost'")

	p, err := b.Build()
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}

	c1 := builderTestConfig{Network: builderTestNetworkConfig{Port: 8080, Host: "localhost"}}
	if err := p.ApplyChecked(&c1); err != nil {
		t.Errorf("ApplyChecked failed for valid config: %v", err)
	}

	c2 := builderTestConfig{Network: builderTestNetworkConfig{Port: 80, Host: "localhost"}}
	if err := p.ApplyChecked(&c2); err == nil {
		t.Errorf("ApplyChecked should have failed for invalid port")
	}

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

	b := NewPatchBuilder[Deep]()
	b.AddCondition("/A/B/C == 10")

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

	b := NewPatchBuilder[MapStruct]()
	b.AddCondition("/Data/key1 > 10")

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
	b := NewPatchBuilder[S]()
	b.AddCondition("/A > /B")

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

	builder := NewPatchBuilder[User]()
	builder.Field("Name").If(cond.Eq[User]("/Age", 20)).Set("Alice", "Bob")
	builder.Field("Age").If(cond.Eq[User]("/Name", "Alice")).Set(30, 31)

	patch, _ := builder.Build()

	if err := patch.ApplyChecked(&u); err != nil {
		t.Fatalf("ApplyChecked failed: %v", err)
	}

	if u.Name != "Alice" {
		t.Errorf("Expected Name=Alice, got %s", u.Name)
	}
	if u.Age != 31 {
		t.Errorf("Expected Age=31, got %d", u.Age)
	}
}

func TestBuilder_Unless(t *testing.T) {
	type User struct {
		Name string
	}
	u := User{Name: "Alice"}
	builder := NewPatchBuilder[User]()
	builder.Field("Name").Unless(cond.Eq[User]("/Name", "Alice")).Set("Alice", "Bob")
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

	builder := NewPatchBuilder[User]()
	builder.Field("Name").Test("Alice")
	builder.Field("Age").Set(30, 31)
	patch, _ := builder.Build()

	if err := patch.ApplyChecked(&u); err != nil {
		t.Fatalf("ApplyChecked failed: %v", err)
	}
	if u.Age != 31 {
		t.Errorf("Expected Age=31, got %d", u.Age)
	}

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

	builder := NewPatchBuilder[User]()
	builder.Field("AltName").Copy("/Name")

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

	builder := NewPatchBuilder[User]()
	builder.Field("AltName").Move("/Name")

	patch, _ := builder.Build()

	jsonPatchBytes, _ := patch.ToJSONPatch()
	var ops []map[string]any
	json.Unmarshal(jsonPatchBytes, &ops)
	if ops[0]["op"] != "move" || ops[0]["from"] != "/Name" || ops[0]["path"] != "/AltName" {
		t.Errorf("Unexpected JSON Patch for move: %s", string(jsonPatchBytes))
	}

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

	builder := NewPatchBuilder[User]()
	builder.Field("AltName").Move("/Name")

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

	builder := NewPatchBuilder[User]()
	builder.AddCondition("/Name == 'Alice'")
	builder.Field("Age").Set(30, 31)

	patch, _ := builder.Build()

	if err := patch.ApplyChecked(&u); err != nil {
		t.Fatalf("ApplyChecked failed: %v", err)
	}

	if u.Age != 31 {
		t.Errorf("Expected Age=31, got %d", u.Age)
	}

	u.Name = "Bob"
	u.Age = 30
	if err := patch.ApplyChecked(&u); err == nil {
		t.Fatal("Expected error because Name is Bob")
	}
}

func TestBuilder_DeleteExhaustive(t *testing.T) {
	b1 := NewPatchBuilder[map[string]int]()
	b1.Delete("a", 1)

	b2 := NewPatchBuilder[[]int]()
	b2.Delete(0, 1)

	b3 := NewPatchBuilder[int]()
	b3.Delete("a", 1)
	if _, err := b3.Build(); err == nil {
		t.Error("Expected error deleting from non-container")
	}
}

func TestBuilder_AddCondition_CornerCases(t *testing.T) {
	type Data struct {
		A int
		B int
	}

	b := NewPatchBuilder[Data]()
	// Valid complex condition
	b.AddCondition("/A == 1 AND /B == 1")

	b2 := NewPatchBuilder[Data]()
	b2.AddCondition("INVALID")
	if _, err := b2.Build(); err == nil {
		t.Error("Expected error for invalid condition")
	}
}

func TestBuilder_Log(t *testing.T) {
	type User struct {
		Name string
	}
	u := User{Name: "Alice"}

	builder := NewPatchBuilder[User]()
	builder.Field("Name").Log("Checking user name")

	patch, _ := builder.Build()

	if err := patch.ApplyChecked(&u); err != nil {
		t.Fatalf("ApplyChecked failed: %v", err)
	}

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
	b := NewPatchBuilder[State]()
	b.Navigate("/Data").Put(map[string]int{"a": 1})
	p, _ := b.Build()

	s := State{Data: make(map[string]int)}
	p.Apply(&s)
	if s.Data["a"] != 1 {
		t.Errorf("expected 1, got %d", s.Data["a"])
	}
}
