package main

import (
	"fmt"
	v5 "github.com/brunoga/deep/v5"
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
	inv2 := Inventory{
		Items: []Item{
			{SKU: "P2", Quantity: 5},
			{SKU: "P3", Quantity: 20},
		},
	}

	patch := v5.Diff(inv1, inv2)

	fmt.Printf("INVENTORY UPDATE (v5):\n%v\n", patch)
}
