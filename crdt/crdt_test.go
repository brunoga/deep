package crdt

import (
	"reflect"
	"testing"

	"github.com/brunoga/deep/v2"
)

type TestUser struct {
	ID      int    `deep:"key"`
	Name    string
	Friends []TestFriend `deep:"key"`
}

type TestFriend struct {
	ID   int `deep:"key"`
	Name string
}

func TestCRDT_EditDelta(t *testing.T) {
	nodeA := NewCRDT(TestUser{ID: 1, Name: "Alice"}, "node-a")
	nodeB := NewCRDT(TestUser{ID: 1, Name: "Alice"}, "node-b")

	delta := nodeA.Edit(func(u *TestUser) {
		u.Name = "Alice Mod"
	})

	if nodeA.View().Name != "Alice Mod" {
		t.Error("Edit failed to update local value")
	}

	nodeB.ApplyDelta(delta)
	if nodeB.View().Name != "Alice Mod" {
		t.Error("ApplyDelta failed to update remote value")
	}
}

func TestCRDT_CreateDelta(t *testing.T) {
	node := NewCRDT(TestUser{ID: 1, Name: "Old"}, "node1")

	// Manually create a patch using deep.Diff
	patch := deep.Diff(node.Value, TestUser{ID: 1, Name: "New"})
	
	// Use the new helper to wrap it into a Delta and update local state
	delta := node.CreateDelta(patch)

	if node.Value.Name != "New" {
		t.Errorf("expected New, got %s", node.Value.Name)
	}

	if delta.Timestamp.Logical != 0 {
		t.Errorf("expected logical clock 0, got %d", delta.Timestamp.Logical)
	}

	if clock, ok := node.Clocks["Name"]; !ok || clock != delta.Timestamp {
		t.Errorf("metadata clock not updated correctly")
	}
}

func TestCRDT_Merge(t *testing.T) {
	initial := TestUser{
		ID: 1,
		Friends: []TestFriend{{ID: 100, Name: "Bob"}},
	}
	nodeA := NewCRDT(initial, "node-a")
	nodeB := NewCRDT(initial, "node-b")

	// Concurrent edits
	nodeA.Edit(func(u *TestUser) {
		u.Friends[0].Name = "Bobby"
	})
	nodeB.Edit(func(u *TestUser) {
		u.Friends = append(u.Friends, TestFriend{ID: 101, Name: "Charlie"})
	})

	nodeA.Merge(nodeB)
	nodeB.Merge(nodeA)

	viewA := nodeA.View()
	viewB := nodeB.View()

	if !reflect.DeepEqual(viewA, viewB) {
		t.Errorf("Nodes diverged after merge! A: %+v B: %+v", viewA, viewB)
	}

	if len(viewA.Friends) != 2 {
		t.Errorf("Expected 2 friends, got %d", len(viewA.Friends))
	}
}

func TestCRDT_Conflict(t *testing.T) {
	nodeA := NewCRDT(TestUser{ID: 1, Name: "Original"}, "node-a")
	nodeB := NewCRDT(TestUser{ID: 1, Name: "Original"}, "node-b")

	// nodeA edits first
	nodeA.Edit(func(u *TestUser) { u.Name = "Alice" })
	// nodeB edits later (physically)
	nodeB.Edit(func(u *TestUser) { u.Name = "Bob" })

	nodeA.Merge(nodeB)
	if nodeA.View().Name != "Bob" {
		t.Errorf("LWW failed: expected 'Bob', got '%s'", nodeA.View().Name)
	}
}
