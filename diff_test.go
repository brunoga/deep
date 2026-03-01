package deep_test

import (
	"github.com/brunoga/deep/v5"
	"github.com/brunoga/deep/v5/internal/testmodels"

	"testing"
)

func TestBuilder(t *testing.T) {
	type Config struct {
		Theme string `json:"theme"`
	}

	c1 := Config{Theme: "dark"}

	builder := deep.Edit(&c1)
	deep.Set(builder, deep.Field(func(c *Config) *string { return &c.Theme }), "light")
	patch := builder.Build()

	if err := deep.Apply(&c1, patch); err != nil {
		t.Fatalf("deep.Apply failed: %v", err)
	}

	if c1.Theme != "light" {
		t.Errorf("got %s, want light", c1.Theme)
	}
}

func TestComplexBuilder(t *testing.T) {
	u1 := testmodels.User{
		ID:    1,
		Name:  "Alice",
		Roles: []string{"user"},
		Score: map[string]int{"a": 10},
	}

	builder := deep.Edit(&u1)
	deep.Set(builder, deep.Field(func(u *testmodels.User) *string { return &u.Name }), "Alice Smith")
	deep.Set(builder, deep.Field(func(u *testmodels.User) *int { return &u.Info.Age }), 35)
	deep.Add(builder, deep.Field(func(u *testmodels.User) *[]string { return &u.Roles }).Index(1), "admin")
	deep.Set(builder, deep.Field(func(u *testmodels.User) *map[string]int { return &u.Score }).Key("b"), 20)
	deep.Remove(builder, deep.Field(func(u *testmodels.User) *map[string]int { return &u.Score }).Key("a"))

	patch := builder.Build()

	u2 := u1
	if err := deep.Apply(&u2, patch); err != nil {
		t.Fatalf("deep.Apply failed: %v", err)
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

func TestLog(t *testing.T) {
	u := testmodels.User{ID: 1, Name: "Alice"}

	builder := deep.Edit(&u)
	builder.Log("Starting update")
	deep.Set(builder, deep.Field(func(u *testmodels.User) *string { return &u.Name }), "Bob").
		If(deep.Log(deep.Field(func(u *testmodels.User) *int { return &u.ID }), "Checking ID"))
	builder.Log("Finished update")

	p := builder.Build()
	deep.Apply(&u, p)
}

func TestBuilderAdvanced(t *testing.T) {
	u := &testmodels.User{}
	b := deep.Edit(u).
		Where(deep.Eq(deep.Field(func(u *testmodels.User) *int { return &u.ID }), 1)).
		Unless(deep.Ne(deep.Field(func(u *testmodels.User) *string { return &u.Name }), "Alice"))

	deep.Set(b, deep.Field(func(u *testmodels.User) *int { return &u.ID }), 2).Unless(deep.Eq(deep.Field(func(u *testmodels.User) *int { return &u.ID }), 1))
	deep.Gt(deep.Field(func(u *testmodels.User) *int { return &u.ID }), 0)
	deep.Lt(deep.Field(func(u *testmodels.User) *int { return &u.ID }), 10)
	deep.Exists(deep.Field(func(u *testmodels.User) *string { return &u.Name }))

	p := b.Build()
	if p.Condition == nil || p.Condition.Op != "==" {
		t.Error("Where failed")
	}
}
