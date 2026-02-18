package main

import (
	"fmt"
	"github.com/brunoga/deep/v4"
)

// ResourceID represents a complex key that might have transient state.
type ResourceID struct {
	Namespace string
	Name      string
	// SessionID is transient and should not be used for logical identity
	SessionID int 
}

// CanonicalKey implements deep.Keyer to normalize the key for diffing.
func (r ResourceID) CanonicalKey() any {
	return fmt.Sprintf("%s:%s", r.Namespace, r.Name)
}

func main() {
	// 1. Base map with specific SessionIDs
	m1 := map[ResourceID]string{
		{Namespace: "prod", Name: "api", SessionID: 100}: "Running",
	}

	// 2. Target map where SessionIDs have changed (transient), but state is updated
	m2 := map[ResourceID]string{
		{Namespace: "prod", Name: "api", SessionID: 200}: "Suspended",
	}

	fmt.Println("--- COMPARING MAPS WITH SEMANTIC KEYS ---")
	
	// Without Keyer, Diff would see this as a Remove(SessionID:100) and Add(SessionID:200).
	// With Keyer, it sees it as an Update to the "prod:api" resource.
	patch := deep.MustDiff(m1, m2)

	fmt.Println("Patch Summary:")
	fmt.Println(patch.Summary())
	fmt.Println()

	// 3. Apply the patch
	final := m1
	err := patch.ApplyChecked(&final)
	if err != nil {
		fmt.Printf("Apply failed: %v\n", err)
	}

	fmt.Println("--- FINAL MAP STATE ---")
	for k, v := range final {
		fmt.Printf("Key: %+v, Value: %s\n", k, v)
	}
}
