package v5

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/brunoga/deep/v5/crdt/hlc"
	"github.com/brunoga/deep/v5/internal/engine"
)

func TestV5_GobSerialization(t *testing.T) {
	Register[User]()

	u1 := User{ID: 1, Name: "Alice"}
	u2 := User{ID: 2, Name: "Bob"}
	patch := Diff(u1, u2)

	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	if err := enc.Encode(patch); err != nil {
		t.Fatalf("Gob Encode failed: %v", err)
	}

	var patch2 Patch[User]
	dec := gob.NewDecoder(&buf)
	if err := dec.Decode(&patch2); err != nil {
		t.Fatalf("Gob Decode failed: %v", err)
	}

	u3 := u1
	Apply(&u3, patch2)
	if !Equal(u2, u3) {
		t.Errorf("Gob roundtrip failed: got %+v, want %+v", u3, u2)
	}
}

func TestV5_Causality(t *testing.T) {
	type Doc struct {
		Title LWW[string]
	}

	clock := hlc.NewClock("node-a")
	ts1 := clock.Now()
	ts2 := clock.Now()

	d1 := Doc{Title: LWW[string]{Value: "Original", Timestamp: ts1}}

	// Newer update
	p1 := NewPatch[Doc]()
	p1.Operations = append(p1.Operations, Operation{
		Kind:      OpReplace,
		Path:      "/Title",
		New:       LWW[string]{Value: "Newer", Timestamp: ts2},
		Timestamp: ts2,
	})

	// Older update (simulating delayed arrival)
	p2 := NewPatch[Doc]()
	p2.Operations = append(p2.Operations, Operation{
		Kind:      OpReplace,
		Path:      "/Title",
		New:       LWW[string]{Value: "Older", Timestamp: ts1},
		Timestamp: ts1,
	})

	// 1. Apply newer then older -> newer should win
	res1 := d1
	Apply(&res1, p1)
	Apply(&res1, p2)
	if res1.Title.Value != "Newer" {
		t.Errorf("newer update lost: got %s, want Newer", res1.Title.Value)
	}

	// 2. Merge patches
	merged := Merge(p1, p2, nil)
	if len(merged.Operations) != 1 {
		t.Errorf("expected 1 merged op, got %d", len(merged.Operations))
	}
	if merged.Operations[0].Timestamp != ts2 {
		t.Errorf("merged op should have latest timestamp")
	}
}

func TestV5_Roundtrip(t *testing.T) {
	bio := Text{{Value: "stable"}}
	u1 := User{ID: 1, Name: "Alice", Bio: bio}
	u2 := User{ID: 1, Name: "Bob", Bio: bio}

	// 1. Diff

	patch := Diff(u1, u2)
	for _, op := range patch.Operations {
		t.Logf("Op: %s %s", op.Kind, op.Path)
	}
	if len(patch.Operations) != 1 {
		t.Fatalf("expected 1 operation, got %d", len(patch.Operations))
	}

	// 2. Apply
	u3 := u1
	if err := Apply(&u3, patch); err != nil {
		t.Fatalf("Apply failed: %v", err)
	}

	if !reflect.DeepEqual(u2, u3) {
		t.Errorf("got %+v, want %+v", u3, u2)
	}
}

func TestV5_Builder(t *testing.T) {
	type Config struct {
		Theme string `json:"theme"`
	}

	c1 := Config{Theme: "dark"}

	builder := Edit(&c1)
	Set(builder, Field(func(c *Config) *string { return &c.Theme }), "light")
	patch := builder.Build()

	if err := Apply(&c1, patch); err != nil {
		t.Fatalf("Apply failed: %v", err)
	}

	if c1.Theme != "light" {
		t.Errorf("got %s, want light", c1.Theme)
	}
}

func TestV5_Nested(t *testing.T) {
	u1 := User{
		ID:   1,
		Name: "Alice",
		Info: Detail{Age: 30, Address: "123 Main St"},
	}
	u2 := User{
		ID:   1,
		Name: "Alice",
		Info: Detail{Age: 31, Address: "123 Main St"},
	}

	// 1. Diff (should recursion into Info)
	patch := Diff(u1, u2)
	found := false
	for _, op := range patch.Operations {
		if op.Path == "/info/Age" {
			found = true
			if op.New != 31 {
				t.Errorf("expected 31, got %v", op.New)
			}
		}
	}
	if !found {
		t.Fatal("nested operation /info/Age not found")
	}

	// 2. Apply (currently fallback to reflection for nested paths)
	u3 := u1
	if err := Apply(&u3, patch); err != nil {
		t.Fatalf("Apply failed: %v", err)
	}

	if u3.Info.Age != 31 {
		t.Errorf("got %d, want 31", u3.Info.Age)
	}
}

func TestV5_Collections(t *testing.T) {
	u1 := User{
		ID:    1,
		Roles: []string{"user"},
		Score: map[string]int{"a": 10},
	}
	u2 := User{
		ID:    1,
		Roles: []string{"user", "admin"},
		Score: map[string]int{"a": 10, "b": 20},
	}

	// 1. Diff
	patch := Diff(u1, u2)

	// Should have 2 operations (one for Roles add, one for Score add)
	// v4 Diff produces specific slice/map ops
	rolesFound := false
	scoreFound := false
	for _, op := range patch.Operations {
		if strings.HasPrefix(op.Path, "/roles") {
			rolesFound = true
		}
		if strings.HasPrefix(op.Path, "/score") {
			scoreFound = true
		}
	}
	if !rolesFound || !scoreFound {
		t.Fatalf("collections ops not found: roles=%v, score=%v", rolesFound, scoreFound)
	}

	// 2. Apply (fallback to reflection for collection sub-paths)
	u3 := u1
	if err := Apply(&u3, patch); err != nil {
		t.Fatalf("Apply failed: %v", err)
	}

	if len(u3.Roles) != 2 || u3.Roles[1] != "admin" {
		t.Errorf("Roles failed: %v", u3.Roles)
	}
	if u3.Score["b"] != 20 {
		t.Errorf("Score failed: %v", u3.Score)
	}
}

func TestV5_ComplexBuilder(t *testing.T) {
	u1 := User{
		ID:    1,
		Name:  "Alice",
		Roles: []string{"user"},
		Score: map[string]int{"a": 10},
	}

	builder := Edit(&u1)
	Set(builder, Field(func(u *User) *string { return &u.Name }), "Alice Smith")
	Set(builder, Field(func(u *User) *int { return &u.Info.Age }), 35)
	Add(builder, Field(func(u *User) *[]string { return &u.Roles }).Index(1), "admin")
	Set(builder, Field(func(u *User) *map[string]int { return &u.Score }).Key("b"), 20)
	Remove(builder, Field(func(u *User) *map[string]int { return &u.Score }).Key("a"))

	patch := builder.Build()

	u2 := u1
	if err := Apply(&u2, patch); err != nil {
		t.Fatalf("Apply failed: %v", err)
	}

	if u2.Name != "Alice Smith" {
		t.Errorf("Name failed: %s", u2.Name)
	}
	if u2.Info.Age != 35 {
		t.Errorf("Age failed: %d", u2.Info.Age)
	}
	if len(u2.Roles) != 2 || u2.Roles[1] != "admin" {
		t.Errorf("Roles failed: %v", u2.Roles)
	}
	if u2.Score["b"] != 20 {
		t.Errorf("Score failed: %v", u2.Score)
	}
	if _, ok := u2.Score["a"]; ok {
		t.Errorf("Score 'a' should have been removed")
	}
}

func TestV5_Text(t *testing.T) {
	u1 := User{
		Bio: Text{{Value: "Hello"}},
	}
	u2 := User{
		Bio: Text{{Value: "Hello World"}},
	}

	// 1. Diff
	patch := Diff(u1, u2)
	found := false
	for _, op := range patch.Operations {
		if op.Path == "/bio" {
			found = true
		}
	}
	if !found {
		t.Fatal("/bio op not found")
	}

	// 2. Apply
	u3 := u1
	if err := Apply(&u3, patch); err != nil {
		t.Fatalf("Apply failed: %v", err)
	}

	if u3.Bio.String() != "Hello World" {
		t.Errorf("got %s, want Hello World", u3.Bio.String())
	}
}

func TestV5_Unexported(t *testing.T) {
	// Note: We access 'age' via a helper or just check it if we are in the same package
	u1 := User{ID: 1, age: 30}
	u2 := User{ID: 1, age: 31}

	// 1. Diff (should pick up unexported 'age')
	patch := Diff(u1, u2)
	found := false
	for _, op := range patch.Operations {
		if op.Path == "/age" {
			found = true
			if op.New != 31 {
				t.Errorf("expected 31, got %v", op.New)
			}
		}
	}
	if !found {
		t.Fatal("unexported operation /age not found")
	}

	// 2. Apply
	u3 := u1
	if err := Apply(&u3, patch); err != nil {
		t.Fatalf("Apply failed: %v", err)
	}

	if u3.age != 31 {
		t.Errorf("got %d, want 31", u3.age)
	}
}

func TestV5_Conditions(t *testing.T) {
	u1 := User{ID: 1, Name: "Alice"}

	// 1. Global condition fails
	p1 := NewPatch[User]()
	p1.Condition = Eq(Field(func(u *User) *string { return &u.Name }), "Bob")
	p1.Operations = []Operation{{Kind: OpReplace, Path: "/full_name", New: "Alice Smith"}}

	if err := Apply(&u1, p1); err == nil || !strings.Contains(err.Error(), "condition not met") {
		t.Errorf("expected global condition failure, got %v", err)
	}

	// 2. Per-op condition
	builder := Edit(&u1)
	Set(builder, Field(func(u *User) *string { return &u.Name }), "Alice Smith").
		If(Eq(Field(func(u *User) *int { return &u.ID }), 1))
	Set(builder, Field(func(u *User) *int { return &u.ID }), 2).
		If(Eq(Field(func(u *User) *string { return &u.Name }), "Bob")) // Should fail

	p2 := builder.Build()
	u2 := u1
	Apply(&u2, p2)

	if u2.Name != "Alice Smith" {
		t.Errorf("Name should have changed")
	}
	if u2.ID != 1 {
		t.Errorf("ID should NOT have changed")
	}
}

func TestV5_Reverse(t *testing.T) {
	u1 := User{ID: 1, Name: "Alice"}
	u2 := User{ID: 2, Name: "Bob"}

	// 1. Create patch u1 -> u2
	patch := Diff(u1, u2)

	// 2. Reverse patch
	reverse := patch.Reverse()

	// 3. Apply reverse to u2
	u3 := u2
	if err := Apply(&u3, reverse); err != nil {
		t.Fatalf("Reverse apply failed: %v", err)
	}

	// 4. Result should be u1
	// Note: Diff might pick up Name as /full_name and ID as /id or /ID depending on tags
	// But Equal should verify logical equality.
	if !Equal(u1, u3) {
		t.Errorf("Reverse failed: got %+v, want %+v", u3, u1)
	}
}

func TestV5_Reverse_Complex(t *testing.T) {
	// 1. Generated Path (User has generated code)
	u1 := User{
		ID:    1,
		Name:  "Alice",
		Info:  Detail{Age: 30, Address: "123 Main"},
		Roles: []string{"admin", "user"},
		Score: map[string]int{"games": 10},
		Bio:   Text{{Value: "Initial"}},
		age:   30,
	}
	u2 := User{
		ID:    2,
		Name:  "Bob",
		Info:  Detail{Age: 31, Address: "456 Side"},
		Roles: []string{"user"},
		Score: map[string]int{"games": 20, "win": 1},
		Bio:   Text{{Value: "Updated"}},
		age:   31,
	}

	t.Run("GeneratedPath", func(t *testing.T) {
		patch := Diff(u1, u2)
		reverse := patch.Reverse()
		u3 := u2
		if err := Apply(&u3, reverse); err != nil {
			t.Fatalf("Reverse apply failed: %v", err)
		}
		// Use reflect.DeepEqual since we want exact parity including unexported fields
		// and we are in the same package.
		if !reflect.DeepEqual(u1, u3) {
			t.Errorf("Reverse failed\nGot:  %+v\nWant: %+v", u3, u1)
		}
	})

	t.Run("ReflectionPath", func(t *testing.T) {
		type OtherDetail struct {
			City string
		}
		type OtherUser struct {
			ID   int
			Data OtherDetail
		}
		o1 := OtherUser{ID: 1, Data: OtherDetail{City: "NY"}}
		o2 := OtherUser{ID: 2, Data: OtherDetail{City: "SF"}}

		patch := Diff(o1, o2) // Uses reflection
		reverse := patch.Reverse()
		o3 := o2
		if err := Apply(&o3, reverse); err != nil {
			t.Fatalf("Reverse apply failed: %v", err)
		}
		if !reflect.DeepEqual(o1, o3) {
			t.Errorf("Reverse failed\nGot:  %+v\nWant: %+v", o3, o1)
		}
	})
}

func TestV5_JSONPatch(t *testing.T) {
	u := User{ID: 1, Name: "Alice"}

	builder := Edit(&u)
	Set(builder, Field(func(u *User) *string { return &u.Name }), "Bob").
		If(In(Field(func(u *User) *int { return &u.ID }), []int{1, 2, 3}))

	patch := builder.Build()

	data, err := patch.ToJSONPatch()
	if err != nil {
		t.Fatalf("ToJSONPatch failed: %v", err)
	}

	// Verify JSON structure matches github.com/brunoga/jsonpatch expectations
	var raw []map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("JSON invalid: %v", err)
	}

	if len(raw) != 1 {
		t.Fatalf("expected 1 op, got %d", len(raw))
	}

	op := raw[0]
	if op["op"] != "replace" {
		t.Errorf("expected op=replace, got %v", op["op"])
	}

	cond := op["if"].(map[string]any)
	if cond["op"] != "contains" {
		t.Errorf("expected if.op=contains, got %v", cond["op"])
	}

	t.Logf("Generated JSON Patch: %s", string(data))
}

func TestV5_JSONPatch_GlobalCondition(t *testing.T) {
	p := NewPatch[User]()
	p.Condition = Eq(Field(func(u *User) *int { return &u.ID }), 1)
	p.Operations = []Operation{{Kind: OpReplace, Path: "/full_name", New: "Bob"}}

	data, err := p.ToJSONPatch()
	if err != nil {
		t.Fatalf("ToJSONPatch failed: %v", err)
	}

	var raw []map[string]any
	json.Unmarshal(data, &raw)

	if len(raw) != 2 {
		t.Fatalf("expected 2 ops (global condition + replace), got %d", len(raw))
	}

	if raw[0]["op"] != "test" {
		t.Errorf("expected first op to be test (global condition), got %v", raw[0]["op"])
	}
}

func TestV5_Log(t *testing.T) {
	u := User{ID: 1, Name: "Alice"}

	builder := Edit(&u)
	builder.Log("Starting update")
	Set(builder, Field(func(u *User) *string { return &u.Name }), "Bob").
		If(Log(Field(func(u *User) *int { return &u.ID }), "Checking ID"))
	builder.Log("Finished update")

	p := builder.Build()
	Apply(&u, p)
}

func TestV5_LogicalConditions(t *testing.T) {
	u := User{ID: 1, Name: "Alice"}

	p1 := NewPatch[User]()
	p1.Condition = And(
		Eq(Field(func(u *User) *int { return &u.ID }), 1),
		Eq(Field(func(u *User) *string { return &u.Name }), "Alice"),
	)
	p1.Operations = []Operation{{Kind: OpReplace, Path: "/full_name", New: "Alice OK"}}

	if err := Apply(&u, p1); err != nil {
		t.Errorf("And condition failed: %v", err)
	}

	p2 := NewPatch[User]()
	p2.Condition = Not(Eq(Field(func(u *User) *int { return &u.ID }), 1))
	p2.Operations = []Operation{{Kind: OpReplace, Path: "/full_name", New: "Alice NOT"}}

	if err := Apply(&u, p2); err == nil {
		t.Error("Not condition should have failed")
	}
}

func TestV5_StructTags(t *testing.T) {
	type TaggedUser struct {
		ID       int    `json:"id"`
		Secret   string `deep:"-"`
		ReadOnly string `deep:"readonly"`
		Config   Detail `deep:"atomic"`
	}

	u1 := TaggedUser{ID: 1, Secret: "hidden", ReadOnly: "locked", Config: Detail{Age: 10}}
	u2 := TaggedUser{ID: 1, Secret: "visible", ReadOnly: "changed", Config: Detail{Age: 20}}

	t.Run("IgnoreAndReadOnly", func(t *testing.T) {
		patch := Diff(u1, u2) // Secret should be ignored, ReadOnly should be picked up by Diff
		for _, op := range patch.Operations {
			if op.Path == "/Secret" {
				t.Error("Secret field should have been ignored by Diff")
			}
		}

		u3 := u1
		err := Apply(&u3, patch)
		if err == nil || !strings.Contains(err.Error(), "read-only") {
			t.Errorf("Apply should have failed for read-only field, got: %v", err)
		}
	})
}

func TestV5_AdvancedConditions(t *testing.T) {
	u := User{ID: 1, Name: "Alice"}

	t.Run("Matches", func(t *testing.T) {
		p := NewPatch[User]()
		p.Condition = Matches(Field(func(u *User) *string { return &u.Name }), "^Ali.*$")
		p.Operations = []Operation{{Kind: OpReplace, Path: "/full_name", New: "Alice Regex"}}
		if err := Apply(&u, p); err != nil {
			t.Errorf("Matches failed: %v", err)
		}
	})

	t.Run("Type", func(t *testing.T) {
		p := NewPatch[User]()
		p.Condition = Type(Field(func(u *User) *int { return &u.ID }), "number")
		p.Operations = []Operation{{Kind: OpReplace, Path: "/full_name", New: "Alice Type"}}
		if err := Apply(&u, p); err != nil {
			t.Errorf("Type failed: %v", err)
		}
	})
}

func BenchmarkV4_DiffApply(b *testing.B) {
	u1 := User{ID: 1, Name: "Alice"}
	u2 := User{ID: 1, Name: "Bob"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p, _ := engine.Diff(u1, u2)
		u3 := u1
		p.Apply(&u3)
	}
}

func BenchmarkV5_DiffApply(b *testing.B) {
	u1 := User{ID: 1, Name: "Alice"}
	u2 := User{ID: 1, Name: "Bob"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p := Diff(u1, u2)
		u3 := u1
		Apply(&u3, p)
	}
}

func BenchmarkV4_ApplyOnly(b *testing.B) {
	u1 := User{ID: 1, Name: "Alice"}
	u2 := User{ID: 1, Name: "Bob"}
	p, _ := engine.Diff(u1, u2)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		u3 := u1
		p.Apply(&u3)
	}
}

func BenchmarkV5_ApplyOnly(b *testing.B) {
	u1 := User{ID: 1, Name: "Alice"}
	u2 := User{ID: 1, Name: "Bob"}
	p := Diff(u1, u2)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		u3 := u1
		Apply(&u3, p)
	}
}
