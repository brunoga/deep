package main

import (
	"fmt"
	"log"

	"github.com/brunoga/deep/v5"
)

type Stock struct {
	SKU      string `json:"sku"`
	Quantity int    `json:"q"`
}

func main() {
	s := Stock{SKU: "BOLT-1", Quantity: 100}

	// User A generates a patch to decrease stock by 10.
	// WithStrict(true) records current values so the patch fails if the
	// state has changed by the time it is applied (optimistic locking).
	rawPatch, err := deep.Diff(s, Stock{SKU: "BOLT-1", Quantity: 90})
	if err != nil {
		log.Fatal(err)
	}
	patchA := rawPatch.WithStrict(true)

	// User B concurrently updates stock to 50.
	s.Quantity = 50

	fmt.Println("--- INITIAL STATE ---")
	fmt.Printf("Stock: %+v (User B set quantity to 50)\n", s)

	// User A's patch was generated when quantity was 100 — it should be rejected.
	fmt.Println("\n--- APPLYING STALE PATCH ---")
	if err = deep.Apply(&s, patchA); err != nil {
		fmt.Printf("REJECTED (optimistic lock): %v\n", err)
	} else {
		fmt.Printf("Applied: new quantity = %d\n", s.Quantity)
	}
}
