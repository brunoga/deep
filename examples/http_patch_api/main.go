package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"

	"github.com/brunoga/deep/v5"
)

type Resource struct {
	ID    string `json:"id"`
	Data  string `json:"data"`
	Value int    `json:"value"`
}

var serverState = map[string]*Resource{
	"res-1": {ID: "res-1", Data: "Initial Data", Value: 100},
}

func main() {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		var patch deep.Patch[Resource]
		if err := json.Unmarshal(body, &patch); err != nil {
			http.Error(w, "Invalid patch", http.StatusBadRequest)
			return
		}

		id := r.URL.Query().Get("id")
		if err := deep.Apply(serverState[id], patch); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		fmt.Fprintf(w, "OK")
	}))
	defer server.Close()

	// Client: compute patch and send it.
	c1 := Resource{ID: "res-1", Data: "Initial Data", Value: 100}
	c2 := Resource{ID: "res-1", Data: "Network Modified Data", Value: 250}

	patch, err := deep.Diff(c1, c2)
	if err != nil {
		log.Fatal(err)
	}
	data, _ := json.Marshal(patch)

	fmt.Println("--- CLIENT ---")
	fmt.Printf("Sending patch (%d bytes)\n", len(data))

	resp, _ := http.Post(server.URL+"?id=res-1", "application/json", bytes.NewBuffer(data))
	io.ReadAll(resp.Body)

	fmt.Println("\n--- SERVER STATE AFTER PATCH ---")
	fmt.Printf("%+v\n", *serverState["res-1"])
}
