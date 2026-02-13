package deep

import (
	"reflect"
	"sync"

	"github.com/brunoga/deep/v2/internal/unsafe"
)

var visitedPool = sync.Pool{
	New: func() any {
		return make(map[visitKey]bool)
	},
}

// Equal performs a deep equality check between a and b.
//
// Unlike reflect.DeepEqual, this function:
// 1. Respects `deep:"-"` struct tags.
// 2. Uses the library's internal reflection cache for faster struct traversal.
// 3. Optimized for common Go types using Generics to reduce interface allocations at the entry point.
func Equal[T any](a, b T) bool {
	va := reflect.ValueOf(&a).Elem()
	vb := reflect.ValueOf(&b).Elem()

	return ValueEqual(va, vb)
}

// ValueEqual performs a deep equality check between two reflect.Values.
// It is the internal engine for Equal and respects the same rules.
func ValueEqual(a, b reflect.Value) bool {
	if !a.IsValid() || !b.IsValid() {
		return a.IsValid() == b.IsValid()
	}

	if a.Type() != b.Type() {
		return false
	}

	// Basic pointer identity check for reference types.
	// This is a massive optimization for identical objects.
	kind := a.Kind()
	if kind == reflect.Pointer || kind == reflect.Slice || kind == reflect.Map {
		if a.IsNil() || b.IsNil() {
			return a.IsNil() == b.IsNil()
		}
		if a.Pointer() == b.Pointer() {
			return true
		}
	}

	visited := visitedPool.Get().(map[visitKey]bool)
	defer func() {
		for k := range visited {
			delete(visited, k)
		}
		visitedPool.Put(visited)
	}()

	return equalRecursive(a, b, visited)
}

func equalRecursive(a, b reflect.Value, visited map[visitKey]bool) bool {
	if !a.IsValid() || !b.IsValid() {
		return a.IsValid() == b.IsValid()
	}

	if a.Type() != b.Type() {
		return false
	}

	kind := a.Kind()

	// Fast path for basic types
	switch kind {
	case reflect.Bool:
		return a.Bool() == b.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return a.Int() == b.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return a.Uint() == b.Uint()
	case reflect.Float32, reflect.Float64:
		return a.Float() == b.Float()
	case reflect.Complex64, reflect.Complex128:
		return a.Complex() == b.Complex()
	case reflect.String:
		return a.String() == b.String()
	}

	// Cycle detection and pointer identity for recursive types
	if kind == reflect.Pointer || kind == reflect.Slice || kind == reflect.Map {
		if a.IsNil() || b.IsNil() {
			return a.IsNil() == b.IsNil()
		}
		ptrA := a.Pointer()
		ptrB := b.Pointer()
		if ptrA == ptrB {
			return true
		}
		
		k := visitKey{ptrA, ptrB, a.Type()}
		if visited[k] {
			return true
		}
		visited[k] = true
	}

	switch kind {
	case reflect.Pointer, reflect.Interface:
		return equalRecursive(a.Elem(), b.Elem(), visited)

	case reflect.Struct:
		info := getTypeInfo(a.Type())
		for _, fInfo := range info.fields {
			if fInfo.tag.ignore {
				continue
			}
			fA := a.Field(fInfo.index)
			fB := b.Field(fInfo.index)
			if !fA.CanInterface() {
				unsafe.DisableRO(&fA)
			}
			if !fB.CanInterface() {
				unsafe.DisableRO(&fB)
			}
			if !equalRecursive(fA, fB, visited) {
				return false
			}
		}
		return true

	case reflect.Slice, reflect.Array:
		if a.Len() != b.Len() {
			return false
		}
		for i := 0; i < a.Len(); i++ {
			if !equalRecursive(a.Index(i), b.Index(i), visited) {
				return false
			}
		}
		return true

	case reflect.Map:
		if a.Len() != b.Len() {
			return false
		}
		iter := a.MapRange()
		for iter.Next() {
			k := iter.Key()
			valA := iter.Value()
			valB := b.MapIndex(k)
			if !valB.IsValid() || !equalRecursive(valA, valB, visited) {
				return false
			}
		}
		return true

	case reflect.Func, reflect.Chan, reflect.UnsafePointer:
		return a.Pointer() == b.Pointer()

	default:
		// Fallback for remaining types
		if a.CanInterface() && b.CanInterface() {
			return reflect.DeepEqual(a.Interface(), b.Interface())
		}
		return false
	}
}
