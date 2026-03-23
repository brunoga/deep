package main

import (
	"fmt"

	"github.com/brunoga/deep/v5"
)

type SystemConfig struct {
	AppName    string            `json:"app"`
	MaxThreads int               `json:"threads"`
	Endpoints  map[string]string `json:"endpoints"`
}

type Resolver struct{}

func (r *Resolver) Resolve(path string, local, remote any) any {
	fmt.Printf("- conflict at %s: %v vs %v (picking remote)\n", path, local, remote)
	return remote
}

func main() {
	base := SystemConfig{
		AppName:    "CoreAPI",
		MaxThreads: 10,
		Endpoints:  map[string]string{"auth": "https://auth.local"},
	}

	// User A changes Endpoints/auth.
	patchA := deep.Patch[SystemConfig]{}
	patchA.Operations = append(patchA.Operations, deep.Operation{
		Kind: deep.OpReplace, Path: "/endpoints/auth", New: "https://auth.internal",
	})

	// User B also changes Endpoints/auth — conflict.
	patchB := deep.Patch[SystemConfig]{}
	patchB.Operations = append(patchB.Operations, deep.Operation{
		Kind: deep.OpReplace, Path: "/endpoints/auth", New: "https://auth.remote",
	})

	fmt.Println("--- BASE STATE ---")
	fmt.Printf("%+v\n", base)

	fmt.Println("\n--- MERGING PATCHES (Custom Resolution) ---")
	merged := deep.Merge(patchA, patchB, &Resolver{})

	final := base
	deep.Apply(&final, merged)

	fmt.Println("\n--- FINAL STATE ---")
	fmt.Printf("%+v\n", final)
}
