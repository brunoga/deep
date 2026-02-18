package main

import (
	"fmt"
	"github.com/brunoga/deep/v4"
)

type UserProfile struct {
	Username string
	Age      int
	Email    string
}

func main() {
	// 1. Initial State
	user := UserProfile{
		Username: "alice",
		Age:      30,
		Email:    "alice@example.com",
	}

	fmt.Printf("Initial User: %+v\n\n", user)

	// 2. Propose a patch with multiple "strict" expectations that are wrong.
	// We'll use the Builder to create a patch that expects different values than what's there.
	builder := deep.NewPatchBuilder[UserProfile]()

	// Error 1: Wrong current age expectation
	builder.Field("Age").Set(25, 31) // Expects 25, but it's actually 30

	// Error 2: Wrong current email expectation
	builder.Field("Email").Set("wrong@example.com", "new@example.com")

	// Error 3: Add a condition that will also fail
	builder.Field("Username").Put("bob")
	// This condition will fail because Username is currently "alice"
	// We use builder.AddCondition which automatically finds the right node
	builder.AddCondition("Username == 'admin'")

	patch, err := builder.Build()
	if err != nil {
		fmt.Printf("Build failed: %v\n", err)
		return
	}

	// 3. Apply the patch
	fmt.Println("Applying patch with multiple conflicting expectations...")
	err = patch.ApplyChecked(&user)

	if err != nil {
		fmt.Printf("\nPatch Application Failed with Multiple Errors:\n")
		fmt.Println(err.Error())

		// We can also inspect the individual errors if needed
		if applyErr, ok := err.(*deep.ApplyError); ok {
			fmt.Printf("Individual error count: %d\n", len(applyErr.Errors()))
		}
	} else {
		fmt.Println("Patch applied successfully (unexpected!)")
	}
}
