package main

import (
	"fmt"
	v5 "github.com/brunoga/deep/v5"
)

type Stock struct {
	SKU      string `json:"sku"`
	Quantity int    `json:"q"`
}

func main() {
	s := Stock{SKU: "BOLT-1", Quantity: 100}

	// 1. User A generates a patch to decrease stock by 10 (expects 100)
	patchA := v5.Diff(s, Stock{SKU: "BOLT-1", Quantity: 90}).WithStrict(true)

	// 2. User B concurrently updates stock to 50
	s.Quantity = 50
	fmt.Printf("Initial Stock: %+v (updated by User B to 50)\n", s)

	// 3. User A attempts to apply their patch
	fmt.Println("\nUser A attempting to apply patch (generated when quantity was 100)...")
	err := v5.Apply(&s, patchA)
	if err != nil {
		fmt.Printf("User A Update FAILED (Optimistic Lock): %v\n", err)
	} else {
		fmt.Printf("User A Update SUCCESS: New Quantity: %d\n", s.Quantity)
	}
}
