package main

import (
	"fmt"

	v5 "github.com/brunoga/deep/v5"
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
	policy := v5.Or(
		v5.And(
			v5.Eq(v5.Field(func(e *Employee) *string { return &e.Role }), "Junior"),
			v5.Eq(v5.Field(func(e *Employee) *int { return &e.Rating }), 5),
		),
		v5.Matches(v5.Field(func(e *Employee) *string { return &e.Name }), ".*Superstar$"),
	)

	patch := v5.NewPatch[Employee]().WithGuard(policy).WithStrict(false)
	patch.Operations = append(patch.Operations, v5.Operation{
		Kind: v5.OpReplace, Path: "/role", New: "Senior",
	})

	fmt.Println("--- PROMOTION ATTEMPT (rating=5) ---")
	fmt.Printf("Employee: %+v\n", e)
	if err := v5.Apply(&e, patch); err != nil {
		fmt.Printf("REJECTED: %v\n", err)
	} else {
		fmt.Printf("ACCEPTED: new role = %s\n", e.Role)
	}

	e.Rating = 3
	fmt.Println("\n--- PROMOTION ATTEMPT (rating=3) ---")
	fmt.Printf("Employee: %+v\n", e)
	if err := v5.Apply(&e, patch); err != nil {
		fmt.Printf("REJECTED: %v\n", err)
	} else {
		fmt.Printf("ACCEPTED: new role = %s\n", e.Role)
	}
}
