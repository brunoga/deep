package deep_test

import (
	"testing"

	"github.com/brunoga/deep/v6"
	"github.com/brunoga/deep/v6/internal/testmodels"
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

// TestMapKeyEscapesJSONPointerSpecials verifies that map keys containing the
// JSON Pointer reserved characters '/' and '~' are RFC 6901-escaped so the
// resulting path navigates to the correct key.
func TestMapKeyEscapesJSONPointerSpecials(t *testing.T) {
	type S struct {
		M map[string]int `json:"m"`
	}

	mPath := deep.Field(func(s *S) *map[string]int { return &s.M })

	cases := []struct {
		key      string
		wantPath string
	}{
		{"a/b", "/m/a~1b"},
		{"c~d", "/m/c~0d"},
		{"~/", "/m/~0~1"},
		{"plain", "/m/plain"},
	}
	for _, c := range cases {
		got := deep.MapKey[S, map[string]int, string, int](mPath, c.key).String()
		if got != c.wantPath {
			t.Errorf("MapKey(%q) path = %q, want %q", c.key, got, c.wantPath)
		}
	}

	// End-to-end: a Set through MapKey on a slash-bearing key must hit the
	// right entry.
	s := &S{M: map[string]int{"a/b": 0, "other": 0}}
	p := deep.NewPatch[S]().With(
		deep.Set(deep.MapKey[S, map[string]int, string, int](mPath, "a/b"), 42),
	).Build()
	if err := deep.Apply(s, p); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if s.M["a/b"] != 42 {
		t.Errorf("expected M[\"a/b\"] == 42 after Apply, got %v", s.M)
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
