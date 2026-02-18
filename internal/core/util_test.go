package core

import (
	"reflect"
	"testing"
)

func TestConvertValue(t *testing.T) {
	tests := []struct {
		val      any
		target   any
		expected any
	}{
		{int(1), int64(0), int64(1)},
		{float64(1.0), int(0), int(1)},
		// int to string = rune conversion
		{65, "", "A"}, 
		// bool to string = not convertible, returns original
		{true, "", true},
	}

	for _, tt := range tests {
		val := reflect.ValueOf(tt.val)
		targetType := reflect.TypeOf(tt.target)
		got := ConvertValue(val, targetType)
		if !reflect.DeepEqual(got.Interface(), tt.expected) {
			t.Errorf("ConvertValue(%v, %v) = %v, want %v", tt.val, targetType, got.Interface(), tt.expected)
		}
	}
}

func TestExtractKey(t *testing.T) {
	type Keyed struct {
		ID int `deep:"key"`
	}
	
	k := Keyed{ID: 10}
	v := reflect.ValueOf(k)
	
	key := ExtractKey(v, 0)
	if key.(int) != 10 {
		t.Errorf("ExtractKey failed: %v", key)
	}
	
	// Pointer
	kp := &k
	vp := reflect.ValueOf(kp)
	key = ExtractKey(vp, 0)
	if key.(int) != 10 {
		t.Errorf("ExtractKey pointer failed: %v", key)
	}
}
