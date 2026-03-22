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

	patch := deep.Edit(&c1).
		With(deep.Set(deep.Field(func(c *Config) *string { return &c.Theme }), "light")).
		Build()

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

	namePath := deep.Field(func(u *testmodels.User) *string { return &u.Name })
	agePath := deep.Field(func(u *testmodels.User) *int { return &u.Info.Age })
	rolesPath := deep.Field(func(u *testmodels.User) *[]string { return &u.Roles })
	scorePath := deep.Field(func(u *testmodels.User) *map[string]int { return &u.Score })

	patch := deep.Edit(&u1).
		With(
			deep.Set(namePath, "Alice Smith"),
			deep.Set(agePath, 35),
			deep.Add(rolesPath.Index(1), "admin"),
			deep.Set(scorePath.Key("b"), 20),
			deep.Remove(scorePath.Key("a")),
		).
		Build()

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

	namePath := deep.Field(func(u *testmodels.User) *string { return &u.Name })

	p := deep.Edit(&u).
		Log("Starting update").
		With(deep.Set(namePath, "Bob")).
		Log("Finished update").
		Build()

	deep.Apply(&u, p)
}

func TestBuilderAdvanced(t *testing.T) {
	u := &testmodels.User{}
	idPath := deep.Field(func(u *testmodels.User) *int { return &u.ID })
	namePath := deep.Field(func(u *testmodels.User) *string { return &u.Name })

	p := deep.Edit(u).
		Where(deep.Eq(idPath, 1)).
		With(
			deep.Set(idPath, 2).Unless(deep.Eq(idPath, 1)),
		).
		Build()

	_ = deep.Gt(idPath, 0)
	_ = deep.Lt(idPath, 10)
	_ = deep.Exists(namePath)

	if p.Guard == nil || p.Guard.Op != "==" {
		t.Error("Where failed")
	}
}
