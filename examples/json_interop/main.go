package main

import (
	"encoding/json"
	"fmt"
	"github.com/brunoga/deep/v5"
)

type UIState struct {
	Theme string `json:"theme"`
	Open  bool   `json:"sidebar_open"`
}

func main() {
	s1 := UIState{Theme: "dark", Open: false}
	s2 := UIState{Theme: "light", Open: true}

	patch := v5.Diff(s1, s2)

	// In v5, Patch is a pure struct. JSON interop is native.
	data, _ := json.MarshalIndent(patch, "", "  ")
	fmt.Println("INTERNAL V5 JSON REPRESENTATION:")
	fmt.Println(string(data))

	// Unmarshal back
	var p2 v5.Patch[UIState]
	json.Unmarshal(data, &p2)

	s3 := s1
	v5.Apply(&s3, p2)

	if s3.Theme == "light" {
		fmt.Println("\nSUCCESS: Patch restored and applied from JSON.")
	}
}
