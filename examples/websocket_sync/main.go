package main

import (
	"encoding/json"
	"fmt"
	v5 "github.com/brunoga/deep/v5"
)

type GameWorld struct {
	Players map[string]*Player `json:"players"`
	Time    int                `json:"time"`
}

type Player struct {
	X    int    `json:"x"`
	Y    int    `json:"y"`
	Name string `json:"name"`
}

func main() {
	serverState := GameWorld{
		Players: map[string]*Player{
			"p1": {X: 0, Y: 0, Name: "Hero"},
		},
		Time: 0,
	}

	clientState := v5.Copy(serverState)

	fmt.Println("Initial Server State:", serverState.Players["p1"])
	fmt.Println("Initial Client State:", clientState.Players["p1"])

	// Server Tick
	previousState := v5.Copy(serverState)
	serverState.Players["p1"].X += 5
	serverState.Players["p1"].Y += 10
	serverState.Time++

	// Broadcast Patch
	patch := v5.Diff(previousState, serverState)
	wireData, _ := json.Marshal(patch)
	fmt.Printf("\n[Network] Broadcasting Patch (%d bytes): %s\n", len(wireData), string(wireData))

	// Client Receive
	var receivedPatch v5.Patch[GameWorld]
	json.Unmarshal(wireData, &receivedPatch)

	v5.Apply(&clientState, receivedPatch)

	fmt.Printf("\nClient State after receiving patch: %v\n", clientState.Players["p1"])
	fmt.Printf("Client Game Time: %d\n", clientState.Time)

	if clientState.Players["p1"].X == serverState.Players["p1"].X {
		fmt.Println("\nSynchronization Successful!")
	}
}
