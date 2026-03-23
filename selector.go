package deep

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
)

// selector is a function that retrieves a field from a struct of type T.
type selector[T, V any] func(*T) *V

// Path represents a type-safe path to a field of type V within type T.
type Path[T, V any] struct {
	sel  selector[T, V]
	path string
}

// String returns the string representation of the path.
// Paths built from a selector resolve lazily; the result is cached in a global
// table so repeated calls are O(1) after the first.
func (p Path[T, V]) String() string {
	if p.path != "" {
		return p.path
	}
	if p.sel != nil {
		return resolvePathInternal(p.sel)
	}
	return ""
}

// Field creates a new type-safe path from a selector function.
func Field[T, V any](s func(*T) *V) Path[T, V] {
	return Path[T, V]{sel: selector[T, V](s)}
}

// At returns a type-safe path to the element at index i within a slice field.
//
//	rolesPath := deep.Field(func(u *User) *[]string { return &u.Roles })
//	elemPath  := deep.At(rolesPath, 0) // Path[User, string]
func At[T any, S ~[]E, E any](p Path[T, S], i int) Path[T, E] {
	return Path[T, E]{path: fmt.Sprintf("%s/%d", p.String(), i)}
}

// MapKey returns a type-safe path to the value at key k within a map field.
//
//	scoreMap := deep.Field(func(u *User) *map[string]int { return &u.Score })
//	entry    := deep.MapKey(scoreMap, "kills") // Path[User, int]
func MapKey[T any, M ~map[K]V, K comparable, V any](p Path[T, M], k K) Path[T, V] {
	return Path[T, V]{path: fmt.Sprintf("%s/%v", p.String(), k)}
}

var (
	pathCache   = make(map[reflect.Type]map[uintptr]string)
	pathCacheMu sync.RWMutex
)

func resolvePathInternal[T, V any](s selector[T, V]) string {
	var zero T
	typ := reflect.TypeOf(zero)

	// Non-struct types have no named fields, so no path can be resolved.
	if typ.Kind() != reflect.Struct {
		return ""
	}

	// Calculate offset by running the selector on a zero instance.
	// The selector must return the address of a field (&u.Field), not a field
	// value. If it returns nil the selector was written incorrectly.
	base := reflect.New(typ).Elem()
	ptr := s(base.Addr().Interface().(*T))
	if ptr == nil {
		panic(fmt.Sprintf("deep.Field: selector returned nil — use &u.Field, not u.Field (type %T)", (*T)(nil)))
	}

	offset := reflect.ValueOf(ptr).Pointer() - base.Addr().Pointer()

	pathCacheMu.RLock()
	cache, ok := pathCache[typ]
	pathCacheMu.RUnlock()

	if ok {
		if p, ok := cache[offset]; ok {
			return p
		}
	}

	// Cache miss: acquire write lock and re-check before scanning (TOCTOU fix).
	pathCacheMu.Lock()
	defer pathCacheMu.Unlock()

	// Another goroutine may have scanned this type between our read and write lock.
	if cache, ok := pathCache[typ]; ok {
		if p, ok := cache[offset]; ok {
			return p
		}
	}

	if pathCache[typ] == nil {
		pathCache[typ] = make(map[uintptr]string)
	}

	scanStructInternal("", typ, 0, pathCache[typ])

	return pathCache[typ][offset]
}

func scanStructInternal(prefix string, typ reflect.Type, baseOffset uintptr, cache map[uintptr]string) {
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)

		// Use JSON tag if available, otherwise field name.
		// Skip fields tagged json:"-" — they have no JSON path.
		name := field.Name
		if tag := field.Tag.Get("json"); tag != "" {
			name = strings.Split(tag, ",")[0]
			if name == "-" {
				continue
			}
		}

		fieldPath := prefix + "/" + name
		offset := baseOffset + field.Offset

		cache[offset] = fieldPath

		// Recurse into nested structs
		fieldType := field.Type
		for fieldType.Kind() == reflect.Ptr {
			fieldType = fieldType.Elem()
		}

		if fieldType.Kind() == reflect.Struct {
			scanStructInternal(fieldPath, fieldType, offset, cache)
		}
	}
}
