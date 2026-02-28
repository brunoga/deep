package core

import (
	"encoding/json"
	"reflect"

	"github.com/brunoga/deep/v5/internal/unsafe"
)

func ConvertValue(v reflect.Value, targetType reflect.Type) reflect.Value {
	if !v.IsValid() {
		return reflect.Zero(targetType)
	}

	if v.Type() == targetType {
		return v
	}

	if v.Type().AssignableTo(targetType) {
		return v
	}

	if v.Type().ConvertibleTo(targetType) {
		return v.Convert(targetType)
	}

	// Handle pointer wrapping
	if targetType.Kind() == reflect.Pointer && v.Type().AssignableTo(targetType.Elem()) {
		ptr := reflect.New(targetType.Elem())
		ptr.Elem().Set(v)
		return ptr
	}

	// Handle JSON/Gob numbers
	if v.Kind() == reflect.Float64 {
		switch targetType.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			return reflect.ValueOf(int64(v.Float())).Convert(targetType)
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			return reflect.ValueOf(uint64(v.Float())).Convert(targetType)
		case reflect.Float32:
			return reflect.ValueOf(float32(v.Float())).Convert(targetType)
		}
	}

	// Handle Map -> Struct (JSON Unmarshal)
	if v.Kind() == reflect.Map && targetType.Kind() == reflect.Struct {
		if v.Type().Key().Kind() == reflect.String {
			// Best effort: marshal map to JSON, unmarshal to struct
			data, err := json.Marshal(v.Interface())
			if err == nil {
				newStruct := reflect.New(targetType).Elem()
				// We need a pointer to the struct for Unmarshal
				if err := json.Unmarshal(data, newStruct.Addr().Interface()); err == nil {
					return newStruct
				}
			}
		}
	}

	return v
}

func SetValue(v, newVal reflect.Value) {
	if !newVal.IsValid() {
		if v.CanSet() {
			v.Set(reflect.Zero(v.Type()))
		}
		return
	}

	// Navigate through pointers if needed
	target := v
	for target.Kind() == reflect.Pointer && target.Type() != newVal.Type() {
		if target.IsNil() {
			target.Set(reflect.New(target.Type().Elem()))
		}
		target = target.Elem()
	}

	converted := ConvertValue(newVal, target.Type())
	target.Set(converted)
}

func ValueToInterface(v reflect.Value) any {
	if !v.IsValid() {
		return nil
	}
	if !v.CanInterface() {
		unsafe.DisableRO(&v)
	}
	return v.Interface()
}

func InterfaceToValue(i any) reflect.Value {
	if i == nil {
		return reflect.Value{}
	}
	return reflect.ValueOf(i)
}

func ExtractKey(v reflect.Value, fieldIdx int) any {
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return nil
	}
	return v.Field(fieldIdx).Interface()
}
