//go:generate go run github.com/brunoga/deep/v5/cmd/deep-gen -type=User .

package main

import (
	"fmt"
	"log"

	v5 "github.com/brunoga/deep/v5"
)

type User struct {
	Name  string          `json:"name"`
	Email string          `json:"email"`
	Tags  map[string]bool `json:"tags"`
}

func main() {
	u1 := User{
		Name:  "Alice",
		Email: "alice@example.com",
		Tags:  map[string]bool{"user": true},
	}

	u2 := User{
		Name:  "Alice Smith",
		Email: "alice.smith@example.com",
		Tags:  map[string]bool{"user": true, "admin": true},
	}

	// Diff captures old and new values for every changed field.
	patch, err := v5.Diff(u1, u2)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("--- AUDIT LOG ---")
	for _, op := range patch.Operations {
		switch op.Kind {
		case v5.OpReplace:
			fmt.Printf("  MODIFY  %s: %v → %v\n", op.Path, op.Old, op.New)
		case v5.OpAdd:
			fmt.Printf("  ADD     %s: %v\n", op.Path, op.New)
		case v5.OpRemove:
			fmt.Printf("  REMOVE  %s: %v\n", op.Path, op.Old)
		}
	}
}
