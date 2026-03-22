package main

import (
	"fmt"
	"log"
	"github.com/brunoga/deep/v5"
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

	patch, err := deep.Diff(inv1, inv2)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("--- INVENTORY UPDATE ---")
	fmt.Println(patch)
}
