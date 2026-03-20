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

	u2 := User{
		Name:  "Alice Smith",
		Email: "alice.smith@example.com",
		Roles: []string{"user", "admin"},
	}

	// Diff captures old and new values for every changed field.
	patch := v5.Diff(u1, u2)

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
