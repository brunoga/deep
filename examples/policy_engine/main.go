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

	// Policy: promote to "Senior" only if (role=="Junior" AND rating==5)
	// OR name ends with "Superstar".
	policy := deep.Or(
		deep.And(
			deep.Eq(deep.Field(func(e *Employee) *string { return &e.Role }), "Junior"),
			deep.Eq(deep.Field(func(e *Employee) *int { return &e.Rating }), 5),
		),
		deep.Matches(deep.Field(func(e *Employee) *string { return &e.Name }), ".*Superstar$"),
	)

	patch := deep.Patch[Employee]{}.WithGuard(policy)
	patch.Operations = append(patch.Operations, deep.Operation{
		Kind: deep.OpReplace, Path: "/role", New: "Senior",
	})

	fmt.Println("--- PROMOTION ATTEMPT (rating=5) ---")
	fmt.Printf("Employee: %+v\n", e)
	if err := deep.Apply(&e, patch); err != nil {
		fmt.Printf("REJECTED: %v\n", err)
	} else {
		fmt.Printf("ACCEPTED: new role = %s\n", e.Role)
	}

	e.Rating = 3
	fmt.Println("\n--- PROMOTION ATTEMPT (rating=3) ---")
	fmt.Printf("Employee: %+v\n", e)
	if err := deep.Apply(&e, patch); err != nil {
		fmt.Printf("REJECTED: %v\n", err)
	} else {
		fmt.Printf("ACCEPTED: new role = %s\n", e.Role)
	}
}
