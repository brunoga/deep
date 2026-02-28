package main

import (
	"fmt"
	"github.com/brunoga/deep/v5"
)

type Employee struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	Role   string `json:"role"`
	Rating int    `json:"rating"`
}

func main() {
	e := Employee{ID: 101, Name: "John Doe", Role: "Junior", Rating: 5}

	fmt.Printf("Initial Employee: %+v\n", e)

	// Policy: Can only promote to "Senior" if current role is "Junior" AND rating is 5
	// OR if the name matches a "Superstar" pattern (just for regex demo).
	policy := v5.Or(
		v5.And(
			v5.Eq(v5.Field(func(e *Employee) *string { return &e.Role }), "Junior"),
			v5.Eq(v5.Field(func(e *Employee) *int { return &e.Rating }), 5),
		),
		v5.Matches(v5.Field(func(e *Employee) *string { return &e.Name }), ".*Superstar$"),
	)

	patch := v5.NewPatch[Employee]().
		WithCondition(policy).
		WithStrict(false)

	// Add operation manually
	patch.Operations = append(patch.Operations, v5.Operation{
		Kind: v5.OpReplace, Path: "/role", New: "Senior",
	})

	fmt.Println("\nAttempting promotion with policy...")
	if err := v5.Apply(&e, patch); err != nil {
		fmt.Printf("Policy Rejected: %v\n", err)
	} else {
		fmt.Printf("Policy Accepted! New Role: %s\n", e.Role)
	}

	// Change rating and try again
	e.Rating = 3
	fmt.Printf("\nRating downgraded to %d. Attempting promotion again...\n", e.Rating)
	if err := v5.Apply(&e, patch); err != nil {
		fmt.Printf("Policy Rejected: %v\n", err)
	} else {
		fmt.Printf("Policy Accepted! New Role: %s\n", e.Role)
	}
}
