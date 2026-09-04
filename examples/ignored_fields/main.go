//go:generate go run github.com/brunoga/deep/v6/cmd/deep-gen -type=User -output user_deep.go .

package main

import (
	"encoding/json"
	"fmt"
	"log"

	"github.com/brunoga/deep/v6"
)

// Fields tagged json:"-" or deep:"-" are invisible to deep: they never appear
// in a diff, never travel in a patch, and are ignored by Equal. Use this for
// secrets that must not leak into a patch sent over the wire, and for derived
// or transient state that should not drive synchronization.
//
// The trade-off: invisible means invisible to Clone as well, so an ignored
// field comes back zero in a copy. Keep ignored fields to values you can
// recompute or re-fetch.
type User struct {
	Name  string `json:"name"`
	Email string `json:"email"`

	// Never leaves the process.
	PasswordHash string `json:"-"`

	// Derived from other fields; recomputed rather than synchronized.
	DisplayName string `deep:"-" json:"display_name"`
}

func main() {
	current := User{
		Name:         "Alice",
		Email:        "alice@example.com",
		PasswordHash: "$2a$10$abcdefghijklmnop",
		DisplayName:  "Alice (alice@example.com)",
	}

	updated := current
	updated.Name = "Alice Smith"
	updated.Email = "alice.smith@example.com"
	updated.PasswordHash = "$2a$10$ROTATED-SECRET"
	updated.DisplayName = "Alice Smith (alice.smith@example.com)"

	patch, err := deep.Diff(current, updated)
	if err != nil {
		log.Fatal(err)
	}

	wire, _ := json.Marshal(patch)

	fmt.Println("--- PATCH ON THE WIRE ---")
	fmt.Println(string(wire))
	fmt.Println("\nOnly /name and /email are present: the password hash and the")
	fmt.Println("derived display name never enter the patch.")

	// Equal ignores them too: these two differ only in ignored fields.
	a := User{Name: "Bob", PasswordHash: "hash-one", DisplayName: "one"}
	b := User{Name: "Bob", PasswordHash: "hash-two", DisplayName: "two"}
	fmt.Printf("\n--- EQUALITY ---\nDiffer only in ignored fields — equal: %v\n", deep.Equal(a, b))

	// And Clone leaves them zero — the flip side of being invisible.
	clone := deep.Clone(current)
	fmt.Println("\n--- CLONE ---")
	fmt.Printf("name=%q email=%q\n", clone.Name, clone.Email)
	fmt.Printf("password_hash=%q display_name=%q (zeroed, recompute after cloning)\n",
		clone.PasswordHash, clone.DisplayName)
}
