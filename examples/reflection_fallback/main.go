package main

import (
	"fmt"

	"github.com/brunoga/deep/v5"
)

// No //go:generate here: this package has no generated code, so everything runs
// on the reflection engine. That engine handles unexported fields, which
// generated code cannot reach, and it is also what a generated type falls back
// to for any single operation its fast path does not model.
//
// It handles cycles too, as shown below — but so does generated code now, when
// deep-gen sees that a type can reach itself. See the cyclic_graph example for
// that side of it; here the point is that a type with no generated code at all
// still gets the same behaviour.

// Session carries state the owning package keeps private.
type Session struct {
	User  string
	token string // unexported: no JSON tag, no exported accessor
	hits  int
}

func NewSession(user, token string) *Session {
	return &Session{User: user, token: token, hits: 1}
}

func (s *Session) String() string {
	return fmt.Sprintf("{User:%s token:%s hits:%d}", s.User, s.token, s.hits)
}

// Node forms a doubly-linked ring — a cycle that would make a naive recursive
// copy run forever. Nothing here is generated, so the reflection engine is what
// keeps track of what it has already copied.
type Node struct {
	Name string
	Next *Node
	Prev *Node
}

func main() {
	// --- Unexported fields participate in Clone and Equal ---
	s1 := NewSession("alice", "secret-abc")
	clone := deep.Clone(s1)

	fmt.Println("--- UNEXPORTED FIELDS ---")
	fmt.Printf("original: %v\n", s1)
	fmt.Printf("clone:    %v\n", clone)
	fmt.Printf("equal: %v, distinct allocations: %v\n", deep.Equal(s1, clone), s1 != clone)

	// Equality sees the private token: same public field, different secret.
	other := NewSession("alice", "secret-xyz")
	fmt.Printf("same user but different token — equal: %v\n", deep.Equal(s1, other))

	// --- Cycles are handled by identity tracking ---
	a := &Node{Name: "a"}
	b := &Node{Name: "b"}
	a.Next, a.Prev = b, b
	b.Next, b.Prev = a, a

	ring := deep.Clone(a)

	fmt.Println("\n--- CYCLIC STRUCTURES ---")
	fmt.Printf("clone terminates: %v\n", ring.Name)
	fmt.Printf("ring closes back on itself: %v\n", ring.Next.Next == ring)
	fmt.Printf("copy is independent of the original: %v\n", ring.Next != b)
	fmt.Printf("deep.Equal on cyclic values: %v\n", deep.Equal(a, ring))

	// Diff and Apply work here too — no generated code required.
	modified := deep.Clone(s1)
	modified.User = "alice.smith"
	patch, err := deep.Diff(*s1, *modified)
	if err != nil {
		panic(err)
	}

	fmt.Println("\n--- DIFF/APPLY VIA REFLECTION ---")
	fmt.Println(patch)
	applied := *deep.Clone(s1)
	if err := deep.Apply(&applied, patch); err != nil {
		panic(err)
	}
	fmt.Printf("result: %v\n", &applied)
}
