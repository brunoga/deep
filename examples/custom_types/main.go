package main

import (
	"fmt"
	"time"
	"github.com/brunoga/deep/v3"
)

type Audit struct {
	User      string
	Timestamp time.Time
}

func main() {
	// 1. Initial State
	base := Audit{
		User:      "admin",
		Timestamp: time.Now(),
	}

	// 2. Target State
	target := base
	target.Timestamp = base.Timestamp.Add(1 * time.Hour)

	// 3. Setup global custom diff logic for time.Time
	deep.RegisterCustomDiff(func(a, b time.Time) (deep.Patch[time.Time], error) {
		if a.Equal(b) {
			return nil, nil
		}
		// Return an atomic replacement patch
		builder := deep.NewPatchBuilder[time.Time]()
		builder.Set(a, b)
		return builder.Build()
	})

	fmt.Println("--- COMPARING WITH CUSTOM TYPE REGISTRY ---")
	patch := deep.MustDiff(base, target)

	fmt.Println("Patch Summary:")
	fmt.Println(patch.Summary())
	fmt.Println()

	// 4. Verify Application
	final := base
	err := patch.ApplyChecked(&final)
	if err != nil {
		fmt.Printf("Apply failed: %v\n", err)
		return
	}

	fmt.Printf("Initial: %v\n", base.Timestamp.Format(time.Kitchen))
	fmt.Printf("Final:   %v\n", final.Timestamp.Format(time.Kitchen))
}
