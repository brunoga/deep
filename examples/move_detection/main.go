package main

import (
	"fmt"
	"log"

	v5 "github.com/brunoga/deep/v5"
)

// Document has a draft field and a published field.
// The Move operation promotes a draft to published atomically:
// it reads the source value, clears the source, and writes to the destination.
type Document struct {
	Draft     string `json:"draft"`
	Published string `json:"published"`
}

func main() {
	doc := Document{
		Draft:     "My Article",
		Published: "",
	}

	fmt.Printf("Before: %+v\n\n", doc)

	// Build a Move patch: /draft → /published
	draftPath := v5.Field(func(d *Document) *string { return &d.Draft })
	pubPath := v5.Field(func(d *Document) *string { return &d.Published })

	patch := v5.Edit(&doc).Move(draftPath, pubPath).Build()

	fmt.Printf("--- GENERATED PATCH ---\n%v\n\n", patch)

	if err := v5.Apply(&doc, patch); err != nil {
		log.Fatal(err)
	}

	fmt.Printf("After: %+v\n", doc)
}
