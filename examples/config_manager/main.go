package main

import (
	"encoding/json"
	"fmt"
	"log"

	"github.com/brunoga/deep/v5"
)

type Config struct {
	Version     int             `json:"version"`
	Environment string          `json:"env"`
	Timeout     int             `json:"timeout"`
	Features    map[string]bool `json:"features"`
}

func main() {
	v1 := Config{
		Version:     1,
		Environment: "production",
		Timeout:     30,
		Features:    map[string]bool{"billing": false},
	}

	// Propose changes on a deep copy so v1 is not mutated.
	v2 := deep.Clone(v1)
	v2.Version = 2
	v2.Timeout = 45
	v2.Features["billing"] = true

	patch, err := deep.Diff(v1, v2)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("--- PROPOSED CHANGES ---")
	fmt.Println(patch)

	// Apply to a copy of the live state.
	state := deep.Clone(v1)
	deep.Apply(&state, patch)
	fmt.Printf("--- SYNCHRONIZED (version %d) ---\n", state.Version)

	// Rollback using the patch's own reverse.
	rollback := patch.Reverse()
	deep.Apply(&state, rollback)

	fmt.Println("--- ROLLED BACK ---")
	out, _ := json.MarshalIndent(state, "", "  ")
	fmt.Println(string(out))
}
