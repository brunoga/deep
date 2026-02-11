package deep

import (
	"reflect"

	"github.com/brunoga/deep/v2/internal/unsafe"
)

func convertValue(v reflect.Value, targetType reflect.Type) reflect.Value {
	if !v.IsValid() {
		return reflect.Zero(targetType)
	}

	if v.Type().AssignableTo(targetType) {
		return v
	}

	if v.Type().ConvertibleTo(targetType) {
		return v.Convert(targetType)
	}

	// Handle JSON/Gob numbers
	if v.Kind() == reflect.Float64 {
		switch targetType.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			return reflect.ValueOf(int64(v.Float())).Convert(targetType)
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			return reflect.ValueOf(uint64(v.Float())).Convert(targetType)
		case reflect.Float32, reflect.Float64:
			return reflect.ValueOf(v.Float()).Convert(targetType)
		}
	}

	return v
}

func setValue(v, newVal reflect.Value) {
	if !newVal.IsValid() {
		if v.CanSet() {
			v.Set(reflect.Zero(v.Type()))
		}
		return
	}

	converted := convertValue(newVal, v.Type())
	v.Set(converted)
}

func valueToInterface(v reflect.Value) any {
	if !v.IsValid() {
		return nil
	}
	if !v.CanInterface() {
		unsafe.DisableRO(&v)
	}
	return v.Interface()
}

func interfaceToValue(i any) reflect.Value {
	if i == nil {
		return reflect.Value{}
	}
	return reflect.ValueOf(i)
}
