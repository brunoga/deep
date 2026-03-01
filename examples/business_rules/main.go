package main

import (
	"fmt"
	v5 "github.com/brunoga/deep/v5"
)

type Account struct {
	ID      string `json:"id"`
	Balance int    `json:"balance"`
	Status  string `json:"status"`
}

func main() {
	acc := Account{ID: "ACC-123", Balance: 0, Status: "Pending"}

	fmt.Printf("Initial Account: %+v\n", acc)

	// In v5, validation can be integrated into the application logic
	// or handled via a specialized engine wrapper.

	patch := v5.Edit(&acc).
		Set(v5.Field(func(a *Account) *string { return &a.Status }), "Active").
		Build()

	fmt.Println("Attempting activation...")

	// Business Rule: Cannot activate with 0 balance
	if acc.Balance == 0 {
		fmt.Println("Update Rejected: activation requires non-zero balance")
	} else {
		v5.Apply(&acc, patch)
		fmt.Printf("Update Successful! New Status: %s\n", acc.Status)
	}

	acc.Balance = 100
	fmt.Printf("\nUpdated Account Balance: %+v\n", acc)
	fmt.Println("Attempting activation again...")

	if acc.Balance == 0 {
		fmt.Println("Update Rejected")
	} else {
		v5.Apply(&acc, patch)
		fmt.Printf("Update Successful! New Status: %s\n", acc.Status)
	}
}
