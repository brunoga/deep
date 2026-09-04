//go:generate go run github.com/brunoga/deep/v6/cmd/deep-gen -type=UIState -output uistate_deep.go .

package main

import (
	"encoding/json"
	"fmt"
	"log"

	"github.com/brunoga/deep/v6"
)

type UIState struct {
	Theme string `json:"theme"`
	Open  bool   `json:"sidebar_open"`
}

func main() {
	s1 := UIState{Theme: "dark", Open: false}
	s2 := UIState{Theme: "light", Open: true}

	patch, err := deep.Diff(s1, s2)
	if err != nil {
		log.Fatal(err)
	}

	// Native JSON: compact wire format (k=kind, p=path, o=old, n=new).
	data, _ := json.MarshalIndent(patch, "", "  ")
	fmt.Println("--- NATIVE JSON ---")
	fmt.Println(string(data))

	// RFC 6902 JSON Patch: human-readable, interoperable with other tools.
	rfc, err := patch.ToJSONPatch()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("--- RFC 6902 JSON PATCH ---")
	fmt.Println(string(rfc))

	// Round-trip 1: unmarshal the native format and reapply.
	var p2 deep.Patch[UIState]
	if err := json.Unmarshal(data, &p2); err != nil {
		log.Fatal(err)
	}
	s3 := s1
	deep.Apply(&s3, p2)

	fmt.Println("--- ROUND-TRIP (native) ---")
	fmt.Printf("Theme: %s, Open: %v\n", s3.Theme, s3.Open)

	// Round-trip 2: ingest a JSON Patch document produced elsewhere.
	// ParseJSONPatch accepts any RFC 6902 document, not just our own output.
	external := []byte(`[
		{"op": "replace", "path": "/theme", "value": "solarized"},
		{"op": "replace", "path": "/sidebar_open", "value": false}
	]`)
	incoming, err := deep.ParseJSONPatch[UIState](external)
	if err != nil {
		log.Fatal(err)
	}
	s4 := s1
	if err := deep.Apply(&s4, incoming); err != nil {
		log.Fatal(err)
	}

	fmt.Println("--- ROUND-TRIP (external RFC 6902) ---")
	fmt.Printf("Theme: %s, Open: %v\n", s4.Theme, s4.Open)
}
