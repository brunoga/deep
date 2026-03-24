package main

import (
	"fmt"

	"github.com/brunoga/deep/v5"
)

type StrictUser struct {
	Name string `json:"name"`
	Age  int    `json:"age"`
}

func main() {
	u := StrictUser{Name: "Alice", Age: 30}

	fmt.Println("--- INITIAL STATE ---")
	fmt.Printf("%+v\n", u)

	// A patch with two operations referencing non-existent fields.
	// Apply collects all errors rather than stopping at the first.
	patch := deep.Patch[StrictUser]{
		Operations: []deep.Operation{
			{Kind: deep.OpReplace, Path: "/nonexistent", New: "fail"},
			{Kind: deep.OpReplace, Path: "/wrong_type", New: 123.456},
		},
	}

	fmt.Println("\n--- APPLY (invalid paths) ---")
	if err := deep.Apply(&u, patch); err != nil {
		fmt.Printf("ERRORS:\n%v\n", err)
	}
}
