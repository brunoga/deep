package main

import (
	"encoding/json"
	"fmt"
	"log"

	v5 "github.com/brunoga/deep/v5"
)

type UIState struct {
	Theme string `json:"theme"`
	Open  bool   `json:"sidebar_open"`
}

func main() {
	s1 := UIState{Theme: "dark", Open: false}
	s2 := UIState{Theme: "light", Open: true}

	patch, err := v5.Diff(s1, s2)
	if err != nil {
		log.Fatal(err)
	}

	// Native v5 JSON: compact wire format.
	data, _ := json.MarshalIndent(patch, "", "  ")
	fmt.Println("--- NATIVE V5 JSON ---")
	fmt.Println(string(data))

	// RFC 6902 JSON Patch: human-readable, interoperable with other tools.
	rfc, err := patch.ToJSONPatch()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("--- RFC 6902 JSON PATCH ---")
	fmt.Println(string(rfc))

	// Round-trip: unmarshal the native format and reapply.
	var p2 v5.Patch[UIState]
	if err := json.Unmarshal(data, &p2); err != nil {
		log.Fatal(err)
	}
	s3 := s1
	v5.Apply(&s3, p2)

	fmt.Println("--- ROUND-TRIP RESULT ---")
	fmt.Printf("Theme: %s, Open: %v\n", s3.Theme, s3.Open)
}
