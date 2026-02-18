package core

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"

	"github.com/brunoga/deep/v4/internal/unsafe"
)

// EqualOption allows configuring the behavior of the Equal function.
type EqualOption interface {
	applyEqual(*equalConfig)
}

type equalConfig struct {
	ignoredPaths map[string]bool
}

type equalOptionFunc func(*equalConfig)

func (f equalOptionFunc) applyEqual(c *equalConfig) {
	f(c)
}

// EqualIgnorePath returns an option that tells Equal to ignore the specified path.
func EqualIgnorePath(path string) EqualOption {
	return equalOptionFunc(func(c *equalConfig) {
		if c.ignoredPaths == nil {
			c.ignoredPaths = make(map[string]bool)
		}
		c.ignoredPaths[NormalizePath(path)] = true
	})
}

var (
	customEqualFuncs = make(map[reflect.Type]reflect.Value)
	muEqual          sync.RWMutex
)

// RegisterCustomEqual registers a custom equality function for a specific type.
// The function must be of type func(a, b T) bool.
func RegisterCustomEqual(typ reflect.Type, fn reflect.Value) {
	muEqual.Lock()
	defer muEqual.Unlock()
	customEqualFuncs[typ] = fn
}

type VisitKey struct {
	A, B uintptr
	Typ  reflect.Type
}

var visitedPool = sync.Pool{
	New: func() any {
		return make(map[VisitKey]bool)
	},
}

// Equal performs a deep equality check between a and b.
func Equal[T any](a, b T, opts ...EqualOption) bool {
	config := &equalConfig{}
	for _, opt := range opts {
		opt.applyEqual(config)
	}

	va := reflect.ValueOf(&a).Elem()
	vb := reflect.ValueOf(&b).Elem()
	return ValueEqual(va, vb, config)
}

// ValueEqual performs a deep equality check between two reflect.Values.
func ValueEqual(a, b reflect.Value, config *equalConfig) bool {
	visited := visitedPool.Get().(map[VisitKey]bool)
	defer func() {
		for k := range visited {
			delete(visited, k)
		}
		visitedPool.Put(visited)
	}()

	var pathStack []string
	if config != nil && len(config.ignoredPaths) > 0 {
		pathStack = make([]string, 0, 8)
	}

	return equalRecursive(a, b, visited, config, pathStack)
}

func equalRecursive(a, b reflect.Value, visited map[VisitKey]bool, config *equalConfig, pathStack []string) bool {
	if pathStack != nil {
		currentPath := buildPath(pathStack)
		if config.ignoredPaths[currentPath] {
			return true
		}
	}

	if !a.IsValid() || !b.IsValid() {
		return a.IsValid() == b.IsValid()
	}

	if a.Type() != b.Type() {
		return false
	}

	muEqual.RLock()
	fn, ok := customEqualFuncs[a.Type()]
	muEqual.RUnlock()
	if ok {
		res := fn.Call([]reflect.Value{a, b})
		return res[0].Bool()
	}

	kind := a.Kind()

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

	if kind == reflect.Pointer || kind == reflect.Slice || kind == reflect.Map {
		if a.IsNil() || b.IsNil() {
			return a.IsNil() == b.IsNil()
		}
		ptrA := a.Pointer()
		ptrB := b.Pointer()
		if ptrA == ptrB {
			return true
		}
		
		k := VisitKey{ptrA, ptrB, a.Type()}
		if visited[k] {
			return true
		}
		visited[k] = true
	}

	switch kind {
	case reflect.Pointer, reflect.Interface:
		return equalRecursive(a.Elem(), b.Elem(), visited, config, pathStack)

	case reflect.Struct:
		info := GetTypeInfo(a.Type())
		for _, fInfo := range info.Fields {
			if fInfo.Tag.Ignore {
				continue
			}
			fA := a.Field(fInfo.Index)
			fB := b.Field(fInfo.Index)
			if !fA.CanInterface() {
				unsafe.DisableRO(&fA)
			}
			if !fB.CanInterface() {
				unsafe.DisableRO(&fB)
			}
			
			var newStack []string
			if pathStack != nil {
				newStack = append(pathStack, fInfo.Name)
			}
			if !equalRecursive(fA, fB, visited, config, newStack) {
				return false
			}
		}
		return true

	case reflect.Slice, reflect.Array:
		if a.Len() != b.Len() {
			return false
		}
		for i := 0; i < a.Len(); i++ {
			var newStack []string
			if pathStack != nil {
				newStack = append(pathStack, strconv.Itoa(i))
			}
			if !equalRecursive(a.Index(i), b.Index(i), visited, config, newStack) {
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
			if !valB.IsValid() {
				return false
			}
			
			var newStack []string
			if pathStack != nil {
				// Normalize map key path construction as in copy.go
				kStr := fmt.Sprintf("%v", k.Interface())
				kStr = strings.ReplaceAll(kStr, "~", "~0")
				kStr = strings.ReplaceAll(kStr, "/", "~1")
				newStack = append(pathStack, kStr)
			}
			if !equalRecursive(valA, valB, visited, config, newStack) {
				return false
			}
		}
		return true

	case reflect.Func, reflect.Chan, reflect.UnsafePointer:
		return a.Pointer() == b.Pointer()

	default:
		if a.CanInterface() && b.CanInterface() {
			return reflect.DeepEqual(a.Interface(), b.Interface())
		}
		return false
	}
}

func buildPath(stack []string) string {
	var b strings.Builder
	b.WriteByte('/')
	for i, s := range stack {
		if i > 0 {
			b.WriteByte('/')
		}
		b.WriteString(s)
	}
	return b.String()
}
