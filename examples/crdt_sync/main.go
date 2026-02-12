package main

import (
	"encoding/json"
	"fmt"

	"github.com/brunoga/deep/v2/crdt"
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
	nodeB.ApplyDelta(deltaA)
	printState(nodeB)

	fmt.Println("\n--- Syncing Node B -> Node A (Full Merge) ---")
	nodeA.Merge(nodeB)
	printState(nodeA)

	fmt.Println("\n--- Serialized State ---")
	data, _ := json.MarshalIndent(nodeA, "", "  ")
	fmt.Println(string(data))
}

func printState[T any](c *crdt.CRDT[T]) {
	val := c.View()
	data, _ := json.Marshal(val)
	fmt.Printf("[%s] Value: %s\n", c.NodeID, string(data))
}
