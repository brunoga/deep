package main

import (
	"encoding/json"
	"fmt"

	"github.com/brunoga/deep/v2"
)

// GameWorld represents a shared state (e.g., in a real-time game or collab tool).
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
	// 1. Initial server state.
	serverState := GameWorld{
		Players: map[string]*Player{
			"p1": {X: 0, Y: 0, Name: "Hero"},
		},
		Time: 0,
	}

	// 2. Simulation: A client has the same initial state.
	clientState := deep.MustCopy(serverState)

	fmt.Println("Initial Server State:", serverState.Players["p1"])
	fmt.Println("Initial Client State:", clientState.Players["p1"])

	// 3. SERVER TICK: Something changes.
	// Player moves and time advances.
	previousState := deep.MustCopy(serverState) // Keep track of old state for diffing

	serverState.Players["p1"].X += 5
	serverState.Players["p1"].Y += 10
	serverState.Time++

	// 4. BROADCAST: Instead of sending the WHOLE GameWorld (which could be large),
	// we only send the Patch.
	patch := deep.Diff(previousState, serverState)

	// Network transport (simulated).
	wireData, _ := json.Marshal(patch)
	fmt.Printf("\n[Network] Broadcasting Patch (%d bytes): %s\n", len(wireData), string(wireData))

	// 5. CLIENT RECEIVE: The client receives the wire data.
	receivedPatch := deep.NewPatch[GameWorld]()
	_ = json.Unmarshal(wireData, receivedPatch)

	// Client applies the patch to its local copy.
	receivedPatch.Apply(&clientState)

	fmt.Printf("\nClient State after receiving patch: %v\n", clientState.Players["p1"])
	fmt.Printf("Client Game Time: %d\n", clientState.Time)

	// 6. Verification: Both should be identical again.
	if clientState.Players["p1"].X == serverState.Players["p1"].X {
		fmt.Println("\nSynchronization Successful!")
	}
}
