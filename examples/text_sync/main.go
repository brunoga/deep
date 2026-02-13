package main

import (
	"fmt"
	"strings"

	"github.com/brunoga/deep/v3/crdt"
)

// Document represents a text document.
type Document struct {
	Text []Char `deep:"key"`
}

// Char represents a single character in the document.
type Char struct {
	ID    string `deep:"key"`
	Value string
}

func (d Document) String() string {
	var b strings.Builder
	for _, c := range d.Text {
		b.WriteString(c.Value)
	}
	return b.String()
}

func main() {
	docA := crdt.NewCRDT(Document{Text: []Char{}}, "A")
	docB := crdt.NewCRDT(Document{Text: []Char{}}, "B")

	fmt.Println("--- Initial State: Empty ---")

	// User A types "Hello"
	fmt.Println("\n--- A types 'Hello' ---")
	deltaA1 := docA.Edit(func(d *Document) {
		d.Text = []Char{
			{ID: "A1", Value: "H"},
			{ID: "A2", Value: "e"},
			{ID: "A3", Value: "l"},
			{ID: "A4", Value: "l"},
			{ID: "A5", Value: "o"},
		}
	})
	fmt.Printf("Doc A: %s\n", docA.View())

	// B receives "Hello"
	docB.ApplyDelta(deltaA1)
	fmt.Printf("Doc B: %s\n", docB.View())

	// Concurrent Editing!
	fmt.Println("\n--- Concurrent Edits ---")
	fmt.Println("A appends ' World'")
	fmt.Println("B inserts '!' at index 5")

	// A appends " World"
	deltaA2 := docA.Edit(func(d *Document) {
		d.Text = append(d.Text,
			Char{ID: "A6", Value: " "},
			Char{ID: "A7", Value: "W"},
			Char{ID: "A8", Value: "o"},
			Char{ID: "A9", Value: "r"},
			Char{ID: "A10", Value: "l"},
			Char{ID: "A11", Value: "d"},
		)
	})

	// B inserts "!" at index 5
	deltaB1 := docB.Edit(func(d *Document) {
		temp := make([]Char, 0)
		temp = append(temp, d.Text[:5]...)
		temp = append(temp, Char{ID: "B1", Value: "!"})
		temp = append(temp, d.Text[5:]...)
		d.Text = temp
	})

	fmt.Printf("Doc A (local): %s\n", docA.View())
	fmt.Printf("Doc B (local): %s\n", docB.View())

	// Sync
	fmt.Println("\n--- Syncing ---")

	// A receives B's insertion
	docA.ApplyDelta(deltaB1)
	fmt.Printf("Doc A (after B): %s\n", docA.View())

	// B receives A's appending
	docB.ApplyDelta(deltaA2)
	fmt.Printf("Doc B (after A): %s\n", docB.View())

	if docA.View().String() == docB.View().String() {
		fmt.Println("SUCCESS: Documents converged!")
	} else {
		fmt.Println("FAILURE: Divergence!")
	}

	// More complex: Interleaved insertion
	fmt.Println("\n--- Interleaved Insertion (Yjs style) ---")

	// A inserts "X" after B1
	deltaA3 := docA.Edit(func(d *Document) {
		idx := -1
		for i, c := range d.Text {
			if c.ID == "B1" {
				idx = i
				break
			}
		}
		if idx != -1 {
			d.Text = append(d.Text[:idx+1], append([]Char{{ID: "A12", Value: "X"}}, d.Text[idx+1:]...)...)
		}
	})

	// B inserts Y after B1
	deltaB2 := docB.Edit(func(d *Document) {
		idx := -1
		for i, c := range d.Text {
			if c.ID == "B1" {
				idx = i
				break
			}
		}
		if idx != -1 {
			d.Text = append(d.Text[:idx+1], append([]Char{{ID: "B2", Value: "Y"}}, d.Text[idx+1:]...)...)
		}
	})

	docA.ApplyDelta(deltaB2)
	docB.ApplyDelta(deltaA3)

	fmt.Printf("Doc A: %s\n", docA.View())
	fmt.Printf("Doc B: %s\n", docB.View())

	if docA.View().String() == docB.View().String() {
		fmt.Println("SUCCESS: Converged (interleaved)!")
	} else {
		fmt.Println("FAILURE: Divergence!")
	}
}
