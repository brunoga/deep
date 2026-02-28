package main

import (
	"fmt"
	"github.com/brunoga/deep/v5"
)

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
		},
	}
	f2 := Fleet{
		Devices: map[DeviceID]string{
			{"prod", 1}: "suspended",
		},
	}

	patch := v5.Diff(f1, f2)

	fmt.Println("--- COMPARING MAPS WITH SEMANTIC KEYS ---")
	for _, op := range patch.Operations {
		fmt.Printf("Change at %s: %v -> %v\n", op.Path, op.Old, op.New)
	}
}
