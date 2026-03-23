package main

import (
	"fmt"
	"github.com/brunoga/deep/v5"
	"log"
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

	fmt.Println("--- INITIAL STATE ---")
	fmt.Printf("%+v\n", meta)

	// 1. Attempt to change the read-only field.
	p1 := deep.Patch[SystemMeta]{}
	p1.Operations = append(p1.Operations, deep.Operation{
		Kind: deep.OpReplace, Path: "/cid", New: "HACKED-CLUSTER",
	})

	fmt.Println("\n--- READ-ONLY ENFORCEMENT ---")
	if err := deep.Apply(&meta, p1); err != nil {
		fmt.Printf("REJECTED: %v\n", err)
	}

	// 2. Demonstrate atomic update: deep:"atomic" causes the entire Settings
	// block to be replaced as a unit rather than field-by-field.
	newSettings := ProxyConfig{Host: "proxy.internal", Port: 9000}
	p2, err := deep.Diff(meta, SystemMeta{ClusterID: meta.ClusterID, Settings: newSettings})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("\n--- ATOMIC SETTINGS UPDATE ---")
	fmt.Println(p2)

	deep.Apply(&meta, p2)
	fmt.Printf("Result: %+v\n", meta)
}
