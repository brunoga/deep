package main

import (
	"fmt"

	"github.com/brunoga/deep/v3"
)

// Account represents a financial account.
type Account struct {
	ID      string
	Balance float64
	Status  string // "Pending", "Active", "Suspended"
}

func main() {
	// 1. Current state: An account with zero balance.
	acc := Account{
		ID:      "ACC-123",
		Balance: 0.0,
		Status:  "Pending",
	}

	// 2. We want to update the account status to "Active".
	// But we have a business rule: Status can only be "Active" IF balance > 0.

	// We use the Builder API to construct a conditional patch.
	builder := deep.NewPatchBuilder[Account]()

	// Set the new status
	builder.Field("Status").Set("Pending", "Active")

	// Attach a condition: ONLY apply this field update if Balance > 0.
	builder.AddCondition("Balance > 0.0")

	patch, err := builder.Build()
	if err != nil {
		fmt.Printf("Error building patch: %v\n", err)
		return
	}

	// 3. Attempt to apply the patch.
	fmt.Printf("Initial Account: %+v\n", acc)
	fmt.Println("Attempting activation with 0.0 balance...")

	err = patch.ApplyChecked(&acc)
	if err != nil {
		// This SHOULD fail because Balance is 0.0
		fmt.Printf("Update Rejected: %v\n", err)
	} else {
		fmt.Println("Update Successful!")
	}

	// 4. Now let's update the balance and try again.
	acc.Balance = 100.0
	fmt.Printf("\nUpdated Account Balance: %+v\n", acc)
	fmt.Println("Attempting activation with 100.0 balance...")

	err = patch.ApplyChecked(&acc)
	if err != nil {
		fmt.Printf("Update Rejected: %v\n", err)
	} else {
		// This SHOULD succeed now.
		fmt.Printf("Update Successful! New Status: %s\n", acc.Status)
	}
}
