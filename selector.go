package deep

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
// Paths built from a selector resolve lazily; the result is cached in a global
// table so repeated calls are O(1) after the first.
func (p Path[T, V]) String() string {
	if p.path != "" {
		return p.path
	}
	if p.selector != nil {
		return resolvePathInternal(p.selector)
	}
	return ""
}

// Index returns a new path to the element at the given index within a slice or
// array field. The returned value type is any because the element type cannot
// be recovered at compile time after the index step; prefer the package-level
// Set/Add/Remove functions for type-checked assignments.
func (p Path[T, V]) Index(i int) Path[T, any] {
	return Path[T, any]{
		path: fmt.Sprintf("%s/%d", p.String(), i),
	}
}

// Key returns a new path to the element at the given key within a map field.
// Like Index, the returned value type is any; see the note on Index.
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

	// Non-struct types have no named fields, so no path can be resolved.
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
