package main

import (
	"fmt"
	v5 "github.com/brunoga/deep/v5"
)

type ProxyConfig struct {
	Host string `json:"host"`
	Port int    `json:"port"`
}

type SystemMeta struct {
	ClusterID string      `deep:"readonly" json:"cid"`
	Settings  ProxyConfig `deep:"atomic" json:"proxy"`
}

func main() {
	meta := SystemMeta{
		ClusterID: "PROD-US-1",
		Settings:  ProxyConfig{Host: "localhost", Port: 8080},
	}

	fmt.Printf("Initial Config: %+v\n", meta)

	// 1. Attempt to change Read-Only field
	p1 := v5.NewPatch[SystemMeta]()
	p1.Operations = append(p1.Operations, v5.Operation{
		Kind: v5.OpReplace, Path: "/cid", New: "HACKED-CLUSTER",
	})

	fmt.Println("\nAttempting to modify read-only ClusterID...")
	if err := v5.Apply(&meta, p1); err != nil {
		fmt.Printf("REJECTED: %v\n", err)
	}

	// 2. Demonstrate Atomic update
	// An atomic update means even if we only change one field in Settings,
	// Diff should produce a replacement of the WHOLE Settings block if we use it.
	// But let's show how Apply treats it.

	newSettings := ProxyConfig{Host: "proxy.internal", Port: 9000}
	p2 := v5.Diff(meta, SystemMeta{ClusterID: meta.ClusterID, Settings: newSettings})

	fmt.Printf("\nGenerated Patch for Settings (Atomic):\n%v\n", p2)

	v5.Apply(&meta, p2)
	fmt.Printf("Final Config: %+v\n", meta)
}
