package deep

import (
	"fmt"
	"reflect"
	"strconv"

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
func DiffIgnorePath(path string) DiffOption {
	return diffOptionFunc(func(c *diffConfig) {
		c.ignoredPaths[NormalizePath(path)] = true
	})
}

// Keyer is an interface that types can implement to provide a canonical
// representation for map keys. This allows semantic equality checks for
// complex map keys.
type Keyer interface {
	CanonicalKey() any
}

// Differ is a stateless engine for calculating patches between two values.
type Differ struct {
	config      *diffConfig
	customDiffs map[reflect.Type]func(a, b reflect.Value, ctx *diffContext) (diffPatch, error)
}

// NewDiffer creates a new Differ with the given options.
func NewDiffer(opts ...DiffOption) *Differ {
	config := &diffConfig{
		ignoredPaths: make(map[string]bool),
	}
	for _, opt := range opts {
		opt.applyDiff(config)
	}
	return &Differ{
		config:      config,
		customDiffs: make(map[reflect.Type]func(a, b reflect.Value, ctx *diffContext) (diffPatch, error)),
	}
}

// diffContext holds transient state for a single Diff execution.
type diffContext struct {
	valueIndex map[any]string
	visited    map[visitKey]bool
}

// RegisterCustomDiff registers a custom diff function for a specific type.
func RegisterCustomDiff[T any](d *Differ, fn func(a, b T) (Patch[T], error)) {
	var t T
	typ := reflect.TypeOf(t)
	d.customDiffs[typ] = func(a, b reflect.Value, ctx *diffContext) (diffPatch, error) {
		p, err := fn(a.Interface().(T), b.Interface().(T))
		if err != nil {
			return nil, err
		}
		if p == nil {
			return nil, nil
		}
		if unwrapper, ok := p.(patchUnwrapper); ok {
			return unwrapper.unwrap(), nil
		}
		return &customDiffPatch{patch: p}, nil
	}
}

// Diff compares two values a and b and returns a Patch that can be applied.
func (d *Differ) Diff(a, b any) (diffPatch, error) {
	va := reflect.ValueOf(&a).Elem()
	vb := reflect.ValueOf(&b).Elem()

	ctx := &diffContext{
		valueIndex: make(map[any]string),
		visited:    make(map[visitKey]bool),
	}

	d.indexValues(va, "/", make(map[uintptr]bool), ctx)

	return d.diffRecursive(va, vb, "/", false, ctx)
}

// DiffTyped is a generic wrapper for Diff that returns a typed Patch[T].
func DiffTyped[T any](d *Differ, a, b T) Patch[T] {
	va := reflect.ValueOf(&a).Elem()
	vb := reflect.ValueOf(&b).Elem()

	ctx := &diffContext{
		valueIndex: make(map[any]string),
		visited:    make(map[visitKey]bool),
	}

	d.indexValues(va, "/", make(map[uintptr]bool), ctx)

	patch, err := d.diffRecursive(va, vb, "/", false, ctx)
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

// Diff compares two values a and b and returns a Patch that can be applied.
func Diff[T any](a, b T, opts ...DiffOption) Patch[T] {
	d := NewDiffer(opts...)
	return DiffTyped(d, a, b)
}

type visitKey struct {
	a, b uintptr
	typ  reflect.Type
}

func isHashable(v reflect.Value) bool {
	kind := v.Kind()
	switch kind {
	case reflect.Slice, reflect.Map, reflect.Func:
		return false
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if !isHashable(v.Field(i)) {
				return false
			}
		}
	case reflect.Array:
		for i := 0; i < v.Len(); i++ {
			if !isHashable(v.Index(i)) {
				return false
			}
		}
	}
	return true
}

func (d *Differ) indexValues(v reflect.Value, path string, visited map[uintptr]bool, ctx *diffContext) {
	if !v.IsValid() {
		return
	}

	kind := v.Kind()
	if kind == reflect.Pointer || kind == reflect.Interface {
		if v.IsNil() {
			return
		}
		if kind == reflect.Pointer {
			ptr := v.Pointer()
			if visited[ptr] {
				return
			}
			visited[ptr] = true
		}
		d.indexValues(v.Elem(), path, visited, ctx)
		return
	}

	iv := v
	if iv.IsValid() && iv.CanInterface() {
		val := iv.Interface()
		if isHashable(iv) {
			switch iv.Kind() {
			case reflect.Struct, reflect.Slice, reflect.Map:
				if _, ok := ctx.valueIndex[val]; !ok {
					ctx.valueIndex[val] = path
				}
			}
		}
	}

	switch kind {
	case reflect.Struct:
		info := getTypeInfo(v.Type())
		for _, fInfo := range info.fields {
			if fInfo.tag.ignore {
				continue
			}
			d.indexValues(v.Field(fInfo.index), JoinPath(path, fInfo.name), visited, ctx)
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			d.indexValues(v.Index(i), JoinPath(path, strconv.Itoa(i)), visited, ctx)
		}
	case reflect.Map:
		iter := v.MapRange()
		for iter.Next() {
			k := iter.Key()
			ck := k.Interface()
			if keyer, ok := ck.(Keyer); ok {
				ck = keyer.CanonicalKey()
			}
			d.indexValues(iter.Value(), JoinPath(path, fmt.Sprintf("%v", ck)), visited, ctx)
		}
	}
}

func (d *Differ) diffRecursive(a, b reflect.Value, path string, atomic bool, ctx *diffContext) (diffPatch, error) {
	if d.config.ignoredPaths[path] {
		return nil, nil
	}

	if !a.IsValid() && !b.IsValid() {
		return nil, nil
	}

	if atomic {
		if a.CanInterface() && b.CanInterface() && reflect.DeepEqual(a.Interface(), b.Interface()) {
			return nil, nil
		}
		return &valuePatch{oldVal: deepCopyValue(a), newVal: deepCopyValue(b)}, nil
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

	if fn, ok := d.customDiffs[a.Type()]; ok {
		return fn(a, b, ctx)
	}

	// Move/Copy Detection
	ivb := b
	for ivb.Kind() == reflect.Pointer || ivb.Kind() == reflect.Interface {
		if ivb.IsNil() {
			break
		}
		ivb = ivb.Elem()
	}

	if ivb.IsValid() && ivb.CanInterface() && isHashable(ivb) {
		kind := ivb.Kind()
		if kind == reflect.Struct || kind == reflect.Slice || kind == reflect.Map {
			if fromPath, ok := ctx.valueIndex[ivb.Interface()]; ok && fromPath != path {
				return &copyPatch{from: fromPath}, nil
			}
		}
	}

	if a.CanInterface() {
		if a.Kind() == reflect.Struct || a.Kind() == reflect.Pointer {
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
					if unwrapper, ok := res[0].Interface().(patchUnwrapper); ok {
						return unwrapper.unwrap(), nil
					}
					return &customDiffPatch{patch: res[0].Interface()}, nil
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

	case reflect.Pointer:
		return d.diffPtr(a, b, path, ctx)
	case reflect.Interface:
		return d.diffInterface(a, b, path, ctx)
	case reflect.Struct:
		return d.diffStruct(a, b, path, ctx)
	case reflect.Slice:
		return d.diffSlice(a, b, path, ctx)
	case reflect.Map:
		return d.diffMap(a, b, path, ctx)
	case reflect.Array:
		return d.diffArray(a, b, path, ctx)
	default:
		if a.Kind() == reflect.Func || a.Kind() == reflect.Chan || a.Kind() == reflect.UnsafePointer {
			if a.IsNil() && b.IsNil() {
				return nil, nil
			}
		}
		return &valuePatch{oldVal: deepCopyValue(a), newVal: deepCopyValue(b)}, nil
	}
}

func (d *Differ) diffPtr(a, b reflect.Value, path string, ctx *diffContext) (diffPatch, error) {
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
	if ctx.visited[k] {
		return nil, nil
	}
	ctx.visited[k] = true

	elemPatch, err := d.diffRecursive(a.Elem(), b.Elem(), path, false, ctx)
	if err != nil {
		return nil, err
	}
	if elemPatch == nil {
		return nil, nil
	}

	return &ptrPatch{elemPatch: elemPatch}, nil
}

func (d *Differ) diffInterface(a, b reflect.Value, path string, ctx *diffContext) (diffPatch, error) {
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

	elemPatch, err := d.diffRecursive(a.Elem(), b.Elem(), path, false, ctx)
	if err != nil {
		return nil, err
	}
	if elemPatch == nil {
		return nil, nil
	}

	return &interfacePatch{elemPatch: elemPatch}, nil
}

func (d *Differ) diffStruct(a, b reflect.Value, path string, ctx *diffContext) (diffPatch, error) {
	fields := make(map[string]diffPatch)
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

		fieldName := fInfo.name
		fieldPath := JoinPath(path, fieldName)

		patch, err := d.diffRecursive(fA, fB, fieldPath, fInfo.tag.atomic, ctx)
		if err != nil {
			return nil, err
		}
		if patch != nil {
			if fInfo.tag.readOnly {
				patch = &readOnlyPatch{inner: patch}
			}
			fields[fieldName] = patch
		}
	}

	if len(fields) == 0 {
		return nil, nil
	}

	return &structPatch{fields: fields}, nil
}

func (d *Differ) diffArray(a, b reflect.Value, path string, ctx *diffContext) (diffPatch, error) {
	indices := make(map[int]diffPatch)

	for i := 0; i < a.Len(); i++ {
		indexPath := JoinPath(path, strconv.Itoa(i))
		patch, err := d.diffRecursive(a.Index(i), b.Index(i), indexPath, false, ctx)
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

func (d *Differ) diffMap(a, b reflect.Value, path string, ctx *diffContext) (diffPatch, error) {
	if a.IsNil() && b.IsNil() {
		return nil, nil
	}
	if a.IsNil() || b.IsNil() {
		if !b.IsValid() {
			return &valuePatch{oldVal: deepCopyValue(a), newVal: reflect.Value{}}, nil
		}
		return &valuePatch{oldVal: deepCopyValue(a), newVal: deepCopyValue(b)}, nil
	}

	added := make(map[any]reflect.Value)
	removed := make(map[any]reflect.Value)
	modified := make(map[any]diffPatch)
	originalKeys := make(map[any]any)

	getCanonical := func(v reflect.Value) any {
		if v.CanInterface() {
			val := v.Interface()
			if k, ok := val.(Keyer); ok {
				ck := k.CanonicalKey()
				originalKeys[ck] = val
				return ck
			}
			return val
		}
		return v.Interface()
	}

	bByCanonical := make(map[any]reflect.Value)
	iterB := b.MapRange()
	for iterB.Next() {
		bByCanonical[getCanonical(iterB.Key())] = iterB.Value()
	}

	iterA := a.MapRange()
	for iterA.Next() {
		k := iterA.Key()
		vA := iterA.Value()
		ck := getCanonical(k)

		keyPath := JoinPath(path, fmt.Sprintf("%v", ck))

		vB, found := bByCanonical[ck]
		if !found {
			if !d.config.ignoredPaths[keyPath] {
				removed[ck] = deepCopyValue(vA)
			}
		} else {
			patch, err := d.diffRecursive(vA, vB, keyPath, false, ctx)
			if err != nil {
				return nil, err
			}
			if patch != nil {
				modified[ck] = patch
			}
			delete(bByCanonical, ck)
		}
	}

	for ck, vB := range bByCanonical {
		keyPath := JoinPath(path, fmt.Sprintf("%v", ck))
		if !d.config.ignoredPaths[keyPath] {
			if vB.CanInterface() {
				ivb := vB
				for ivb.Kind() == reflect.Pointer || ivb.Kind() == reflect.Interface {
					if ivb.IsNil() {
						break
					}
					ivb = ivb.Elem()
				}
				if ivb.IsValid() && isHashable(ivb) {
					if fromPath, ok := ctx.valueIndex[ivb.Interface()]; ok && fromPath != keyPath {
						modified[ck] = &copyPatch{from: fromPath}
						delete(bByCanonical, ck)
						continue
					}
				}
			}
			added[ck] = deepCopyValue(vB)
		}
	}

	if len(added) == 0 && len(removed) == 0 && len(modified) == 0 {
		return nil, nil
	}

	return &mapPatch{
		added:        added,
		removed:      removed,
		modified:     modified,
		originalKeys: originalKeys,
		keyType:      a.Type().Key(),
	}, nil
}

func (d *Differ) diffSlice(a, b reflect.Value, path string, ctx *diffContext) (diffPatch, error) {
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

	keyField, hasKey := getKeyField(a.Type().Elem())

	if midAStart == midAEnd && midBStart < midBEnd {
		var ops []sliceOp
		for i := midBStart; i < midBEnd; i++ {
			var prevKey any
			if hasKey {
				if i > 0 {
					prevKey = extractKey(b.Index(i-1), keyField)
				}
			}
			op := sliceOp{
				Kind:    OpAdd,
				Index:   i,
				Val:     deepCopyValue(b.Index(i)),
				PrevKey: prevKey,
			}
			if hasKey {
				op.Key = extractKey(b.Index(i), keyField)
			}
			ops = append(ops, op)
		}
		return &slicePatch{ops: ops}, nil
	}

	if midBStart == midBEnd && midAStart < midAEnd {
		var ops []sliceOp
		for i := midAStart; i < midAEnd; i++ {
			op := sliceOp{
				Kind:  OpRemove,
				Index: i,
				Val:   deepCopyValue(a.Index(i)),
			}
			if hasKey {
				op.Key = extractKey(a.Index(i), keyField)
			}
			ops = append(ops, op)
		}
		return &slicePatch{ops: ops}, nil
	}

	if midAStart >= midAEnd && midBStart >= midBEnd {
		return nil, nil
	}

	ops := d.computeSliceEdits(a, b, midAStart, midAEnd, midBStart, midBEnd, keyField, hasKey, path, ctx)

	return &slicePatch{ops: ops}, nil
}

func (d *Differ) computeSliceEdits(a, b reflect.Value, aStart, aEnd, bStart, bEnd, keyField int, hasKey bool, path string, ctx *diffContext) []sliceOp {
	n := aEnd - aStart
	m := bEnd - bStart

	same := func(v1, v2 reflect.Value) bool {
		if hasKey {
			k1 := v1
			k2 := v2
			if k1.Kind() == reflect.Pointer {
				if k1.IsNil() || k2.IsNil() {
					return k1.IsNil() && k2.IsNil()
				}
				k1 = k1.Elem()
				k2 = k2.Elem()
			}
			return reflect.DeepEqual(k1.Field(keyField).Interface(), k2.Field(keyField).Interface())
		}
		return reflect.DeepEqual(v1.Interface(), v2.Interface())
	}

	max := n + m
	v := make([]int, 2*max+1)
	offset := max
	trace := [][]int{}

	for dStep := 0; dStep <= max; dStep++ {
		vc := make([]int, 2*max+1)
		copy(vc, v)
		trace = append(trace, vc)

		for k := -dStep; k <= dStep; k += 2 {
			var x int
			if k == -dStep || (k != dStep && v[k-1+offset] < v[k+1+offset]) {
				x = v[k+1+offset]
			} else {
				x = v[k-1+offset] + 1
			}
			y := x - k
			for x < n && y < m && same(a.Index(aStart+x), b.Index(bStart+y)) {
				x++
				y++
			}
			v[k+offset] = x
			if x >= n && y >= m {
				return d.backtrackMyers(a, b, aStart, aEnd, bStart, bEnd, keyField, hasKey, trace, path, ctx)
			}
		}
	}

	return nil
}

func (d *Differ) backtrackMyers(a, b reflect.Value, aStart, aEnd, bStart, bEnd, keyField int, hasKey bool, trace [][]int, path string, ctx *diffContext) []sliceOp {
	var ops []sliceOp
	x, y := aEnd-aStart, bEnd-bStart
	offset := (aEnd - aStart) + (bEnd - bStart)

	for dStep := len(trace) - 1; dStep > 0; dStep-- {
		v := trace[dStep]
		k := x - y
		
		var prevK int
		if k == -dStep || (k != dStep && v[k-1+offset] < v[k+1+offset]) {
			prevK = k + 1
		} else {
			prevK = k - 1
		}
		
		prevX := v[prevK+offset]
		prevY := prevX - prevK

		for x > prevX && y > prevY {
			vA := a.Index(aStart + x - 1)
			vB := b.Index(bStart + y - 1)
			if !reflect.DeepEqual(vA.Interface(), vB.Interface()) {
				p, _ := d.diffRecursive(vA, vB, JoinPath(path, fmt.Sprintf("%v", extractKey(vA, keyField))), false, ctx)
				op := sliceOp{
					Kind:  OpReplace,
					Index: aStart + x - 1,
					Patch: p,
				}
				if hasKey {
					op.Key = extractKey(vA, keyField)
				}
				ops = append(ops, op)
			}
			x--
			y--
		}

		if x > prevX {
			op := sliceOp{
				Kind:  OpRemove,
				Index: aStart + x - 1,
				Val:   deepCopyValue(a.Index(aStart + x - 1)),
			}
			if hasKey {
				op.Key = extractKey(a.Index(aStart+x-1), keyField)
			}
			ops = append(ops, op)
		} else if y > prevY {
			var prevKey any
			if hasKey && (bStart+y-2 >= 0) {
				prevKey = extractKey(b.Index(bStart+y-2), keyField)
			}
			val := b.Index(bStart + y - 1)

			// Move/Copy Detection for slice additions
			if val.CanInterface() {
				iv := val
				for iv.Kind() == reflect.Pointer || iv.Kind() == reflect.Interface {
					if iv.IsNil() {
						break
					}
					iv = iv.Elem()
				}
				if iv.IsValid() && isHashable(iv) {
					if fromPath, ok := ctx.valueIndex[iv.Interface()]; ok {
						op := sliceOp{
							Kind:    OpCopy,
							Index:   aStart + x,
							Patch:   &copyPatch{from: fromPath},
							PrevKey: prevKey,
						}
						if hasKey {
							op.Key = extractKey(val, keyField)
						}
						ops = append(ops, op)
						x, y = prevX, prevY
						continue
					}
				}
			}

			op := sliceOp{
				Kind:    OpAdd,
				Index:   aStart + x,
				Val:     deepCopyValue(val),
				PrevKey: prevKey,
			}
			if hasKey {
				op.Key = extractKey(b.Index(bStart+y-1), keyField)
			}
			ops = append(ops, op)
		}
		x, y = prevX, prevY
	}

	for x > 0 && y > 0 {
		vA := a.Index(aStart + x - 1)
		vB := b.Index(bStart + y - 1)
		if !reflect.DeepEqual(vA.Interface(), vB.Interface()) {
			p, _ := d.diffRecursive(vA, vB, JoinPath(path, fmt.Sprintf("%v", extractKey(vA, keyField))), false, ctx)
			op := sliceOp{
				Kind:  OpReplace,
				Index: aStart + x - 1,
				Patch: p,
			}
			if hasKey {
				op.Key = extractKey(vA, keyField)
			}
			ops = append(ops, op)
		}
		x--
		y--
	}

	for i := 0; i < len(ops)/2; i++ {
		ops[i], ops[len(ops)-1-i] = ops[len(ops)-1-i], ops[i]
	}

	return ops
}
