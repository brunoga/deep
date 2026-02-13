package main

import (
	"fmt"
	"github.com/brunoga/deep/v3"
)

type SystemConfig struct {
	AppName    string
	MaxThreads int
	Debug      bool
	Endpoints  map[string]string
}

func main() {
	// 1. Common Base State
	base := SystemConfig{
		AppName:    "CoreAPI",
		MaxThreads: 10,
		Debug:      false,
		Endpoints:  map[string]string{"auth": "https://auth.local"},
	}

	fmt.Printf("--- BASE STATE ---\n%+v\n\n", base)

	// 2. User A: Changes AppName and adds an Endpoint
	userA := base
	userA.AppName = "CoreAPI-v2"
	// We must deep copy or re-initialize maps to avoid shared state in the example
	userA.Endpoints = map[string]string{
		"auth":    "https://auth.internal",
		"metrics": "https://metrics.local",
	}
	patchA := deep.Diff(base, userA)

	fmt.Println("--- PATCH A (User A) ---")
	fmt.Println(patchA.Summary())
	fmt.Println()

	// 3. User B: Changes MaxThreads and Debug mode
	userB := base
	userB.MaxThreads = 20
	userB.Debug = true
	userB.Endpoints = map[string]string{
		"auth": "https://auth.remote",
	}
	// Copy other endpoints from base to avoid accidental removal
	for k, v := range base.Endpoints {
		if _, ok := userB.Endpoints[k]; !ok {
			userB.Endpoints[k] = v
		}
	}
	patchB := deep.Diff(base, userB)

	fmt.Println("--- PATCH B (User B) ---")
	fmt.Println(patchB.Summary())
	fmt.Println()

	// 4. Merge Patch A and Patch B
	fmt.Println("--- MERGING PATCHES ---")
	merged, conflicts, err := deep.Merge(patchA, patchB)
	if err != nil {
		fmt.Printf("Merge failed error: %v\n", err)
		return
	}

	fmt.Printf("Merged Patch Summary:\n%s\n\n", merged.Summary())

	if len(conflicts) > 0 {
		fmt.Println("Detected Conflicts (A wins by default):")
		for _, c := range conflicts {
			fmt.Printf("- %s\n", c.String())
		}
		fmt.Println()
	}

	// 5. Apply Merged Patch to Base
	finalState := base
	err = merged.ApplyChecked(&finalState)
	if err != nil {
		fmt.Printf("Apply failed: %v\n", err)
	}

	fmt.Println("--- FINAL STATE (After Merge & Apply) ---")
	fmt.Printf("%+v\n", finalState)
}
