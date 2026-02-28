package main

import (
	"fmt"
	"github.com/brunoga/deep/v5"
)

type Workspace struct {
	Drafts  []string          `json:"drafts"`
	Archive map[string]string `json:"archive"`
}

func main() {
	w1 := Workspace{
		Drafts:  []string{"Important Doc"},
		Archive: make(map[string]string),
	}

	// Move from Drafts[0] to Archive["v1"]
	w2 := Workspace{
		Drafts: []string{},
		Archive: map[string]string{
			"v1": "Important Doc",
		},
	}

	// Move detection is a high-level feature currently handled by reflection fallback
	patch := v5.Diff(w1, w2)

	fmt.Printf("--- GENERATED PATCH SUMMARY ---\n%v\n", patch)

	// Apply
	final := w1
	v5.Apply(&final, patch)

	fmt.Printf("\nFinal Workspace: %+v\n", final)
}
