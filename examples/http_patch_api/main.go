package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"

	"github.com/brunoga/deep/v4"
)

// Resource is the data we want to manage over the network.
type Resource struct {
	ID    string `json:"id"`
	Data  string `json:"data"`
	Value int    `json:"value"`
}

// ServerState represents the server's local database.
var ServerState = map[string]*Resource{
	"res-1": {ID: "res-1", Data: "Initial Data", Value: 100},
}

func main() {
	// --- PART 1: THE SERVER ---
	// We set up a mock server that accepts patches via POST.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// 1. Read the serialized patch from the request body.
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "Failed to read request body", http.StatusBadRequest)
			return
		}

		// 2. Deserialize the patch.
		// NOTE: We use deep.NewPatch[Resource]() to get a typed patch object
		// that knows how to unmarshal itself.
		patch := deep.NewPatch[Resource]()
		if err := json.Unmarshal(body, patch); err != nil {
			http.Error(w, "Invalid patch format", http.StatusBadRequest)
			return
		}

		// 3. Find the local resource to update.
		id := r.URL.Query().Get("id")
		res, ok := ServerState[id]
		if !ok {
			http.Error(w, "Resource not found", http.StatusNotFound)
			return
		}

		// 4. Apply the patch to the server's local state.
		// We use ApplyChecked to ensure the update is consistent with what the client saw.
		if err := patch.ApplyChecked(res); err != nil {
			http.Error(w, fmt.Sprintf("Apply failed: %v", err), http.StatusConflict)
			return
		}

		fmt.Fprintf(w, "Resource %s updated successfully", id)
	}))
	defer server.Close()

	// --- PART 2: THE CLIENT ---
	// The client has a local copy of the resource.
	clientLocalCopy := Resource{ID: "res-1", Data: "Initial Data", Value: 100}

	// The client makes some changes locally.
	updatedCopy := clientLocalCopy
	updatedCopy.Data = "Network Modified Data"
	updatedCopy.Value = 250

	// Generate a patch representing those changes.
	patch := deep.MustDiff(clientLocalCopy, updatedCopy)

	// Serialize the patch to JSON for transmission.
	patchJSON, err := json.Marshal(patch)
	if err != nil {
		fmt.Printf("Failed to marshal patch: %v\n", err)
		return
	}

	fmt.Printf("Client: Sending patch to server (%d bytes)\n", len(patchJSON))

	// Send the patch via HTTP POST.
	resp, err := http.Post(server.URL+"?id=res-1", "application/json", bytes.NewBuffer(patchJSON))
	if err != nil {
		fmt.Printf("Client Error: %v\n", err)
		return
	}
	defer resp.Body.Close()

	// --- PART 3: VERIFICATION ---
	status, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Printf("Failed to read response: %v\n", err)
		return
	}
	fmt.Printf("Server Response: %s\n", string(status))
	fmt.Printf("Server Final State for res-1: %+v\n", ServerState["res-1"])
}
