package deep_test

import (
	"testing"

	"github.com/brunoga/deep/v5"
	"github.com/brunoga/deep/v5/internal/testmodels"
)

func TestSelector(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{
			"simple field",
			deep.Field(func(u *testmodels.User) *int { return &u.ID }).String(),
			"/id",
		},
		{
			"nested field",
			deep.Field(func(u *testmodels.User) *int { return &u.Info.Age }).String(),
			"/info/Age",
		},
		{
			"slice index",
			deep.At(deep.Field(func(u *testmodels.User) *[]string { return &u.Roles }), 1).String(),
			"/roles/1",
		},
		{
			"map key",
			deep.MapKey(deep.Field(func(u *testmodels.User) *map[string]int { return &u.Score }), "alice").String(),
			"/score/alice",
		},
		{
			"unexported field",
			deep.Field((*testmodels.User).AgePtr).String(),
			"/age",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.path != tt.want {
				t.Errorf("Path() = %v, want %v", tt.path, tt.want)
			}
		})
	}
}

func TestSelectorNestedPointer(t *testing.T) {
	type NestedPointer struct {
		Inner *struct {
			Value string
		} `json:"inner"`
	}

	path := deep.Field(func(n *NestedPointer) *string { return &n.Inner.Value })
	got := path.String()
	want := "/inner/Value"
	if got != want {
		t.Errorf("path.String() = %q, want %q", got, want)
	}
}

// TestSelectorCircularType verifies that self-referential struct types do not
// cause infinite recursion during path resolution.
func TestSelectorCircularType(t *testing.T) {
	type Node struct {
		Value int    `json:"value"`
		Next  *Node  `json:"next"`
	}

	path := deep.Field(func(n *Node) *int { return &n.Value })
	got := path.String()
	want := "/value"
	if got != want {
		t.Errorf("path.String() = %q, want %q", got, want)
	}

	// Selecting through the pointer field should also work (one level deep).
	path2 := deep.Field(func(n *Node) *int { return &n.Next.Value })
	got2 := path2.String()
	want2 := "/next/value"
	if got2 != want2 {
		t.Errorf("path.String() = %q, want %q", got2, want2)
	}
}
