package main

import (
	"fmt"
	v5 "github.com/brunoga/deep/v5"
)

type StrictUser struct {
	Name string `json:"name"`
	Age  int    `json:"age"`
}

func main() {
	u := StrictUser{Name: "Alice", Age: 30}

	fmt.Printf("Initial User: %+v\n", u)

	// Create a patch that will fail multiple checks
	// In v5, the engine fallback (reflection) can handle these types.
	// But let's trigger real path errors.
	patch := v5.Patch[StrictUser]{
		Operations: []v5.Operation{
			{Kind: v5.OpReplace, Path: "/nonexistent", New: "fail"},
			{Kind: v5.OpReplace, Path: "/wrong_type", New: 123.456},
		},
	}

	fmt.Println("\nApplying patch with multiple invalid paths/types...")

	err := v5.Apply(&u, patch)
	if err != nil {
		fmt.Printf("Patch Application Failed with Multiple Errors:\n%v\n", err)
	}
}
