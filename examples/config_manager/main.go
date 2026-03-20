package main

import (
	"encoding/json"
	"fmt"
	v5 "github.com/brunoga/deep/v5"
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

	// 1. Propose Changes (deep-copy the map so v1 is not aliased by v2)
	v2 := v1
	v2.Features = make(map[string]bool, len(v1.Features))
	for k, val := range v1.Features {
		v2.Features[k] = val
	}
	v2.Version = 2
	v2.Timeout = 45
	v2.Features["billing"] = true

	patch := v5.Diff(v1, v2)

	fmt.Printf("[Version 2] PROPOSING %d CHANGES:\n%v\n", len(patch.Operations), patch)

	// 2. Synchronize (Apply) — copy the map so v1 is not aliased
	state := v1
	state.Features = make(map[string]bool, len(v1.Features))
	for k, val := range v1.Features {
		state.Features[k] = val
	}
	v5.Apply(&state, patch)
	fmt.Printf("System synchronized to Version %d\n", state.Version)

	// 3. Rollback using the patch's own reverse
	rollback := patch.Reverse()
	v5.Apply(&state, rollback)
	fmt.Printf("[ROLLBACK] System reverted to Version %d\n", state.Version)

	out, _ := json.MarshalIndent(state, "", "  ")
	fmt.Println(string(out))
}
