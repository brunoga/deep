package main

import (
	"fmt"
	"log"

	"github.com/brunoga/deep/v6"
)

// Move relocates a value between paths — reading the source, clearing it, and
// writing the destination — while Copy duplicates it, leaving the source in
// place. Both are single operations, so they travel in a patch like any other.
type Document struct {
	Draft     string `json:"draft"`
	Published string `json:"published"`
	Archived  string `json:"archived"`
}

func main() {
	doc := Document{Draft: "My Article"}

	draftPath := deep.Field(func(d *Document) *string { return &d.Draft })
	pubPath := deep.Field(func(d *Document) *string { return &d.Published })
	archivePath := deep.Field(func(d *Document) *string { return &d.Archived })

	fmt.Println("--- BEFORE ---")
	fmt.Printf("draft=%q published=%q archived=%q\n", doc.Draft, doc.Published, doc.Archived)

	// Publish: move the draft into the published slot, clearing the draft.
	publish := deep.NewPatch[Document]().With(deep.Move(draftPath, pubPath)).Build()

	fmt.Println("\n--- PUBLISH (move /draft -> /published) ---")
	fmt.Println(publish)
	if err := deep.Apply(&doc, publish); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("draft=%q published=%q archived=%q\n", doc.Draft, doc.Published, doc.Archived)

	// Archive: copy the published text, keeping it live as well.
	archive := deep.NewPatch[Document]().With(deep.Copy(pubPath, archivePath)).Build()

	fmt.Println("\n--- ARCHIVE (copy /published -> /archived) ---")
	fmt.Println(archive)
	if err := deep.Apply(&doc, archive); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("draft=%q published=%q archived=%q\n", doc.Draft, doc.Published, doc.Archived)

	fmt.Println("\nMove empties the source; Copy leaves it intact.")
}
