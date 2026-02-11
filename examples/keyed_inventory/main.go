package main

import (
	"fmt"

	"github.com/brunoga/deep/v2"
)

// Product represents an item in an inventory.
type Product struct {
	// The SKU is the unique identity of this product.
	// By tagging it with `deep:"key"`, we tell the library to use this field
	// to align slices instead of using indices.
	SKU      string `deep:"key"`
	Quantity int
	Price    float64
}

func main() {
	// 1. Initial inventory (A list of products).
	inventoryA := []Product{
		{SKU: "P1", Quantity: 10, Price: 19.99},
		{SKU: "P2", Quantity: 5, Price: 29.99},
		{SKU: "P3", Quantity: 100, Price: 5.99},
	}

	// 2. New inventory state:
	// - P2 moved to the front.
	// - P1 had a price change and quantity change.
	// - P3 was removed.
	// - P4 was added.
	inventoryB := []Product{
		{SKU: "P2", Quantity: 5, Price: 29.99},
		{SKU: "P1", Quantity: 8, Price: 17.99},
		{SKU: "P4", Quantity: 50, Price: 9.99},
	}

	// 3. Diff the inventories.
	// Because of the `deep:"key"` tag, this diff will be SEMANTIC.
	// It will understand that P1 is the same object even though it's at index 1 now.
	patch := deep.Diff(inventoryA, inventoryB)

	// 4. Use the Walk API to see the semantic operations.
	fmt.Println("INVENTORY UPDATE PLAN:")
	fmt.Println("----------------------")

	_ = patch.Walk(func(path string, op deep.OpKind, old, new any) error {
		switch op {
		case deep.OpReplace:
			fmt.Printf("[Update] Item '%s' value changed: %v -> %v\n", path, old, new)
		case deep.OpAdd:
			fmt.Printf("[Add] New item at '%s': %+v\n", path, new)
		case deep.OpRemove:
			fmt.Printf("[Remove] Item at '%s' (Value was: %+v)\n", path, old)
		}
		return nil
	})

	// 5. Apply the update.
	deep.Diff(inventoryA, inventoryB).Apply(&inventoryA)

	fmt.Printf("\nFinal Inventory: %+v\n", inventoryA)
}
