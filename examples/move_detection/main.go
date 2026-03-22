package main

import (
	"fmt"
	"log"

	"github.com/brunoga/deep/v5"
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

	fmt.Println("--- BEFORE ---")
	fmt.Printf("Draft: %q  Published: %q\n", doc.Draft, doc.Published)

	// Build a Move patch: /draft → /published
	draftPath := deep.Field(func(d *Document) *string { return &d.Draft })
	pubPath := deep.Field(func(d *Document) *string { return &d.Published })

	patch := deep.Edit(&doc).Move(draftPath, pubPath).Build()

	fmt.Println("\n--- PATCH ---")
	fmt.Println(patch)

	if err := deep.Apply(&doc, patch); err != nil {
		log.Fatal(err)
	}

	fmt.Println("--- AFTER ---")
	fmt.Printf("Draft: %q  Published: %q\n", doc.Draft, doc.Published)
}
