package crdt

import (
	"reflect"
	"testing"
	"time"

	"github.com/brunoga/deep/v4"
)

type TestUser struct {
	ID      int `deep:"key"`
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
	patch := deep.MustDiff(node.View(), TestUser{ID: 1, Name: "New"})

	// Use the new helper to wrap it into a Delta and update local state
	delta := node.CreateDelta(patch)

	if node.View().Name != "New" {
		t.Errorf("expected New, got %s", node.View().Name)
	}

	if delta.Timestamp.Logical != 0 {
		t.Errorf("expected logical clock 0, got %d", delta.Timestamp.Logical)
	}

	// We cannot access internal clocks/tombstones directly anymore.
	// But we can check if the Delta has the correct metadata updates implicitly via ApplyDelta to another node.
	node2 := NewCRDT(TestUser{ID: 1, Name: "Old"}, "node2")
	node2.ApplyDelta(delta)
	if node2.View().Name != "New" {
		t.Error("CreateDelta produced invalid delta")
	}
}

func TestCRDT_Merge(t *testing.T) {
	initial := TestUser{
		ID:      1,
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
	time.Sleep(2 * time.Millisecond)
	nodeB.Edit(func(u *TestUser) { u.Name = "Bob" })

	nodeA.Merge(nodeB)
	if nodeA.View().Name != "Bob" {
		t.Errorf("LWW failed: expected 'Bob', got '%s'", nodeA.View().Name)
	}
}

func TestCRDT_JSON(t *testing.T) {
	node := NewCRDT(TestUser{ID: 1, Name: "Initial"}, "node1")
	node.Edit(func(u *TestUser) {
		u.Name = "Modified"
	})

	data, err := node.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON failed: %v", err)
	}

	newNode := &CRDT[TestUser]{}
	if err := newNode.UnmarshalJSON(data); err != nil {
		t.Fatalf("UnmarshalJSON failed: %v", err)
	}

	if newNode.NodeID() != node.NodeID() {
		t.Errorf("expected NodeID %s, got %s", node.NodeID(), newNode.NodeID())
	}

	if newNode.View().Name != "Modified" {
		t.Errorf("expected Name Modified, got %s", newNode.View().Name)
	}
	
	// Ensure clocks are restored by doing a merge
	// If clocks are missing, merge might fail to converge correctly or assume old data
	node2 := NewCRDT(TestUser{ID: 1, Name: "Initial"}, "node2")
	node2.Merge(newNode)
	if node2.View().Name != "Modified" {
		t.Error("Merge with unmarshalled node failed")
	}
}
