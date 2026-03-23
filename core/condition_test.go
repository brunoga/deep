package core

import (
	"reflect"
	"testing"
)

func TestCheckType(t *testing.T) {
	type testUser struct {
		ID int
	}
	u := testUser{ID: 1}

	if !CheckType("foo", "string") {
		t.Error("CheckType string failed")
	}
	if !CheckType(1, "number") {
		t.Error("CheckType number failed")
	}
	if !CheckType(true, "boolean") {
		t.Error("CheckType boolean failed")
	}
	if !CheckType(u, "object") {
		t.Error("CheckType object failed")
	}
	if !CheckType([]int{}, "array") {
		t.Error("CheckType array failed")
	}
	if !CheckType((*testUser)(nil), "null") {
		t.Error("CheckType null failed")
	}
	if !CheckType(nil, "null") {
		t.Error("CheckType nil null failed")
	}
	if CheckType("foo", "number") {
		t.Error("CheckType invalid failed")
	}
}

func TestEvaluateCondition(t *testing.T) {
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
		{c: &Condition{Op: "and", Sub: []*Condition{
			{Op: "==", Path: "/id", Value: 1},
			{Op: "==", Path: "/full_name", Value: "Alice"},
		}}, want: true},
		{c: &Condition{Op: "or", Sub: []*Condition{
			{Op: "==", Path: "/id", Value: 2},
			{Op: "==", Path: "/full_name", Value: "Alice"},
		}}, want: true},
		{c: &Condition{Op: "not", Sub: []*Condition{
			{Op: "==", Path: "/id", Value: 2},
		}}, want: true},
	}

	for _, tt := range tests {
		got, err := EvaluateCondition(root, tt.c)
		if err != nil {
			t.Errorf("EvaluateCondition(%s) error: %v", tt.c.Op, err)
		}
		if got != tt.want {
			t.Errorf("EvaluateCondition(%s) = %v, want %v", tt.c.Op, got, tt.want)
		}
	}
}
