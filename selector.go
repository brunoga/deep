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
// Paths built from a selector resolve lazily; the result is cached per selector
// function so repeated calls are O(1) after the first.
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
func At[T any, S ~[]E, E any](p Path[T, S], i int) Path[T, E] {
	return Path[T, E]{path: fmt.Sprintf("%s/%d", p.String(), i)}
}

// MapKey returns a type-safe path to the value at key k within a map field.
func MapKey[T any, M ~map[K]V, K comparable, V any](p Path[T, M], k K) Path[T, V] {
	return Path[T, V]{path: fmt.Sprintf("%s/%v", p.String(), k)}
}

// pathCache stores resolved paths keyed by selector function pointer.
var pathCache sync.Map // map[uintptr]string

func resolvePathInternal[T, V any](s selector[T, V]) string {
	key := reflect.ValueOf(s).Pointer()
	if cached, ok := pathCache.Load(key); ok {
		return cached.(string)
	}

	var zero T
	typ := reflect.TypeOf(zero)

	if typ.Kind() != reflect.Struct {
		pathCache.Store(key, "")
		return ""
	}

	base := reflect.New(typ).Elem()
	initializePointers(base, make(map[reflect.Type]bool))

	targetPtr := s(base.Addr().Interface().(*T))
	if targetPtr == nil {
		panic(fmt.Sprintf("deep.Field: selector returned nil — use &u.Field, not u.Field (type %T)", (*T)(nil)))
	}

	targetAddr := reflect.ValueOf(targetPtr).Pointer()
	targetTyp := reflect.TypeOf(targetPtr).Elem()

	path := findPathByAddr(base, targetAddr, targetTyp, "", make(map[uintptr]bool))
	if path == "" {
		// Fallback: maybe it's the root itself?
		if targetAddr == base.Addr().Pointer() {
			path = "/"
		}
	}

	pathCache.Store(key, path)
	return path
}

// initializePointers allocates nil pointer fields so selectors can safely
// dereference them. inProgress prevents infinite recursion for self-referential
// types (e.g. linked lists).
func initializePointers(v reflect.Value, inProgress map[reflect.Type]bool) {
	if v.Kind() != reflect.Struct {
		return
	}
	typ := v.Type()
	if inProgress[typ] {
		return
	}
	inProgress[typ] = true
	defer delete(inProgress, typ)

	for i := 0; i < v.NumField(); i++ {
		f := v.Field(i)
		if f.Kind() == reflect.Ptr {
			if f.IsNil() && f.CanSet() {
				f.Set(reflect.New(f.Type().Elem()))
			}
			initializePointers(f.Elem(), inProgress)
		} else if f.Kind() == reflect.Struct {
			initializePointers(f, inProgress)
		}
	}
}

// findPathByAddr walks v looking for the field at targetAddr with type
// targetTyp. visited tracks struct addresses already on the walk stack to
// prevent infinite recursion through circular pointer structures.
func findPathByAddr(v reflect.Value, targetAddr uintptr, targetTyp reflect.Type, prefix string, visited map[uintptr]bool) string {
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return ""
		}
		addr := v.Pointer()
		if visited[addr] {
			return ""
		}
		visited[addr] = true
		defer delete(visited, addr)
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return ""
	}

	t := v.Type()
	for i := 0; i < v.NumField(); i++ {
		field := t.Field(i)
		f := v.Field(i)

		name := field.Name
		if tag := field.Tag.Get("json"); tag != "" {
			name = strings.Split(tag, ",")[0]
			if name == "-" {
				continue
			}
		}

		fieldPath := prefix + "/" + name

		// Recurse first to find the most specific match (handles overlapping
		// addresses, e.g. a struct and its first field share the same address).
		if f.Kind() == reflect.Struct {
			if p := findPathByAddr(f, targetAddr, targetTyp, fieldPath, visited); p != "" {
				return p
			}
		} else if f.Kind() == reflect.Ptr && !f.IsNil() && f.Elem().Kind() == reflect.Struct {
			if p := findPathByAddr(f, targetAddr, targetTyp, fieldPath, visited); p != "" {
				return p
			}
		}

		// Check this field after recursing so deeper matches win.
		if f.Addr().Pointer() == targetAddr && f.Type() == targetTyp {
			return fieldPath
		}
	}
	return ""
}
