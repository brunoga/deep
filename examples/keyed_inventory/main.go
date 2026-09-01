//go:generate go run github.com/brunoga/deep/v5/cmd/deep-gen -type=Inventory,Item -output inventory_deep.go .

package main

import (
	"fmt"
	"github.com/brunoga/deep/v5"
	"log"
)

type Item struct {
	SKU      string `deep:"key" json:"sku"`
	Quantity int    `json:"q"`
}

type Inventory struct {
	Items []Item `json:"items"`
}

func main() {
	inv1 := Inventory{
		Items: []Item{
			{SKU: "P1", Quantity: 10},
			{SKU: "P2", Quantity: 5},
		},
	}
	// P1 is gone, P2 moved to the front AND its quantity changed, P3 is new.
	inv2 := Inventory{
		Items: []Item{
			{SKU: "P2", Quantity: 7},
			{SKU: "P3", Quantity: 20},
		},
	}

	patch, err := deep.Diff(inv1, inv2)
	if err != nil {
		log.Fatal(err)
	}

	// Paths are keyed by SKU, not by index: reordering alone produces no
	// operations, and a changed element diffs down to the changed field.
	fmt.Println("--- INVENTORY UPDATE ---")
	fmt.Println(patch)

	if err := deep.Apply(&inv1, patch); err != nil {
		log.Fatal(err)
	}

	fmt.Println("\n--- AFTER APPLY ---")
	for _, item := range inv1.Items {
		fmt.Printf("  %s: %d\n", item.SKU, item.Quantity)
	}
	fmt.Printf("\nMatches target: %v\n", deep.Equal(inv1, inv2))
}
