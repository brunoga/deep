package testmodels

//go:generate go run github.com/brunoga/deep/v5/cmd/deep-gen -type=User,Detail -output user_deep.go .

import (
	"github.com/brunoga/deep/v5/crdt"
)

type User struct {
	ID    int            `json:"id"`
	Name  string         `json:"full_name"`
	Info  Detail         `json:"info"`
	Roles []string       `json:"roles"`
	Score map[string]int `json:"score"`
	Bio   crdt.Text      `json:"bio"`
	age   int            // Unexported field
}

type Detail struct {
	Age     int
	Address string `json:"addr"`
}

// AgePtr returns a pointer to the unexported age field for use in path selectors.
func (u *User) AgePtr() *int {
	return &u.age
}
