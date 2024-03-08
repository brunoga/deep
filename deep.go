package deep

import (
	"fmt"
	"reflect"
)

// Copy creates a deep copy of src. It returns the copy and a nil error in case
// of success and the zero value for the type and a non-nil error on failure.
func Copy[T any](src T) (T, error) {
	return copy(src, false)
}

// CopySkipUnsupported creates a deep copy of src. It returns the copy and a nil
// errorin case of success and the zero value for the type and a non-nil error
// on failure. Unsupported types are skipped (the copy will have the zero value
// for the type) instead of returning an error.
func CopySkipUnsupported[T any](src T) (T, error) {
	return copy(src, true)
}

// MustCopy creates a deep copy of src. It returns the copy on success or panics
// in case of any failure.
func MustCopy[T any](src T) T {
	dst, err := copy(src, false)
	if err != nil {
		panic(err)
	}

	return dst
}

func copy[T any](src T, skipUnsupported bool) (T, error) {
	var t T
	pointers := make(map[uintptr]interface{})
	dst, err := recursiveCopy(src, pointers, skipUnsupported)
	if err != nil {
		return t, err
	}

	return dst.(T), nil
}

func recursiveCopy(src any, pointers map[uintptr]interface{},
	skipUnsupported bool) (any, error) {
	if src == nil {
		return nil, nil
	}

	// Get the reflect.Value associated with the source.
	v := reflect.ValueOf(src)

	var dst any
	var err error

	switch v.Kind() {
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32,
		reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32,
		reflect.Uint64, reflect.Uintptr, reflect.Float32, reflect.Float64,
		reflect.Complex64, reflect.Complex128, reflect.String:
		// Primitive type, just copy it.
		dst = src
	case reflect.Array:
		dst, err = recursiveCopyArray(v, pointers, skipUnsupported)
	case reflect.Map:
		dst, err = recursiveCopyMap(v, pointers, skipUnsupported)
	case reflect.Ptr:
		dst, err = recursiveCopyPtr(v, pointers, skipUnsupported)
	case reflect.Slice:
		dst, err = recursiveCopySlice(v, pointers, skipUnsupported)
	case reflect.Struct:
		dst, err = recursiveCopyStruct(v, pointers, skipUnsupported)
	case reflect.Func, reflect.Chan, reflect.UnsafePointer:
		if v.IsNil() {
			// If we have a nil function, unsafe pointer or channel, then we
			// can copy it.
			dst = src
		} else {
			if skipUnsupported {
				dst = getZero(v)
			} else {
				err = fmt.Errorf("unsuported non-nil value for type: %T", src)
			}
		}
	default:
		if skipUnsupported {
			dst = getZero(v)
		} else {
			err = fmt.Errorf("unsuported type: %T", src)
		}
	}

	return dst, err
}

func recursiveCopyArray(v reflect.Value, pointers map[uintptr]interface{},
	skipUnsupported bool) (any, error) {
	dst := reflect.New(v.Type()).Elem()

	for i := 0; i < v.Len(); i++ {
		elem := v.Index(i)
		elemDst, err := recursiveCopy(elem.Interface(), pointers, skipUnsupported)
		if err != nil {
			return nil, err
		}

		dst.Index(i).Set(reflect.ValueOf(elemDst))
	}

	return dst.Interface(), nil
}

func recursiveCopyMap(v reflect.Value, pointers map[uintptr]interface{},
	skipUnsupported bool) (any, error) {
	dst := reflect.MakeMap(v.Type())

	for _, key := range v.MapKeys() {
		elem := v.MapIndex(key)
		elemDst, err := recursiveCopy(elem.Interface(), pointers,
			skipUnsupported)
		if err != nil {
			return nil, err
		}

		dst.SetMapIndex(key, reflect.ValueOf(elemDst))
	}

	return dst.Interface(), nil
}

func recursiveCopyPtr(v reflect.Value, pointers map[uintptr]interface{},
	skipUnsupported bool) (any, error) {
	// If the pointer is nil, just return its zero value.
	if v.IsNil() {
		return reflect.Zero(v.Type()).Interface(), nil
	}

	// If the pointer is already in the pointers map, return it.
	ptr := v.Pointer()
	if dst, ok := pointers[ptr]; ok {
		return dst, nil
	}

	// Otherwise, create a new pointer and add it to the pointers map.
	dst := reflect.New(v.Type().Elem())
	pointers[ptr] = dst.Interface()

	// Proceed with the copy.
	elem := v.Elem()
	elemDst, err := recursiveCopy(elem.Interface(), pointers, skipUnsupported)
	if err != nil {
		return nil, err
	}

	dst.Elem().Set(reflect.ValueOf(elemDst))

	return dst.Interface(), nil
}

func recursiveCopySlice(v reflect.Value, pointers map[uintptr]interface{},
	skipUnsupported bool) (any, error) {
	dst := reflect.MakeSlice(v.Type(), v.Len(), v.Cap())

	for i := 0; i < v.Len(); i++ {
		elem := v.Index(i)
		elemDst, err := recursiveCopy(elem.Interface(), pointers,
			skipUnsupported)
		if err != nil {
			return nil, err
		}

		dst.Index(i).Set(reflect.ValueOf(elemDst))
	}

	return dst.Interface(), nil
}

func recursiveCopyStruct(v reflect.Value, pointers map[uintptr]interface{},
	skipUnsupported bool) (any, error) {
	dst := reflect.New(v.Type()).Elem()

	for i := 0; i < v.NumField(); i++ {
		elem := v.Field(i)

		// If the field is unexported, we need to disable read-only mode. If it
		// is exported, doing this changes nothing so we just do it. We need to
		// do this here not because we are writting to the field (this is the
		// source), but because Interface() does not work if the read-only bits
		// are set.
		disableRO(&elem)

		elemDst, err := recursiveCopy(elem.Interface(), pointers,
			skipUnsupported)
		if err != nil {
			return nil, err
		}

		dstField := dst.Field(i)

		// If the field is unexported, we need to disable read-only mode so we
		// can actually write to it.
		disableRO(&dstField)

		if elemDst == nil {
			// Naked nil value, just continue.
			continue
		}

		dstField.Set(reflect.ValueOf(elemDst))
	}

	return dst.Interface(), nil
}

func getZero(v reflect.Value) any {
	return reflect.Zero(v.Type()).Interface()
}
