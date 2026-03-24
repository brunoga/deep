package main

import (
	"fmt"
	"log"

	"github.com/brunoga/deep/v5"
)

// DeviceID is a non-string map key. deep uses its String() representation
// to build JSON Pointer paths, so map[DeviceID]string produces paths like
// /devices/prod:1 rather than requiring string keys.
type DeviceID struct {
	Namespace string
	ID        int
}

func (d DeviceID) String() string {
	return fmt.Sprintf("%s:%d", d.Namespace, d.ID)
}

type Fleet struct {
	Devices map[DeviceID]string `json:"devices"`
}

func main() {
	f1 := Fleet{
		Devices: map[DeviceID]string{
			{"prod", 1}: "running",
			{"prod", 2}: "running",
		},
	}
	f2 := Fleet{
		Devices: map[DeviceID]string{
			{"prod", 1}: "suspended",
			{"prod", 3}: "running",
		},
	}

	patch, err := deep.Diff(f1, f2)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("--- FLEET DIFF (non-string map keys) ---")
	for _, op := range patch.Operations {
		switch op.Kind {
		case deep.OpReplace:
			fmt.Printf("  %-8s %s: %v → %v\n", op.Kind, op.Path, op.Old, op.New)
		case deep.OpAdd:
			fmt.Printf("  %-8s %s: %v\n", op.Kind, op.Path, op.New)
		case deep.OpRemove:
			fmt.Printf("  %-8s %s: %v\n", op.Kind, op.Path, op.Old)
		}
	}
}
