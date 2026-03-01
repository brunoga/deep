package main

import (
	"fmt"
	v5 "github.com/brunoga/deep/v5"
)

type User struct {
	Name  string   `json:"name"`
	Email string   `json:"email"`
	Roles []string `json:"roles"`
}

func main() {
	u1 := User{
		Name:  "Alice",
		Email: "alice@example.com",
		Roles: []string{"user"},
	}

	// Create a patch using the type-safe builder
	builder := v5.Edit(&u1)
	v5.Set(builder, v5.Field(func(u *User) *string { return &u.Name }), "Alice Smith")
	v5.Set(builder, v5.Field(func(u *User) *string { return &u.Email }), "alice.smith@example.com")
	v5.Add(builder, v5.Field(func(u *User) *[]string { return &u.Roles }).Index(1), "admin")

	patch := builder.Build()

	fmt.Println("AUDIT LOG (v5):")
	fmt.Println("----------")
	for _, op := range patch.Operations {
		switch op.Kind {
		case v5.OpReplace:
			fmt.Printf("Modified field '%s': %v -> %v\n", op.Path, op.Old, op.New)
		case v5.OpAdd:
			fmt.Printf("Set new field '%s': %v\n", op.Path, op.New)
		}
	}
}
