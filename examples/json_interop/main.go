package main

import (
	"encoding/json"
	"fmt"

	"github.com/brunoga/deep/v3"
)

// UIState represents something typically shared between a Go backend and a JS frontend.
type UIState struct {
	Theme       string `json:"theme"`
	SidebarOpen bool   `json:"sidebarOpen"`
	UserCount   int    `json:"userCount"`
}

func main() {
	// 1. Backend has the current state.
	stateA := UIState{
		Theme:       "dark",
		SidebarOpen: true,
		UserCount:   10,
	}

	// 2. State changes after some events.
	stateB := UIState{
		Theme:       "light", // Theme changed
		SidebarOpen: true,
		UserCount:   11, // One user joined
	}

	// 3. Backend calculates the diff.
	patch := deep.MustDiff(stateA, stateB)

	// 4. Backend wants to send this patch to a JavaScript frontend.
	// We use ToJSONPatch() which generates an RFC 6902 compliant list of ops.
	jsonPatchBytes, err := patch.ToJSONPatch()
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	// Print the JSON that would be sent over the wire.
	fmt.Println("RFC 6902 JSON PATCH (sent to frontend):")
	fmt.Println(string(jsonPatchBytes))

	// 5. Demonstrate normal JSON serialization of the Patch object itself.
	// This is useful if you want to save the patch in a database (like MongoDB)
	// and restore it later in another Go service.
	serializedPatch, err := json.MarshalIndent(patch, "", "  ")
	if err != nil {
		fmt.Printf("Marshal failed: %v\n", err)
		return
	}
	fmt.Println("\nINTERNAL DEEP JSON REPRESENTATION (for persistence):")
	fmt.Println(string(serializedPatch))
}
