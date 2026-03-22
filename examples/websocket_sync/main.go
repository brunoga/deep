//go:generate go run github.com/brunoga/deep/v5/cmd/deep-gen -type=GameWorld,Player .

package main

import (
	"encoding/json"
	"fmt"
	"log"

	"github.com/brunoga/deep/v5"
)

type GameWorld struct {
	Players map[string]Player `json:"players"`
	Time    int               `json:"time"`
}

type Player struct {
	X    int    `json:"x"`
	Y    int    `json:"y"`
	Name string `json:"name"`
}

func main() {
	serverState := GameWorld{
		Players: map[string]Player{
			"p1": {X: 0, Y: 0, Name: "Hero"},
		},
		Time: 0,
	}

	clientState := deep.Copy(serverState)

	fmt.Println("--- INITIAL STATE ---")
	fmt.Printf("Server: %+v\n", serverState.Players["p1"])
	fmt.Printf("Client: %+v\n", clientState.Players["p1"])

	// Server tick: move player and advance time.
	previousState := deep.Copy(serverState)
	p := serverState.Players["p1"]
	p.X += 5
	p.Y += 10
	serverState.Players["p1"] = p
	serverState.Time++

	// Compute and broadcast the patch (only the changed fields).
	patch, err := deep.Diff(previousState, serverState)
	if err != nil {
		log.Fatal(err)
	}
	wireData, _ := json.Marshal(patch)

	fmt.Println("\n--- SERVER BROADCAST ---")
	fmt.Printf("Patch (%d bytes): %s\n", len(wireData), string(wireData))

	// Client receives and applies.
	var receivedPatch deep.Patch[GameWorld]
	json.Unmarshal(wireData, &receivedPatch)
	deep.Apply(&clientState, receivedPatch)

	fmt.Println("\n--- CLIENT STATE AFTER SYNC ---")
	fmt.Printf("Player: %+v\n", clientState.Players["p1"])
	fmt.Printf("Time:   %d\n", clientState.Time)

	if clientState.Players["p1"].X == serverState.Players["p1"].X {
		fmt.Println("\nSUCCESS: Client synchronized!")
	}
}
