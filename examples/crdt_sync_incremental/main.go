package main

import (
	"encoding/json"
	"fmt"

	"github.com/brunoga/deep/v6/crdt"
	"github.com/brunoga/deep/v6/crdt/hlc"
)

// Two replicas can always exchange whole documents, but that costs the size of
// the document every time however little changed. A state vector says how much
// of each writer's output a replica already holds — one number per writer, not
// per character — so a peer can work out exactly what is missing and send only
// that.
type Doc = crdt.Document

func main() {
	server := crdt.NewDocument(hlc.NewClock("server"))
	client := crdt.NewDocument(hlc.NewClock("client"))

	// A sizeable shared document.
	for i := 0; i < 5000; i++ {
		server.Insert(server.Len(), "x")
	}

	// First sync: the client has nothing, so it receives everything.
	first := server.Since(client.StateVector())
	client.Apply(first)
	firstBytes, _ := json.Marshal(first)

	fmt.Println("--- FIRST SYNC (client starts empty) ---")
	fmt.Printf("  sent %d bytes for a %d-character document\n", len(firstBytes), server.Len())
	fmt.Printf("  in sync: %v\n", client.String() == server.String())

	// A small edit afterwards.
	server.Insert(server.Len(), " and one more sentence.")

	update := server.Since(client.StateVector())
	updateBytes, _ := json.Marshal(update)
	whole, _ := json.Marshal(server)

	fmt.Println("\n--- AFTER A 23-CHARACTER EDIT ---")
	fmt.Printf("  whole document: %d bytes\n", len(whole))
	fmt.Printf("  what is missing: %d bytes\n", len(updateBytes))
	client.Apply(update)
	fmt.Printf("  in sync: %v\n", client.String() == server.String())

	// The state vector itself is small, whatever the document holds.
	sv, _ := json.Marshal(client.StateVector())
	fmt.Printf("  state vector the client sends to ask: %d bytes\n", len(sv))

	// Deletions travel too, though the peer already has the characters.
	server.Delete(0, 100)
	del := server.Since(client.StateVector())
	client.Apply(del)
	fmt.Println("\n--- AFTER DELETING 100 CHARACTERS ---")
	fmt.Printf("  in sync: %v (%d characters each)\n",
		client.String() == server.String(), client.Len())

	// Both directions at once: each side edits, each sends what the other lacks.
	server.Insert(0, "[server] ")
	client.Insert(client.Len(), " [client]")

	fromServer := server.Since(client.StateVector())
	fromClient := client.Since(server.StateVector())
	client.Apply(fromServer)
	server.Apply(fromClient)

	fmt.Println("\n--- CONCURRENT EDITS, SYNCED BOTH WAYS ---")
	fmt.Printf("  converged: %v\n", client.String() == server.String())
	fmt.Printf("  begins %q, ends %q\n",
		server.String()[:20], server.String()[len(server.String())-20:])

	// Applying an update twice changes nothing.
	client.Apply(fromServer)
	fmt.Printf("  unchanged after redelivery: %v\n", client.String() == server.String())
}
