package v5

import (
	"testing"
)

func TestBuilder(t *testing.T) {
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

func TestComplexBuilder(t *testing.T) {
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

func TestLog(t *testing.T) {
	u := User{ID: 1, Name: "Alice"}

	builder := Edit(&u)
	builder.Log("Starting update")
	Set(builder, Field(func(u *User) *string { return &u.Name }), "Bob").
		If(Log(Field(func(u *User) *int { return &u.ID }), "Checking ID"))
	builder.Log("Finished update")

	p := builder.Build()
	Apply(&u, p)
}
