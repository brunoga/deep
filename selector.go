package v5

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
)

// Selector is a function that retrieves a field from a struct of type T.
// This allows type-safe path generation.
type Selector[T, V any] func(*T) *V

// Path represents a type-safe path to a field of type V within type T.
type Path[T, V any] struct {
	selector Selector[T, V]
	path     string
}

// String returns the string representation of the path.
func (p Path[T, V]) String() string {
	if p.path == "" && p.selector != nil {
		p.path = resolvePathInternal(p.selector)
	}
	return p.path
}

// Index returns a new path to the element at the given index.
func (p Path[T, V]) Index(i int) Path[T, any] {
	return Path[T, any]{
		path: fmt.Sprintf("%s/%d", p.String(), i),
	}
}

// Key returns a new path to the element at the given key.
func (p Path[T, V]) Key(k any) Path[T, any] {
	return Path[T, any]{
		path: fmt.Sprintf("%s/%v", p.String(), k),
	}
}

// Field creates a new type-safe path from a selector.
func Field[T, V any](s Selector[T, V]) Path[T, V] {
	return Path[T, V]{
		selector: s,
	}
}

var (
	pathCache   = make(map[reflect.Type]map[uintptr]string)
	pathCacheMu sync.RWMutex
)

func resolvePathInternal[T, V any](s Selector[T, V]) string {
	var zero T
	typ := reflect.TypeOf(zero)

	// In a real implementation, we'd handle non-struct types or return "/" for the root.
	if typ.Kind() != reflect.Struct {
		return ""
	}

	// Calculate offset by running the selector on a dummy instance
	base := reflect.New(typ).Elem()
	ptr := s(base.Addr().Interface().(*T))

	offset := reflect.ValueOf(ptr).Pointer() - base.Addr().Pointer()

	pathCacheMu.RLock()
	cache, ok := pathCache[typ]
	pathCacheMu.RUnlock()

	if ok {
		if p, ok := cache[offset]; ok {
			return p
		}
	}

	// Cache miss: Scan the struct for offsets
	pathCacheMu.Lock()
	defer pathCacheMu.Unlock()

	if pathCache[typ] == nil {
		pathCache[typ] = make(map[uintptr]string)
	}

	scanStructInternal("", typ, 0, pathCache[typ])

	return pathCache[typ][offset]
}

func scanStructInternal(prefix string, typ reflect.Type, baseOffset uintptr, cache map[uintptr]string) {
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)

		// Use JSON tag if available, otherwise field name
		name := field.Name
		if tag := field.Tag.Get("json"); tag != "" {
			name = strings.Split(tag, ",")[0]
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

