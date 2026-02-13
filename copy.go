package deep

import (
	"fmt"
	"reflect"
	"strconv"
	"sync"

	"github.com/brunoga/deep/v3/internal/unsafe"
)

// Copier is an interface that types can implement to provide their own
// custom deep copy logic. The type T in Copy() (T, error) must be the
// same concrete type as the receiver that implements this interface.
type Copier[T any] interface {
	// Copy returns a deep copy of the receiver.
	Copy() (T, error)
}

// CopyOption allows configuring the behavior of the Copy function.
type CopyOption interface {
	applyCopy(*copyConfig)
}

type copyOptionFunc func(*copyConfig)

func (f copyOptionFunc) applyCopy(c *copyConfig) {
	f(c)
}

type copyConfig struct {
	skipUnsupported bool
	ignoredPaths    map[string]bool
}

var defaultCopyConfig = &copyConfig{}

// SkipUnsupported returns an option that tells Copy to skip unsupported types
// (like non-nil functions or channels) instead of returning an error.
func SkipUnsupported() CopyOption {
	return copyOptionFunc(func(c *copyConfig) {
		c.skipUnsupported = true
	})
}

// CopyIgnorePath returns an option that tells Copy to ignore the specified path.
// The ignored path will have the zero value for its type in the resulting copy.
func CopyIgnorePath(path string) CopyOption {
	return copyOptionFunc(func(c *copyConfig) {
		if c.ignoredPaths == nil {
			c.ignoredPaths = make(map[string]bool)
		}
		c.ignoredPaths[path] = true
	})
}

// Copy creates a deep copy of src. It returns the copy and a nil error in case
// of success and the zero value for the type and a non-nil error on failure.
//
// It correctly handles cyclic references and unexported fields.
func Copy[T any](src T, opts ...CopyOption) (T, error) {
	v := reflect.ValueOf(src)
	if !v.IsValid() {
		var t T
		return t, nil
	}

	kind := v.Kind()

	// Fast path for basic types with no options.
	if len(opts) == 0 {
		switch kind {
		case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32,
			reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32,
			reflect.Uint64, reflect.Uintptr, reflect.Float32, reflect.Float64,
			reflect.Complex64, reflect.Complex128, reflect.String:
			return src, nil
		}
	}

	config := defaultCopyConfig
	if len(opts) > 0 {
		config = &copyConfig{}
		for _, opt := range opts {
			opt.applyCopy(config)
		}
	}

	// For cyclic reference detection to work reliably for root value types,
	// they must be addressable. We ensure this by taking the address.
	var rv reflect.Value
	if v.Kind() == reflect.Pointer {
		rv = v
	} else {
		pv := reflect.New(v.Type())
		pv.Elem().Set(v)
		rv = pv.Elem()
	}

	dst, err := copyInternal(rv, config)
	if err != nil {
		var t T
		return t, err
	}

	return dst.Interface().(T), nil
}

// MustCopy creates a deep copy of src. It returns the copy on success or panics
// in case of any failure.
//
// It correctly handles cyclic references and unexported fields.
func MustCopy[T any](src T, opts ...CopyOption) T {
	dst, err := Copy(src, opts...)
	if err != nil {
		panic(err)
	}

	return dst
}

type pointersMapKey struct {
	ptr uintptr
	typ reflect.Type
}
type pointersMap map[pointersMapKey]reflect.Value

var pointersMapPool = sync.Pool{
	New: func() any {
		return make(pointersMap)
	},
}

func copyInternal(v reflect.Value, config *copyConfig) (reflect.Value, error) {
	pointers := pointersMapPool.Get().(pointersMap)
	defer func() {
		for k := range pointers {
			delete(pointers, k)
		}
		pointersMapPool.Put(pointers)
	}()

	dst, err := recursiveCopy(v, pointers, config, "", false)
	if err != nil {
		return reflect.Value{}, err
	}

	return dst, nil
}

func recursiveCopy(v reflect.Value, pointers pointersMap,
	config *copyConfig, path string, atomic bool) (reflect.Value, error) {
	if config.ignoredPaths != nil && config.ignoredPaths[path] {
		return reflect.Zero(v.Type()), nil
	}

	kind := v.Kind()

	if atomic {
		return v, nil
	}

	// Fast path for basic types.
	switch kind {
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32,
		reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32,
		reflect.Uint64, reflect.Uintptr, reflect.Float32, reflect.Float64,
		reflect.Complex64, reflect.Complex128, reflect.String:
		return v, nil
	}

	// Handle cyclic references for addressable values (structs, maps, slices, pointers).
	if v.CanAddr() {
		ptr := v.Addr().Pointer()
		typ := v.Type()
		key := pointersMapKey{ptr, typ}
		if dst, ok := pointers[key]; ok {
			if dst.Kind() == reflect.Pointer && v.Kind() != reflect.Pointer {
				return dst.Elem(), nil
			}
			return dst, nil
		}
	} else if kind == reflect.Pointer && !v.IsNil() {
		ptr := v.Pointer()
		typ := v.Type()
		key := pointersMapKey{ptr, typ}
		if dst, ok := pointers[key]; ok {
			return dst, nil
		}
	}

	// Handle Copier interface.
	// NOTE: We use reflection to detect and call the Copy method because Copier[T]
	// is a generic interface. Since T is the concrete type implementing the
	// interface, we cannot easily perform a type assertion here without knowing
	// T at each step of the recursion. Furthermore, Go reflection doesn't allow
	// dynamic instantiation of generic interfaces. Searching for the method by
	// name and signature provides a flexible "duck-typing" approach that
	// preserves type safety for the user.
	if v.IsValid() && v.CanInterface() {
		attemptCopier := true
		if kind == reflect.Interface || kind == reflect.Pointer {
			if v.IsNil() {
				attemptCopier = false
			}
		}

		if attemptCopier {
			if kind == reflect.Struct || kind == reflect.Pointer {
				method := v.MethodByName("Copy")
				if method.IsValid() && method.Type().NumIn() == 0 && method.Type().NumOut() == 2 {
					if method.Type().Out(0) == v.Type() && method.Type().Out(1).Implements(reflect.TypeOf((*error)(nil)).Elem()) {
						res := method.Call(nil)
						if !res[1].IsNil() {
							return reflect.Value{}, res[1].Interface().(error)
						}
						return res[0], nil
					}
				}
			}
		}
	}

	switch kind {
	case reflect.Array:
		return recursiveCopyArray(v, pointers, config, path)
	case reflect.Interface:
		return recursiveCopyInterface(v, pointers, config, path)
	case reflect.Map:
		return recursiveCopyMap(v, pointers, config, path)
	case reflect.Pointer:
		return recursiveCopyPtr(v, pointers, config, path)
	case reflect.Slice:
		return recursiveCopySlice(v, pointers, config, path)
	case reflect.Struct:
		return recursiveCopyStruct(v, pointers, config, path)
	case reflect.Func, reflect.Chan, reflect.UnsafePointer:
		if v.IsNil() {
			return v, nil
		} else {
			if config.skipUnsupported {
				return reflect.Zero(v.Type()), nil
			} else {
				return reflect.Value{}, fmt.Errorf("unsupported non-nil value for type: %s", v.Type())
			}
		}
	default:
		if config.skipUnsupported {
			return reflect.Zero(v.Type()), nil
		} else {
			return reflect.Value{}, fmt.Errorf("unsupported type: %s", v.Type())
		}
	}
}

func recursiveCopyArray(v reflect.Value, pointers pointersMap,
	config *copyConfig, path string) (reflect.Value, error) {
	dst := reflect.New(v.Type()).Elem()

	hasIgnoredPaths := config.ignoredPaths != nil
	for i := 0; i < v.Len(); i++ {
		var indexPath string
		if hasIgnoredPaths {
			indexPath = path + "[" + strconv.Itoa(i) + "]"
		}
		elem := v.Index(i)
		elemDst, err := recursiveCopy(elem, pointers, config, indexPath, false)
		if err != nil {
			return reflect.Value{}, err
		}

		dst.Index(i).Set(elemDst)
	}

	return dst, nil
}

func recursiveCopyInterface(v reflect.Value, pointers pointersMap,
	config *copyConfig, path string) (reflect.Value, error) {
	if v.IsNil() {
		return v, nil
	}

	return recursiveCopy(v.Elem(), pointers, config, path, false)
}

func recursiveCopyMap(v reflect.Value, pointers pointersMap,
	config *copyConfig, path string) (reflect.Value, error) {
	if v.IsNil() {
		return v, nil
	}

	dst := reflect.MakeMap(v.Type())

	hasIgnoredPaths := config.ignoredPaths != nil
	for _, key := range v.MapKeys() {
		var keyPath string
		if hasIgnoredPaths {
			if path == "" {
				keyPath = fmt.Sprintf("%v", key.Interface())
			} else {
				keyPath = path + "." + fmt.Sprintf("%v", key.Interface())
			}
		}

		elem := v.MapIndex(key)
		elemDst, err := recursiveCopy(elem, pointers, config, keyPath, false)
		if err != nil {
			return reflect.Value{}, err
		}

		dst.SetMapIndex(key, elemDst)
	}

	return dst, nil
}

func recursiveCopyPtr(v reflect.Value, pointers pointersMap,
	config *copyConfig, path string) (reflect.Value, error) {
	if v.IsNil() {
		return v, nil
	}

	ptr := v.Pointer()
	typ := v.Type()
	key := pointersMapKey{ptr, typ}

	if dst, ok := pointers[key]; ok {
		return dst, nil
	}

	dst := reflect.New(v.Type().Elem())
	pointers[key] = dst

	elem := v.Elem()
	elemDst, err := recursiveCopy(elem, pointers, config, path, false)
	if err != nil {
		return reflect.Value{}, err
	}

	dst.Elem().Set(elemDst)

	return dst, nil
}

func recursiveCopySlice(v reflect.Value, pointers pointersMap,
	config *copyConfig, path string) (reflect.Value, error) {
	if v.IsNil() {
		return v, nil
	}

	dst := reflect.MakeSlice(v.Type(), v.Len(), v.Cap())

	hasIgnoredPaths := config.ignoredPaths != nil
	for i := 0; i < v.Len(); i++ {
		var indexPath string
		if hasIgnoredPaths {
			indexPath = path + "[" + strconv.Itoa(i) + "]"
		}
		elem := v.Index(i)
		elemDst, err := recursiveCopy(elem, pointers, config, indexPath, false)
		if err != nil {
			return reflect.Value{}, err
		}

		dst.Index(i).Set(elemDst)
	}

	return dst, nil
}

func recursiveCopyStruct(v reflect.Value, pointers pointersMap,
	config *copyConfig, path string) (reflect.Value, error) {
	dst := reflect.New(v.Type()).Elem()

	if v.CanAddr() {
		pointers[pointersMapKey{v.Addr().Pointer(), v.Type()}] = dst.Addr()
	}

	hasIgnoredPaths := config.ignoredPaths != nil
	for i := 0; i < v.NumField(); i++ {
		field := v.Type().Field(i)
		tag := parseTag(field)
		if tag.ignore {
			continue
		}

		var fieldPath string
		fieldName := field.Name
		if hasIgnoredPaths {
			if path != "" {
				fieldPath = path + "." + fieldName
			} else {
				fieldPath = fieldName
			}
		}

		elem := v.Field(i)

		if !elem.CanInterface() {
			unsafe.DisableRO(&elem)
		}

		elemDst, err := recursiveCopy(elem, pointers, config, fieldPath, tag.atomic)
		if err != nil {
			return reflect.Value{}, err
		}

		dstField := dst.Field(i)

		if !dstField.CanSet() {
			unsafe.DisableRO(&dstField)
		}

		dstField.Set(elemDst)
	}

	return dst, nil
}

func deepCopyValue(v reflect.Value) reflect.Value {
	if !v.IsValid() {
		return reflect.Value{}
	}
	config := defaultCopyConfig
	pointers := pointersMapPool.Get().(pointersMap)
	defer func() {
		for k := range pointers {
			delete(pointers, k)
		}
		pointersMapPool.Put(pointers)
	}()

	// We ensure the root is addressable for cycle detection.
	var rv reflect.Value
	isPtr := v.Kind() == reflect.Pointer
	if isPtr {
		rv = v
	} else {
		pv := reflect.New(v.Type())
		pv.Elem().Set(v)
		rv = pv.Elem()
	}

	copied, err := recursiveCopy(rv, pointers, config, "", false)
	if err != nil {
		return v
	}

	if !isPtr && copied.Kind() == reflect.Pointer {
		return copied.Elem()
	}

	return copied
}
