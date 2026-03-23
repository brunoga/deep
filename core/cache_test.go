package core

import (
	"reflect"
	"testing"
)

func TestGetTypeInfo(t *testing.T) {
	type S struct {
		A int
		B string `deep:"-"`
	}

	info := GetTypeInfo(reflect.TypeOf(S{}))
	if len(info.Fields) != 2 {
		t.Errorf("Expected 2 fields, got %d", len(info.Fields))
	}

	if info.Fields[0].Name != "A" || info.Fields[0].Tag.Ignore {
		t.Errorf("Field A incorrect: %+v", info.Fields[0])
	}

	if info.Fields[1].Name != "B" || !info.Fields[1].Tag.Ignore {
		t.Errorf("Field B incorrect: %+v", info.Fields[1])
	}

	// Cache hit check (implicit)
	info2 := GetTypeInfo(reflect.TypeOf(S{}))
	if info != info2 {
		t.Errorf("Expected same info pointer for same type (cache hit)")
	}
}
