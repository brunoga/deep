package main

import (
	"fmt"
	"strings"

	"github.com/brunoga/deep/v2"
)

// User represents a typical user profile in a system.
type User struct {
	ID    int
	Name  string
	Email string
	Roles []string
}

func main() {
	// 1. Initial state of the user.
	userA := User{
		ID:    1,
		Name:  "Alice",
		Email: "alice@example.com",
		Roles: []string{"user"},
	}

	// 2. Modified state of the user.
	// We've changed the name, email, and added a role.
	userB := User{
		ID:    1,
		Name:  "Alice Smith",
		Email: "alice.smith@example.com",
		Roles: []string{"user", "admin"},
	}

	// 3. Generate a patch representing the difference.
	patch := deep.Diff(userA, userB)
	if patch == nil {
		fmt.Println("No changes detected.")
		return
	}

	// 4. Use the Walk API to generate an audit log.
	// This is much better than just printing the struct, as it tells us
	// exactly WHAT changed, from WHAT, to WHAT.
	fmt.Println("AUDIT LOG:")
	fmt.Println("----------")

	err := patch.Walk(func(path string, op deep.OpKind, old, new any) error {
		switch op {
		case deep.OpReplace:
			fmt.Printf("Modified field '%s': %v -> %v\n", path, old, new)
		case deep.OpAdd:
			// For slice elements, path will look like "Roles[1]"
			if strings.Contains(path, "[") {
				fmt.Printf("Added to list '%s': %v\n", path, new)
			} else {
				fmt.Printf("Set new field '%s': %v\n", path, new)
			}
		case deep.OpRemove:
			fmt.Printf("Removed field/item '%s' (was: %v)\n", path, old)
		}
		return nil
	})

	if err != nil {
		fmt.Printf("Error walking patch: %v\n", err)
	}

	// Output should look like:
	// AUDIT LOG:
	// ----------
	// Modified field 'Name': Alice -> Alice Smith
	// Modified field 'Email': alice@example.com -> alice.smith@example.com
	// Added to list 'Roles[1]': admin
}
