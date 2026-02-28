package v5

import (
	"testing"
)

func TestResolvePath(t *testing.T) {
	tests := []struct {
		name     string
		selector Selector[User, any]
		want     string
	}{
		{
			name: "Simple field",
			selector: func(u *User) *any {
				res := any(&u.ID)
				return &res
			},
			want: "/id",
		},
		{
			name: "Field with JSON tag",
			selector: func(u *User) *any {
				res := any(&u.Name)
				return &res
			},
			want: "/full_name",
		},
		{
			name: "Nested field",
			selector: func(u *User) *any {
				res := any(&u.Info.Age)
				return &res
			},
			want: "/info/Age",
		},
		{
			name: "Nested field with JSON tag",
			selector: func(u *User) *any {
				res := any(&u.Info.Address)
				return &res
			},
			want: "/info/addr",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Note: We need to cast our specialized selectors to work with the test helper
			// Since our Path struct is generic, let's test it directly.
		})
	}
}

// Actual test implementation to avoid generic casting issues in the table
func TestPathResolution(t *testing.T) {
	p1 := Field(func(u *User) *int { return &u.ID })
	if p1.String() != "/id" {
		t.Errorf("got %s, want /id", p1.String())
	}

	p2 := Field(func(u *User) *string { return &u.Name })
	if p2.String() != "/full_name" {
		t.Errorf("got %s, want /full_name", p2.String())
	}

	p3 := Field(func(u *User) *int { return &u.Info.Age })
	if p3.String() != "/info/Age" {
		t.Errorf("got %s, want /info/Age", p3.String())
	}

	p4 := Field(func(u *User) *string { return &u.Info.Address })
	if p4.String() != "/info/addr" {
		t.Errorf("got %s, want /info/addr", p4.String())
	}
}
