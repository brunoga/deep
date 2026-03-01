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
			deep.Field(func(u *testmodels.User) *[]string { return &u.Roles }).Index(1).String(),
			"/roles/1",
		},
		{
			"map key",
			deep.Field(func(u *testmodels.User) *map[string]int { return &u.Score }).Key("alice").String(),
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
