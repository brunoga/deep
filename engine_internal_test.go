package deep

import (
	"reflect"
	"testing"
)

func TestCheckType(t *testing.T) {
	type testUser struct {
		ID int
	}
	u := testUser{ID: 1}

	if !checkType("foo", "string") {
		t.Error("checkType string failed")
	}
	if !checkType(1, "number") {
		t.Error("checkType number failed")
	}
	if !checkType(true, "boolean") {
		t.Error("checkType boolean failed")
	}
	if !checkType(u, "object") {
		t.Error("checkType object failed")
	}
	if !checkType([]int{}, "array") {
		t.Error("checkType array failed")
	}
	if !checkType((*testUser)(nil), "null") {
		t.Error("checkType null failed")
	}
	if !checkType(nil, "null") {
		t.Error("checkType nil null failed")
	}
	if checkType("foo", "number") {
		t.Error("checkType invalid failed")
	}
}

func TestEvaluateConditionInternal(t *testing.T) {
	type testUser struct {
		ID   int    `json:"id"`
		Name string `json:"full_name"`
	}
	u := testUser{ID: 1, Name: "Alice"}
	root := reflect.ValueOf(u)

	tests := []struct {
		c    *Condition
		want bool
	}{
		{c: &Condition{Op: "exists", Path: "/id"}, want: true},
		{c: &Condition{Op: "exists", Path: "/none"}, want: false},
		{c: &Condition{Op: "matches", Path: "/full_name", Value: "^Al.*$"}, want: true},
		{c: &Condition{Op: "type", Path: "/id", Value: "number"}, want: true},
		{c: And(Eq(Field(func(u *testUser) *int { return &u.ID }), 1), Eq(Field(func(u *testUser) *string { return &u.Name }), "Alice")), want: true},
		{c: Or(Eq(Field(func(u *testUser) *int { return &u.ID }), 2), Eq(Field(func(u *testUser) *string { return &u.Name }), "Alice")), want: true},
		{c: Not(Eq(Field(func(u *testUser) *int { return &u.ID }), 2)), want: true},
	}

	for _, tt := range tests {
		got, err := evaluateCondition(root, tt.c)
		if err != nil {
			t.Errorf("evaluateCondition(%s) error: %v", tt.c.Op, err)
		}
		if got != tt.want {
			t.Errorf("evaluateCondition(%s) = %v, want %v", tt.c.Op, got, tt.want)
		}
	}
}
