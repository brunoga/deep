//go:generate go run github.com/brunoga/deep/v6/cmd/deep-gen -type=Account -output account_deep.go .

package main

import (
	"fmt"
	"log"

	"github.com/brunoga/deep/v6"
)

// A patch-level Guard is all-or-nothing: if it fails, nothing applies (see the
// policy_engine example). Per-operation conditions are the opposite — each
// operation decides for itself, so one patch can carry a mix of unconditional
// and conditional changes.
type Account struct {
	Owner    string  `json:"owner"`
	Balance  float64 `json:"balance"`
	LateFee  float64 `json:"late_fee"`
	Status   string  `json:"status"`
	LastSeen string  `json:"last_seen"`
}

func main() {
	balancePath := deep.Field(func(a *Account) *float64 { return &a.Balance })
	feePath := deep.Field(func(a *Account) *float64 { return &a.LateFee })
	statusPath := deep.Field(func(a *Account) *string { return &a.Status })
	seenPath := deep.Field(func(a *Account) *string { return &a.LastSeen })

	// One nightly patch for every account:
	//   - always stamp the last-seen date
	//   - charge a late fee only if a balance is outstanding
	//   - suspend only if the debt is serious
	nightly := deep.NewPatch[Account]().
		With(deep.Set(seenPath, "2026-09-01")).
		With(deep.Set(feePath, 25.0).If(deep.Gt(balancePath, 0.0))).
		With(deep.Set(statusPath, "suspended").If(deep.Gt(balancePath, 500.0))).
		With(deep.Set(statusPath, "active").Unless(deep.Gt(balancePath, 0.0))).
		Build()

	accounts := []Account{
		{Owner: "Alice", Balance: 0, Status: "active"},
		{Owner: "Bob", Balance: 120, Status: "active"},
		{Owner: "Carol", Balance: 900, Status: "active"},
	}

	fmt.Println("--- NIGHTLY RUN (one patch, three accounts) ---")
	for i := range accounts {
		if err := deep.Apply(&accounts[i], nightly); err != nil {
			log.Fatal(err)
		}
		a := accounts[i]
		fmt.Printf("  %-6s balance=%6.2f fee=%5.2f status=%-9s last_seen=%s\n",
			a.Owner, a.Balance, a.LateFee, a.Status, a.LastSeen)
	}

	fmt.Println("\nEvery account got a last_seen stamp; the fee and status")
	fmt.Println("operations applied only where their conditions held.")
}
