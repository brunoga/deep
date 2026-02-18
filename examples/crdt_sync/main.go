package main

import (
	"encoding/json"
	"fmt"

	"github.com/brunoga/deep/v3/crdt"
)

type Config struct {
	Title   string
	Options map[string]string
	Users   []User `deep:"key"`
}

type User struct {
	ID   int `deep:"key"`
	Name string
	Role string
}

func main() {
	initial := Config{
		Title:   "Global Config",
		Options: map[string]string{"theme": "light"},
		Users:   []User{{ID: 1, Name: "Alice", Role: "Admin"}},
	}

	nodeA := crdt.NewCRDT(initial, "node-a")
	nodeB := crdt.NewCRDT(initial, "node-b")

	fmt.Println("--- Initial State ---")
	printState(nodeA)

	fmt.Println("\n--- Node A Edits ---")
	deltaA := nodeA.Edit(func(c *Config) {
		c.Title = "Updated Title (A)"
		c.Options["font"] = "mono"
	})
	printState(nodeA)

	fmt.Println("\n--- Node B Edits (Concurrent) ---")
	nodeB.Edit(func(c *Config) {
		c.Title = "Final Title (B)"
		c.Users[0].Role = "SuperAdmin"
		c.Users = append(c.Users, User{ID: 2, Name: "Bob", Role: "User"})
	})
	printState(nodeB)

	fmt.Println("\n--- Syncing Node A -> Node B ---")
	if !nodeB.ApplyDelta(deltaA) {
		fmt.Println("ApplyDelta failed!")
		return
	}
	printState(nodeB)

	fmt.Println("\n--- Syncing Node B -> Node A (Full Merge) ---")
	if !nodeA.Merge(nodeB) {
		fmt.Println("Merge failed!")
		return
	}
	printState(nodeA)

	fmt.Println("\n--- Serialized State ---")
	data, err := json.MarshalIndent(nodeA, "", "  ")
	if err != nil {
		fmt.Printf("State serialization failed: %v\n", err)
		return
	}
	fmt.Println(string(data))
}

func printState[T any](c *crdt.CRDT[T]) {
	val := c.View()
	data, err := json.Marshal(val)
	if err != nil {
		fmt.Printf("Serialization failed: %v\n", err)
		return
	}
	fmt.Printf("[%s] Value: %s\n", c.NodeID(), string(data))
}
