package core

import (
	"reflect"
	"testing"
)

func TestParseTag(t *testing.T) {
	tests := []struct {
		tag      string
		expected StructTag
	}{
		{`deep:"-"`, StructTag{Ignore: true}},
		{`deep:"readonly"`, StructTag{ReadOnly: true}},
		{`deep:"atomic"`, StructTag{Atomic: true}},
		{`deep:"key"`, StructTag{Key: true}},
		{`deep:"-" json:"foo"`, StructTag{Ignore: true}},
		{`json:"foo"`, StructTag{}},
		{`deep:"unknown"`, StructTag{}},
	}

	for _, tt := range tests {
		field := reflect.StructField{Tag: reflect.StructTag(tt.tag)}
		got := ParseTag(field)
		if got != tt.expected {
			t.Errorf("ParseTag(%s) = %+v, want %+v", tt.tag, got, tt.expected)
		}
	}
}

func TestGetKeyField(t *testing.T) {
	type NoKey struct {
		A int
	}
	type WithKey struct {
		ID   string `deep:"key"`
		Name string
	}
	type WithKeyInt struct {
		ID int `deep:"key"`
	}

	// No key
	idx, ok := GetKeyField(reflect.TypeOf(NoKey{}))
	if ok {
		t.Errorf("Expected no key for NoKey, got index %d", idx)
	}

	// With key string
	idx, ok = GetKeyField(reflect.TypeOf(WithKey{}))
	if !ok || idx != 0 {
		t.Errorf("Expected key at index 0 for WithKey, got %d, %v", idx, ok)
	}

	// With key int
	idx, ok = GetKeyField(reflect.TypeOf(WithKeyInt{}))
	if !ok || idx != 0 {
		t.Errorf("Expected key at index 0 for WithKeyInt, got %d, %v", idx, ok)
	}
}
