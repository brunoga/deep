package v5

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"reflect"
	"testing"
)

func TestGobSerialization(t *testing.T) {
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

func TestReverse(t *testing.T) {
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

func TestReverse_Complex(t *testing.T) {
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

func TestJSONPatch(t *testing.T) {
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

func TestJSONPatch_GlobalCondition(t *testing.T) {
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
