//go:generate go run github.com/brunoga/deep/v6/cmd/deep-gen -type=User .

package main

import (
	"fmt"
	"log"
	"log/slog"
	"os"

	"github.com/brunoga/deep/v6"
)

type User struct {
	Name  string          `json:"name"`
	Email string          `json:"email"`
	Tags  map[string]bool `json:"tags"`
}

func main() {
	u1 := User{
		Name:  "Alice",
		Email: "alice@example.com",
		Tags:  map[string]bool{"user": true},
	}

	u2 := User{
		Name:  "Alice Smith",
		Email: "alice.smith@example.com",
		Tags:  map[string]bool{"user": true, "admin": true},
	}

	// --- Pattern 1: diff-based audit trail ---
	// Diff captures the old and new values for every changed field.
	patch, err := deep.Diff(u1, u2)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("--- AUDIT LOG ---")
	for _, op := range patch.Operations {
		switch op.Kind {
		case deep.OpReplace:
			fmt.Printf("  MODIFY  %s: %v → %v\n", op.Path, op.Old, op.New)
		case deep.OpAdd:
			fmt.Printf("  ADD     %s: %v\n", op.Path, op.New)
		case deep.OpRemove:
			fmt.Printf("  REMOVE  %s: %v\n", op.Path, op.Old)
		}
	}

	// --- Pattern 2: embedded OpLog + injectable logger ---
	// OpLog operations fire structured log messages during Apply.
	// WithLogger routes them to any slog.Logger — useful for tracing,
	// per-request loggers, or test capture.
	// Wrap the audited change itself in log operations, so the trace and the
	// audit log above describe the same update.
	tracePatch := patch.WithGuard(nil)
	tracePatch.Operations = append(
		[]deep.Operation{{Kind: deep.OpLog, Path: "/", New: "applying user update"}},
		tracePatch.Operations...,
	)
	tracePatch.Operations = append(tracePatch.Operations,
		deep.Operation{Kind: deep.OpLog, Path: "/", New: "user update complete"})

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{} // omit timestamp for stable output
			}
			return a
		},
	}))

	fmt.Println("\n--- TRACED APPLY ---")
	if err := deep.Apply(&u1, tracePatch, deep.WithLogger(logger)); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Result: %+v\n", u1)

	if deep.Equal(u1, u2) {
		fmt.Println("\nThe audited patch reproduced the target state exactly.")
	}
}
