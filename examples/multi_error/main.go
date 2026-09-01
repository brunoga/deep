//go:generate go run github.com/brunoga/deep/v5/cmd/deep-gen -type=StrictUser -output strictuser_deep.go .

package main

import (
	"errors"
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

	// A patch mixing one valid operation with two broken ones: an unknown
	// path and a value of the wrong type. Apply does not stop at the first
	// failure — it applies what it can and collects every error.
	patch := deep.Patch[StrictUser]{
		Operations: []deep.Operation{
			{Kind: deep.OpReplace, Path: "/name", New: "Alice Smith"},
			{Kind: deep.OpReplace, Path: "/nonexistent", New: "fail"},
			{Kind: deep.OpReplace, Path: "/age", New: "not a number"},
		},
	}

	fmt.Println("\n--- APPLY (one valid, two broken operations) ---")
	err := deep.Apply(&u, patch)
	if err != nil {
		fmt.Printf("%v\n", err)
	}

	// The error is an *ApplyError; Unwrap exposes the individual failures so
	// callers can inspect or filter them.
	var applyErr *deep.ApplyError
	if errors.As(err, &applyErr) {
		fmt.Printf("\n--- INSPECTING %d FAILURES ---\n", len(applyErr.Unwrap()))
		for i, e := range applyErr.Unwrap() {
			fmt.Printf("  [%d] %v\n", i, e)
		}
	}

	// The valid operation still applied.
	fmt.Println("\n--- FINAL STATE ---")
	fmt.Printf("%+v (name updated, age untouched)\n", u)
}
