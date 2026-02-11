package deep

import (
	"fmt"
	"reflect"

	"github.com/brunoga/deep/v2/internal/unsafe"
)

// DiffOption allows configuring the behavior of the Diff function.
type DiffOption interface {
	applyDiff(*diffConfig)
}

type diffOptionFunc func(*diffConfig)

func (f diffOptionFunc) applyDiff(c *diffConfig) {
	f(c)
}

type diffConfig struct {
	ignoredPaths map[string]bool
}

// DiffIgnorePath returns an option that tells Diff to ignore changes at the specified path.
// The path should use Go-style notation (e.g., "Field.SubField", "Map.Key", "Slice[0]").
func DiffIgnorePath(path string) DiffOption {
	return diffOptionFunc(func(c *diffConfig) {
		c.ignoredPaths[path] = true
	})
}

// Diff compares two values a and b and returns a Patch that can be applied
// to a to make it equal to b.
//
// It uses a combination of Myers' Diff algorithm for slices and recursive
// type-specific comparison for structs, maps, and pointers.
//
// If a and b are deeply equal, it returns nil.
func Diff[T any](a, b T, opts ...DiffOption) Patch[T] {
	config := &diffConfig{
		ignoredPaths: make(map[string]bool),
	}
	for _, opt := range opts {
		opt.applyDiff(config)
	}

	// We take the address of a and b to ensure that if T is an interface,
	// reflect.ValueOf doesn't "peek through" to the concrete type immediately,
	// preserving the interface wrapper which is important for ApplyChecked.
	va := reflect.ValueOf(&a).Elem()
	vb := reflect.ValueOf(&b).Elem()

	patch, err := diffRecursive(va, vb, make(map[visitKey]bool), config, "")
	if err != nil {
		panic(err)
	}

	if patch == nil {
		return nil
	}

	return &typedPatch[T]{
		inner:  patch,
		strict: true,
	}
}

// Differ is an interface that types can implement to provide their own
// custom diff logic. The type T in Diff(other T) (Patch[T], error) must be the
// same concrete type as the receiver that implements this interface.
type Differ[T any] interface {
	// Diff returns a patch that transforms the receiver into other.
	Diff(other T) (Patch[T], error)
}

type visitKey struct {
	a, b uintptr
	typ  reflect.Type
}

func diffRecursive(a, b reflect.Value, visited map[visitKey]bool, config *diffConfig, path string) (diffPatch, error) {
	if config.ignoredPaths[path] {
		return nil, nil
	}

	if !a.IsValid() && !b.IsValid() {
		return nil, nil
	}
	if !a.IsValid() || !b.IsValid() {
		if !b.IsValid() {
			return &valuePatch{oldVal: deepCopyValue(a), newVal: reflect.Value{}}, nil
		}
		return &valuePatch{oldVal: deepCopyValue(a), newVal: deepCopyValue(b)}, nil
	}

	if a.Type() != b.Type() {
		return &valuePatch{oldVal: deepCopyValue(a), newVal: deepCopyValue(b)}, nil
	}

	if a.CanInterface() && b.CanInterface() && reflect.DeepEqual(a.Interface(), b.Interface()) {
		return nil, nil
	}

	// Handle Differ interface.
	// NOTE: We use reflection to detect and call the Diff method because Differ[T]
	// is a generic interface. Since T is the concrete type implementing the
	// interface, we cannot easily perform a type assertion here without knowing
	// T at each step of the recursion. Furthermore, Go reflection doesn't allow
	// dynamic instantiation of generic interfaces. Searching for the method by
	// name and signature provides a flexible "duck-typing" approach that
	// preserves type safety for the user.
	if a.IsValid() && a.CanInterface() {
		kind := a.Kind()
		attemptDiffer := true
		if kind == reflect.Interface || kind == reflect.Ptr {
			if a.IsNil() || b.IsNil() {
				attemptDiffer = false
			}
		}

		if attemptDiffer {
			if kind == reflect.Struct || kind == reflect.Ptr {
				method := a.MethodByName("Diff")
				if method.IsValid() && method.Type().NumIn() == 1 && method.Type().NumOut() == 2 {
					if method.Type().In(0) == a.Type() &&
						method.Type().Out(1).Implements(reflect.TypeOf((*error)(nil)).Elem()) {
						res := method.Call([]reflect.Value{b})
						if !res[1].IsNil() {
							return nil, res[1].Interface().(error)
						}
						if res[0].IsNil() {
							return nil, nil
						}

						// If it implements patchUnwrapper, we can get the inner diffPatch.
						if unwrapper, ok := res[0].Interface().(patchUnwrapper); ok {
							return unwrapper.unwrap(), nil
						}

						// Otherwise, wrap it.
						return &customDiffPatch{
							patch: res[0].Interface(),
						}, nil
					}
				}
			}
		}
	}

	switch a.Kind() {
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32,
		reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32,
		reflect.Uint64, reflect.Uintptr, reflect.Float32, reflect.Float64,
		reflect.Complex64, reflect.Complex128, reflect.String:
		return &valuePatch{oldVal: deepCopyValue(a), newVal: deepCopyValue(b)}, nil

	case reflect.Ptr:
		return diffPtr(a, b, visited, config, path)
	case reflect.Interface:
		return diffInterface(a, b, visited, config, path)
	case reflect.Struct:
		return diffStruct(a, b, visited, config, path)
	case reflect.Slice:
		return diffSlice(a, b, visited, config, path)
	case reflect.Map:
		return diffMap(a, b, visited, config, path)
	case reflect.Array:
		return diffArray(a, b, visited, config, path)
	case reflect.Func, reflect.Chan, reflect.UnsafePointer:
		if a.IsNil() && b.IsNil() {
			return nil, nil
		}
		return &valuePatch{oldVal: deepCopyValue(a), newVal: deepCopyValue(b)}, nil
	default:
		return &valuePatch{oldVal: deepCopyValue(a), newVal: deepCopyValue(b)}, nil
	}
}

func diffPtr(a, b reflect.Value, visited map[visitKey]bool, config *diffConfig, path string) (diffPatch, error) {
	if a.IsNil() && b.IsNil() {
		return nil, nil
	}
	if a.IsNil() {
		return &valuePatch{oldVal: deepCopyValue(a), newVal: deepCopyValue(b)}, nil
	}
	if b.IsNil() {
		return &valuePatch{oldVal: deepCopyValue(a), newVal: reflect.Zero(a.Type())}, nil
	}

	k := visitKey{a.Pointer(), b.Pointer(), a.Type()}
	if visited[k] {
		return nil, nil
	}
	visited[k] = true

	elemPatch, err := diffRecursive(a.Elem(), b.Elem(), visited, config, path)
	if err != nil {
		return nil, err
	}
	if elemPatch == nil {
		return nil, nil
	}

	return &ptrPatch{elemPatch: elemPatch}, nil
}

func diffInterface(a, b reflect.Value, visited map[visitKey]bool, config *diffConfig, path string) (diffPatch, error) {
	if a.IsNil() && b.IsNil() {
		return nil, nil
	}
	if a.IsNil() || b.IsNil() {
		if !b.IsValid() {
			return &valuePatch{oldVal: deepCopyValue(a), newVal: reflect.Value{}}, nil
		}
		return &valuePatch{oldVal: deepCopyValue(a), newVal: deepCopyValue(b)}, nil
	}

	if a.Elem().Type() != b.Elem().Type() {
		return &valuePatch{oldVal: deepCopyValue(a), newVal: deepCopyValue(b)}, nil
	}

	elemPatch, err := diffRecursive(a.Elem(), b.Elem(), visited, config, path)
	if err != nil {
		return nil, err
	}
	if elemPatch == nil {
		return nil, nil
	}

	return &interfacePatch{elemPatch: elemPatch}, nil
}

func diffStruct(a, b reflect.Value, visited map[visitKey]bool, config *diffConfig, path string) (diffPatch, error) {
	fields := make(map[string]diffPatch)

	for i := 0; i < a.NumField(); i++ {
		fA := a.Field(i)
		fB := b.Field(i)

		if !fA.CanInterface() {
			unsafe.DisableRO(&fA)
		}
		if !fB.CanInterface() {
			unsafe.DisableRO(&fB)
		}

		fieldName := a.Type().Field(i).Name
		fieldPath := fieldName
		if path != "" {
			fieldPath = path + "." + fieldName
		}

		patch, err := diffRecursive(fA, fB, visited, config, fieldPath)
		if err != nil {
			return nil, err
		}
		if patch != nil {
			fields[fieldName] = patch
		}
	}

	if len(fields) == 0 {
		return nil, nil
	}

	return &structPatch{fields: fields}, nil
}

func diffArray(a, b reflect.Value, visited map[visitKey]bool, config *diffConfig, path string) (diffPatch, error) {
	indices := make(map[int]diffPatch)

	for i := 0; i < a.Len(); i++ {
		indexPath := fmt.Sprintf("%s[%d]", path, i)
		patch, err := diffRecursive(a.Index(i), b.Index(i), visited, config, indexPath)
		if err != nil {
			return nil, err
		}
		if patch != nil {
			indices[i] = patch
		}
	}

	if len(indices) == 0 {
		return nil, nil
	}

	return &arrayPatch{indices: indices}, nil
}

func diffMap(a, b reflect.Value, visited map[visitKey]bool, config *diffConfig, path string) (diffPatch, error) {
	if a.IsNil() && b.IsNil() {
		return nil, nil
	}
	if a.IsNil() || b.IsNil() {
		if !b.IsValid() {
			return &valuePatch{oldVal: deepCopyValue(a), newVal: reflect.Value{}}, nil
		}
		return &valuePatch{oldVal: deepCopyValue(a), newVal: deepCopyValue(b)}, nil
	}

	added := make(map[interface{}]reflect.Value)
	removed := make(map[interface{}]reflect.Value)
	modified := make(map[interface{}]diffPatch)

	iterA := a.MapRange()
	for iterA.Next() {
		k := iterA.Key()
		vA := iterA.Value()

		keyPath := fmt.Sprintf("%s.%v", path, k.Interface())
		if path == "" {
			keyPath = fmt.Sprintf("%v", k.Interface())
		}

		vB := b.MapIndex(k)
		if !vB.IsValid() {
			if !config.ignoredPaths[keyPath] {
				removed[k.Interface()] = deepCopyValue(vA)
			}
		} else {
			patch, err := diffRecursive(vA, vB, visited, config, keyPath)
			if err != nil {
				return nil, err
			}
			if patch != nil {
				modified[k.Interface()] = patch
			}
		}
	}

	iterB := b.MapRange()
	for iterB.Next() {
		k := iterB.Key()
		vB := iterB.Value()

		vA := a.MapIndex(k)
		if !vA.IsValid() {
			keyPath := fmt.Sprintf("%s.%v", path, k.Interface())
			if path == "" {
				keyPath = fmt.Sprintf("%v", k.Interface())
			}
			if !config.ignoredPaths[keyPath] {
				added[k.Interface()] = deepCopyValue(vB)
			}
		}
	}

	if len(added) == 0 && len(removed) == 0 && len(modified) == 0 {
		return nil, nil
	}

	return &mapPatch{
		added:    added,
		removed:  removed,
		modified: modified,
		keyType:  a.Type().Key(),
	}, nil
}

func diffSlice(a, b reflect.Value, visited map[visitKey]bool, config *diffConfig, path string) (diffPatch, error) {
	if a.IsNil() && b.IsNil() {
		return nil, nil
	}
	if a.IsNil() || b.IsNil() {
		if !b.IsValid() {
			return &valuePatch{oldVal: deepCopyValue(a), newVal: reflect.Value{}}, nil
		}
		return &valuePatch{oldVal: deepCopyValue(a), newVal: deepCopyValue(b)}, nil
	}

	lenA := a.Len()
	lenB := b.Len()

	// 1. Identify common prefix
	prefix := 0
	for prefix < lenA && prefix < lenB {
		vA := a.Index(prefix)
		vB := b.Index(prefix)
		if reflect.DeepEqual(vA.Interface(), vB.Interface()) {
			prefix++
		} else {
			break
		}
	}

	// 2. Identify common suffix
	suffix := 0
	for suffix < (lenA-prefix) && suffix < (lenB-prefix) {
		vA := a.Index(lenA - 1 - suffix)
		vB := b.Index(lenB - 1 - suffix)
		if reflect.DeepEqual(vA.Interface(), vB.Interface()) {
			suffix++
		} else {
			break
		}
	}

	midAStart := prefix
	midAEnd := lenA - suffix
	midBStart := prefix
	midBEnd := lenB - suffix

	// Fast path: Simple append
	if midAStart == midAEnd && midBStart < midBEnd {
		var ops []sliceOp
		for i := midBStart; i < midBEnd; i++ {
			ops = append(ops, sliceOp{
				Kind:  opAdd,
				Index: i,
				Val:   deepCopyValue(b.Index(i)),
			})
		}
		return &slicePatch{ops: ops}, nil
	}

	// Fast path: Simple removal
	if midBStart == midBEnd && midAStart < midAEnd {
		var ops []sliceOp
		for i := midAStart; i < midAEnd; i++ {
			ops = append(ops, sliceOp{
				Kind:  opDel,
				Index: i,
				Val:   deepCopyValue(a.Index(i)),
			})
		}
		return &slicePatch{ops: ops}, nil
	}

	if midAStart >= midAEnd && midBStart >= midBEnd {
		return nil, nil
	}

	// 3. Diff the middle part
	ops := computeSliceEdits(a, b, midAStart, midAEnd, midBStart, midBEnd)

	return &slicePatch{ops: ops}, nil
}

// computeSliceEdits uses dynamic programming to find the shortest edit script
// for the middle portion of two slices.
func computeSliceEdits(a, b reflect.Value, aStart, aEnd, bStart, bEnd int) []sliceOp {
	n := aEnd - aStart
	m := bEnd - bStart

	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}

	for i := 0; i <= n; i++ {
		dp[i][0] = i
	}
	for j := 0; j <= m; j++ {
		dp[0][j] = j
	}

	for i := 1; i <= n; i++ {
		for j := 1; j <= m; j++ {
			vA := a.Index(aStart + i - 1)
			vB := b.Index(bStart + j - 1)

			cost := 1
			if reflect.DeepEqual(vA.Interface(), vB.Interface()) {
				cost = 0
			}

			delCost := dp[i-1][j] + 1
			insCost := dp[i][j-1] + 1
			subCost := dp[i-1][j-1] + cost

			min := delCost
			if insCost < min {
				min = insCost
			}
			if subCost < min {
				min = subCost
			}
			dp[i][j] = min
		}
	}

	var ops []sliceOp
	i, j := n, m
	for i > 0 || j > 0 {
		if i > 0 && j > 0 {
			vA := a.Index(aStart + i - 1)
			vB := b.Index(bStart + j - 1)

			cost := 1
			if reflect.DeepEqual(vA.Interface(), vB.Interface()) {
				cost = 0
			}

			if dp[i][j] == dp[i-1][j-1]+cost {
				if cost == 1 {
					// NOTE: We pass empty config here for now as slice edits don't support
					// nested path-based ignoring easily yet without knowing the final index.
					p, _ := diffRecursive(vA, vB, make(map[visitKey]bool), &diffConfig{ignoredPaths: make(map[string]bool)}, "")
					ops = append(ops, sliceOp{
						Kind:  opMod,
						Index: aStart + i - 1,
						Patch: p,
					})
				}
				i--
				j--
				continue
			}
		}

		if i > 0 && dp[i][j] == dp[i-1][j]+1 {
			ops = append(ops, sliceOp{
				Kind:  opDel,
				Index: aStart + i - 1,
				Val:   deepCopyValue(a.Index(aStart + i - 1)),
			})
			i--
			continue
		}

		if j > 0 && dp[i][j] == dp[i][j-1]+1 {
			ops = append(ops, sliceOp{
				Kind:  opAdd,
				Index: aStart + i,
				Val:   deepCopyValue(b.Index(bStart + j - 1)),
			})
			j--
			continue
		}
	}

	for k := 0; k < len(ops)/2; k++ {
		ops[k], ops[len(ops)-1-k] = ops[len(ops)-1-k], ops[k]
	}

	return ops
}
