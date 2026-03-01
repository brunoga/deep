package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"

	v5 "github.com/brunoga/deep/v5"
)

type Resource struct {
	ID    string `json:"id"`
	Data  string `json:"data"`
	Value int    `json:"value"`
}

var ServerState = map[string]*Resource{
	"res-1": {ID: "res-1", Data: "Initial Data", Value: 100},
}

func main() {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		// In v5, Patch is just a struct. We can unmarshal it directly.
		var patch v5.Patch[Resource]
		if err := json.Unmarshal(body, &patch); err != nil {
			http.Error(w, "Invalid patch", http.StatusBadRequest)
			return
		}

		id := r.URL.Query().Get("id")
		res := ServerState[id]

		// Apply the patch
		if err := v5.Apply(res, patch); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		fmt.Fprintf(w, "Resource %s updated successfully", id)
	}))
	defer server.Close()

	// Client
	c1 := Resource{ID: "res-1", Data: "Initial Data", Value: 100}
	c2 := c1
	c2.Data = "Network Modified Data"
	c2.Value = 250

	patch := v5.Diff(c1, c2)
	data, _ := json.Marshal(patch)

	fmt.Printf("Client: Sending patch to server (%d bytes)\n", len(data))
	resp, _ := http.Post(server.URL+"?id=res-1", "application/json", bytes.NewBuffer(data))

	status, _ := io.ReadAll(resp.Body)
	fmt.Printf("Server Response: %s\n", string(status))
	fmt.Printf("Server Final State for res-1: %+v\n", ServerState["res-1"])
}
