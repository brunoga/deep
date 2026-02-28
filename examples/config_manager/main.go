package main

import (
	"encoding/json"
	"fmt"
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

	// 1. Propose Changes
	v2 := v1
	v2.Version = 2
	v2.Timeout = 45
	v2.Features["billing"] = true

	patch := v5.Diff(v1, v2)

	fmt.Printf("[Version 2] PROPOSING %d CHANGES:\n%v\n", len(patch.Operations), patch)

	// 2. Synchronize (Apply)
	state := v1
	v5.Apply(&state, patch)
	fmt.Printf("System synchronized to Version %d\n", state.Version)

	// 3. Rollback
	// In v5, we can just Diff again to get the inverse if we have history
	rollback := v5.Diff(state, v1)
	v5.Apply(&state, rollback)
	fmt.Printf("[ROLLBACK] System reverted to Version %d\n", state.Version)

	out, _ := json.MarshalIndent(state, "", "  ")
	fmt.Println(string(out))
}
