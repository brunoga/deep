package testmodels

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

// NewUser creates a new User with the given unexported age.
func NewUser(id int, name string, age int) *User {
	return &User{
		ID:   id,
		Name: name,
		age:  age,
	}
}

// AgePtr returns a pointer to the unexported age field for use in path selectors.
func (u *User) AgePtr() *int {
	return &u.age
}

// SetAge sets the unexported age field.
func (u *User) SetAge(age int) {
	u.age = age
}
