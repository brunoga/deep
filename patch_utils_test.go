package deep

import (
	"reflect"
	"testing"
)

func TestPatch_NumericConversion(t *testing.T) {
	tests := []struct {
		val    any
		target any
	}{
		{int64(10), int(0)},
		{float64(10), int(0)},
		{float64(10), int8(0)},
		{float64(10), int16(0)},
		{float64(10), int32(0)},
		{float64(10), int64(0)},
		{float64(10), uint(0)},
		{float64(10), uint8(0)},
		{float64(10), uint16(0)},
		{float64(10), uint32(0)},
		{float64(10), uint64(0)},
		{float64(10), uintptr(0)},
		{float64(10), float32(0)},
		{float64(10), float64(0)},
		{10, float64(0)}, // int to float
		{"s", "s"},
		{nil, int(0)},
	}
	for _, tt := range tests {
		var v reflect.Value
		if tt.val == nil {
			v = reflect.Value{}
		} else {
			v = reflect.ValueOf(tt.val)
		}
		targetType := reflect.TypeOf(tt.target)
		got := convertValue(v, targetType)
		if got.Type() != targetType && v.IsValid() && !v.Type().AssignableTo(targetType) {
			t.Errorf("Expected %v, got %v for %v", targetType, got.Type(), tt.val)
		}
	}
}
