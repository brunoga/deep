//go:generate go run github.com/brunoga/deep/v5/cmd/deep-gen -type=Server,Meta,Limits -output server_deep.go .

package main

import (
	"fmt"
	"log"

	"github.com/brunoga/deep/v5"
)

// Nested structs are addressed by path, so a diff pinpoints the field that
// actually changed instead of replacing the whole sub-struct. Embedded fields
// are addressed by their type name — the same way Go names them.
//
// Every struct a generated type reaches must have generated code too, which is
// why Meta and Limits are listed alongside Server in the go:generate directive.
type Meta struct {
	Region string `json:"region"`
	Zone   string `json:"zone"`
}

type Limits struct {
	CPU    int `json:"cpu"`
	Memory int `json:"memory"`
}

type Server struct {
	Meta            // embedded: paths start at /Meta
	Name    string  `json:"name"`
	Limits  Limits  `json:"limits"`
	Sidecar *Limits `json:"sidecar"`
}

func main() {
	before := Server{
		Meta:   Meta{Region: "us-east", Zone: "a"},
		Name:   "api-1",
		Limits: Limits{CPU: 2, Memory: 4096},
	}

	after := before
	after.Zone = "b"                             // embedded field, promoted as usual
	after.Limits.Memory = 8192                   // one field inside a nested struct
	after.Sidecar = &Limits{CPU: 1, Memory: 512} // pointer field appearing

	patch, err := deep.Diff(before, after)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("--- DIFF ---")
	fmt.Println(patch)
	fmt.Println("\n/Meta/zone and /limits/memory pinpoint single fields;")
	fmt.Println("/sidecar appears as one operation because it went from nil to set.")

	// Patches can also be written directly against a nested path.
	cpuPath := deep.Field(func(s *Server) *int { return &s.Limits.CPU })
	scale := deep.Edit(&before).With(deep.Set(cpuPath, 8)).Build()

	fmt.Println("\n--- TARGETED UPDATE ---")
	fmt.Printf("selector path: %s\n", cpuPath.String())
	if err := deep.Apply(&before, scale); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("limits: %+v\n", before.Limits)

	// Round-trip the full diff.
	target := deep.Clone(before)
	if err := deep.Apply(&target, patch); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("\nzone=%q memory=%d sidecar=%+v\n", target.Zone, target.Limits.Memory, *target.Sidecar)
}
