package deep

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"

	"github.com/brunoga/deep/v3/internal/unsafe"
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
	detectMoves  bool
}

// DiffIgnorePath returns an option that tells Diff to ignore changes at the specified path.
func DiffIgnorePath(path string) DiffOption {
	return diffOptionFunc(func(c *diffConfig) {
		c.ignoredPaths[normalizePath(path)] = true
	})
}

// DiffDetectMoves returns an option that enables move and copy detection.
func DiffDetectMoves(enable bool) DiffOption {
	return diffOptionFunc(func(c *diffConfig) {
		c.detectMoves = enable
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
	movedPaths map[string]bool
	visited    map[visitKey]bool
	pathStack  []string
	rootB      reflect.Value
}

var diffContextPool = sync.Pool{
	New: func() any {
		return &diffContext{
			valueIndex: make(map[any]string),
			movedPaths: make(map[string]bool),
			visited:    make(map[visitKey]bool),
			pathStack:  make([]string, 0, 32),
		}
	},
}

func getDiffContext() *diffContext {
	return diffContextPool.Get().(*diffContext)
}

func releaseDiffContext(ctx *diffContext) {
	for k := range ctx.valueIndex {
		delete(ctx.valueIndex, k)
	}
	for k := range ctx.movedPaths {
		delete(ctx.movedPaths, k)
	}
	for k := range ctx.visited {
		delete(ctx.visited, k)
	}
	ctx.pathStack = ctx.pathStack[:0]
	ctx.rootB = reflect.Value{}
	diffContextPool.Put(ctx)
}

func (ctx *diffContext) buildPath() string {
	var b strings.Builder
	b.WriteByte('/')
	for i, s := range ctx.pathStack {
		if i > 0 {
			b.WriteByte('/')
		}
		b.WriteString(s)
	}
	return b.String()
}

var (
	defaultDiffer = NewDiffer()
	mu            sync.RWMutex
)

// RegisterCustomDiff registers a custom diff function for a specific type.
func RegisterCustomDiff[T any](d *Differ, fn func(a, b T) (Patch[T], error)) {
	if d == nil {
		mu.Lock()
		d = defaultDiffer
		mu.Unlock()
	}
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

	ctx := getDiffContext()
	defer releaseDiffContext(ctx)
	ctx.rootB = vb

	if d.config.detectMoves {
		d.indexValues(va, ctx)
		d.detectMovesRecursive(vb, ctx)
	}

	return d.diffRecursive(va, vb, false, ctx)
}

func (d *Differ) detectMovesRecursive(v reflect.Value, ctx *diffContext) {
	if !v.IsValid() {
		return
	}

	key := getHashKey(v)
	if key != nil && isHashable(v) {
		if fromPath, ok := ctx.valueIndex[key]; ok {
			currentPath := ctx.buildPath()
			if fromPath != currentPath {
				if isMove, checked := ctx.movedPaths[fromPath]; checked {
					// Already checked this source path.
					_ = isMove
				} else {
					if !d.isValueAtTarget(fromPath, v.Interface(), ctx) {
						ctx.movedPaths[fromPath] = true
					} else {
						ctx.movedPaths[fromPath] = false
					}
				}
			}
		}
	}

	switch v.Kind() {
	case reflect.Struct:
		info := getTypeInfo(v.Type())
		for _, fInfo := range info.fields {
			if fInfo.tag.ignore {
				continue
			}
			ctx.pathStack = append(ctx.pathStack, fInfo.name)
			d.detectMovesRecursive(v.Field(fInfo.index), ctx)
			ctx.pathStack = ctx.pathStack[:len(ctx.pathStack)-1]
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			ctx.pathStack = append(ctx.pathStack, strconv.Itoa(i))
			d.detectMovesRecursive(v.Index(i), ctx)
			ctx.pathStack = ctx.pathStack[:len(ctx.pathStack)-1]
		}
	case reflect.Map:
		iter := v.MapRange()
		for iter.Next() {
			k := iter.Key()
			ck := k.Interface()
			if keyer, ok := ck.(Keyer); ok {
				ck = keyer.CanonicalKey()
			}
			ctx.pathStack = append(ctx.pathStack, fmt.Sprintf("%v", ck))
			d.detectMovesRecursive(iter.Value(), ctx)
			ctx.pathStack = ctx.pathStack[:len(ctx.pathStack)-1]
		}
	case reflect.Pointer, reflect.Interface:
		if !v.IsNil() {
			d.detectMovesRecursive(v.Elem(), ctx)
		}
	}
}

// DiffTyped is a generic wrapper for Diff that returns a typed Patch[T].
func DiffTyped[T any](d *Differ, a, b T) Patch[T] {
	va := reflect.ValueOf(&a).Elem()
	vb := reflect.ValueOf(&b).Elem()

	ctx := getDiffContext()
	defer releaseDiffContext(ctx)
	ctx.rootB = vb

	if d.config.detectMoves {
		d.indexValues(va, ctx)
		d.detectMovesRecursive(vb, ctx)
	}

	patch, err := d.diffRecursive(va, vb, false, ctx)
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
	if len(opts) == 0 {
		mu.RLock()
		defer mu.RUnlock()
		return DiffTyped(defaultDiffer, a, b)
	}
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
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			return true
		}
		return isHashable(v.Elem())
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

func isNilValue(v reflect.Value) bool {
	if !v.IsValid() {
		return true
	}
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	}
	return false
}

func (d *Differ) tryDetectMove(v reflect.Value, path string, ctx *diffContext) (fromPath string, isMove bool, found bool) {
	if !d.config.detectMoves {
		return "", false, false
	}
	key := getHashKey(v)
	if key == nil || !isHashable(v) {
		return "", false, false
	}
	fromPath, found = ctx.valueIndex[key]
	if !found || fromPath == path {
		return "", false, false
	}
	isMove = ctx.movedPaths[fromPath]
	return fromPath, isMove, true
}

func getHashKey(v reflect.Value) any {
	if !v.IsValid() {
		return nil
	}
	if v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return nil
		}
		// For pointers/interfaces, use the pointer value as key to ensure 
		// consistent identity regardless of the interface type it's wrapped in.
		if v.Kind() == reflect.Pointer {
			return v.Pointer()
		}
		// For interfaces, recurse to get the underlying pointer or value.
		return getHashKey(v.Elem())
	}
	if v.CanInterface() {
		return v.Interface()
	}
	return nil
}

func (d *Differ) indexValues(v reflect.Value, ctx *diffContext) {
	if !v.IsValid() {
		return
	}

	key := getHashKey(v)
	if key != nil && isHashable(v) {
		if _, ok := ctx.valueIndex[key]; !ok {
			ctx.valueIndex[key] = ctx.buildPath()
		}
	}

	kind := v.Kind()
	if kind == reflect.Pointer || kind == reflect.Interface {
		if v.IsNil() {
			return
		}
		if kind == reflect.Pointer {
			ptr := v.Pointer()
			if ctx.visited[visitKey{a: ptr}] {
				return
			}
			ctx.visited[visitKey{a: ptr}] = true
		}
		d.indexValues(v.Elem(), ctx)
		return
	}

	switch kind {
	case reflect.Struct:
		info := getTypeInfo(v.Type())
		for _, fInfo := range info.fields {
			if fInfo.tag.ignore {
				continue
			}
			ctx.pathStack = append(ctx.pathStack, fInfo.name)
			d.indexValues(v.Field(fInfo.index), ctx)
			ctx.pathStack = ctx.pathStack[:len(ctx.pathStack)-1]
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			ctx.pathStack = append(ctx.pathStack, strconv.Itoa(i))
			d.indexValues(v.Index(i), ctx)
			ctx.pathStack = ctx.pathStack[:len(ctx.pathStack)-1]
		}
	case reflect.Map:
		iter := v.MapRange()
		for iter.Next() {
			k := iter.Key()
			ck := k.Interface()
			if keyer, ok := ck.(Keyer); ok {
				ck = keyer.CanonicalKey()
			}
			ctx.pathStack = append(ctx.pathStack, fmt.Sprintf("%v", ck))
			d.indexValues(iter.Value(), ctx)
			ctx.pathStack = ctx.pathStack[:len(ctx.pathStack)-1]
		}
	}
}

func (d *Differ) isValueAtTarget(path string, val any, ctx *diffContext) bool {
	if !ctx.rootB.IsValid() {
		return false
	}
	targetVal, err := deepPath(path).resolve(ctx.rootB)
	if err != nil {
		return false
	}
	if !targetVal.IsValid() {
		return false
	}
	return Equal(targetVal.Interface(), val)
}

func (d *Differ) diffRecursive(a, b reflect.Value, atomic bool, ctx *diffContext) (diffPatch, error) {
	if len(d.config.ignoredPaths) > 0 {
		if d.config.ignoredPaths[ctx.buildPath()] {
			return nil, nil
		}
	}

	if !a.IsValid() && !b.IsValid() {
		return nil, nil
	}

	if atomic {
		if valueEqual(a, b) {
			return nil, nil
		}
		return newValuePatch(deepCopyValue(a), deepCopyValue(b)), nil
	}

	if !a.IsValid() || !b.IsValid() {
		if !b.IsValid() {
			return newValuePatch(a, reflect.Value{}), nil
		}
		return newValuePatch(a, b), nil
	}

	if a.Type() != b.Type() {
		return newValuePatch(a, b), nil
	}

	if a.Kind() == reflect.Struct || a.Kind() == reflect.Map || a.Kind() == reflect.Slice {
		if !atomic {
			// Skip valueEqual and recurse
		} else {
			if valueEqual(a, b) {
				return nil, nil
			}
		}
	} else {
		// Basic types equality handled by Kind switch below.
	}

	if fn, ok := d.customDiffs[a.Type()]; ok {
		return fn(a, b, ctx)
	}

	// Move/Copy Detection
	if fromPath, isMove, ok := d.tryDetectMove(b, ctx.buildPath(), ctx); ok {
		if isMove {
			return &movePatch{from: fromPath, path: ctx.buildPath()}, nil
		}
		return &copyPatch{from: fromPath}, nil
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
	case reflect.Bool:
		if a.Bool() == b.Bool() {
			return nil, nil
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if a.Int() == b.Int() {
			return nil, nil
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		if a.Uint() == b.Uint() {
			return nil, nil
		}
	case reflect.Float32, reflect.Float64:
		if a.Float() == b.Float() {
			return nil, nil
		}
	case reflect.Complex64, reflect.Complex128:
		if a.Complex() == b.Complex() {
			return nil, nil
		}
	case reflect.String:
		if a.String() == b.String() {
			return nil, nil
		}
	case reflect.Pointer:
		return d.diffPtr(a, b, ctx)
	case reflect.Interface:
		return d.diffInterface(a, b, ctx)
	case reflect.Struct:
		return d.diffStruct(a, b, ctx)
	case reflect.Slice:
		return d.diffSlice(a, b, ctx)
	case reflect.Map:
		return d.diffMap(a, b, ctx)
	case reflect.Array:
		return d.diffArray(a, b, ctx)
	default:
		if a.Kind() == reflect.Func || a.Kind() == reflect.Chan || a.Kind() == reflect.UnsafePointer {
			if a.IsNil() && b.IsNil() {
				return nil, nil
			}
		}
	}
	
	// Default: if types are basic and immutable, return valuePatch without deep copy.
	k := a.Kind()
	if (k >= reflect.Bool && k <= reflect.String) || k == reflect.Float32 || k == reflect.Float64 || k == reflect.Complex64 || k == reflect.Complex128 {
		return newValuePatch(a, b), nil
	}
	return newValuePatch(deepCopyValue(a), deepCopyValue(b)), nil
}

func (d *Differ) diffPtr(a, b reflect.Value, ctx *diffContext) (diffPatch, error) {
	if a.IsNil() && b.IsNil() {
		return nil, nil
	}
	if a.IsNil() {
		return newValuePatch(a, b), nil
	}
	if b.IsNil() {
		return newValuePatch(deepCopyValue(a), reflect.Zero(a.Type())), nil
	}

	if a.Pointer() == b.Pointer() {
		return nil, nil
	}

	k := visitKey{a.Pointer(), b.Pointer(), a.Type()}
	if ctx.visited[k] {
		return nil, nil
	}
	ctx.visited[k] = true

	elemPatch, err := d.diffRecursive(a.Elem(), b.Elem(), false, ctx)
	if err != nil {
		return nil, err
	}
	if elemPatch == nil {
		return nil, nil
	}

	return newPtrPatch(elemPatch), nil
}

func (d *Differ) diffInterface(a, b reflect.Value, ctx *diffContext) (diffPatch, error) {
	if a.IsNil() && b.IsNil() {
		return nil, nil
	}
	if a.IsNil() || b.IsNil() {
		if !b.IsValid() {
			return newValuePatch(a, reflect.Value{}), nil
		}
		return newValuePatch(a, b), nil
	}

	if a.Elem().Type() != b.Elem().Type() {
		return newValuePatch(a, b), nil
	}

	elemPatch, err := d.diffRecursive(a.Elem(), b.Elem(), false, ctx)
	if err != nil {
		return nil, err
	}
	if elemPatch == nil {
		return nil, nil
	}

	return &interfacePatch{elemPatch: elemPatch}, nil
}

func (d *Differ) diffStruct(a, b reflect.Value, ctx *diffContext) (diffPatch, error) {
	var fields map[string]diffPatch
	info := getTypeInfo(b.Type())

	for _, fInfo := range info.fields {
		if fInfo.tag.ignore {
			continue
		}

		var fA reflect.Value
		if a.IsValid() {
			fA = a.Field(fInfo.index)
			if !fA.CanInterface() {
				unsafe.DisableRO(&fA)
			}
		}
		fB := b.Field(fInfo.index)
		if !fB.CanInterface() {
			unsafe.DisableRO(&fB)
		}

		ctx.pathStack = append(ctx.pathStack, fInfo.name)
		patch, err := d.diffRecursive(fA, fB, fInfo.tag.atomic, ctx)
		ctx.pathStack = ctx.pathStack[:len(ctx.pathStack)-1]

		if err != nil {
			return nil, err
		}
		if patch != nil {
			if fInfo.tag.readOnly {
				patch = &readOnlyPatch{inner: patch}
			}
			if fields == nil {
				fields = make(map[string]diffPatch)
			}
			fields[fInfo.name] = patch
		}
	}

	if fields == nil {
		return nil, nil
	}

	sp := newStructPatch()
	for k, v := range fields {
		sp.fields[k] = v
	}
	return sp, nil
}

func (d *Differ) diffArray(a, b reflect.Value, ctx *diffContext) (diffPatch, error) {
	indices := make(map[int]diffPatch)

	for i := 0; i < b.Len(); i++ {
		ctx.pathStack = append(ctx.pathStack, strconv.Itoa(i))
		var vA reflect.Value
		if a.IsValid() && i < a.Len() {
			vA = a.Index(i)
		}
		patch, err := d.diffRecursive(vA, b.Index(i), false, ctx)
		ctx.pathStack = ctx.pathStack[:len(ctx.pathStack)-1]

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

func (d *Differ) diffMap(a, b reflect.Value, ctx *diffContext) (diffPatch, error) {
	if isNilValue(a) && isNilValue(b) {
		return nil, nil
	}
	if isNilValue(a) || isNilValue(b) {
		if isNilValue(b) {
			return newValuePatch(a, reflect.Value{}), nil
		}
		if !d.config.detectMoves {
			return newValuePatch(a, b), nil
		}
	}

	if a.IsValid() && b.IsValid() && a.Pointer() == b.Pointer() {
		return nil, nil
	}

	var added map[any]reflect.Value
	var removed map[any]reflect.Value
	var modified map[any]diffPatch
	var originalKeys map[any]any

	getCanonical := func(v reflect.Value) any {
		if v.CanInterface() {
			val := v.Interface()
			if k, ok := val.(Keyer); ok {
				ck := k.CanonicalKey()
				if originalKeys == nil {
					originalKeys = make(map[any]any)
				}
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

	if a.IsValid() {
		iterA := a.MapRange()
		for iterA.Next() {
			k := iterA.Key()
			vA := iterA.Value()
			ck := getCanonical(k)

			ctx.pathStack = append(ctx.pathStack, fmt.Sprintf("%v", ck))
			vB, found := bByCanonical[ck]
			if !found {
				currentPath := ctx.buildPath()
				if (len(d.config.ignoredPaths) == 0 || !d.config.ignoredPaths[currentPath]) && !ctx.movedPaths[currentPath] {
					if removed == nil {
						removed = make(map[any]reflect.Value)
					}
					removed[ck] = deepCopyValue(vA)
				}
			} else {
				patch, err := d.diffRecursive(vA, vB, false, ctx)
				if err != nil {
					ctx.pathStack = ctx.pathStack[:len(ctx.pathStack)-1]
					return nil, err
				}
				if patch != nil {
					if modified == nil {
						modified = make(map[any]diffPatch)
					}
					modified[ck] = patch
				}
				delete(bByCanonical, ck)
			}
			ctx.pathStack = ctx.pathStack[:len(ctx.pathStack)-1]
		}
	}

	for ck, vB := range bByCanonical {
		currentPath := joinPath(ctx.buildPath(), fmt.Sprintf("%v", ck))
		if len(d.config.ignoredPaths) == 0 || !d.config.ignoredPaths[currentPath] {
			if fromPath, isMove, ok := d.tryDetectMove(vB, currentPath, ctx); ok {
				if modified == nil {
					modified = make(map[any]diffPatch)
				}
				if isMove {
					modified[ck] = &movePatch{from: fromPath, path: currentPath}
				} else {
					modified[ck] = &copyPatch{from: fromPath}
				}
				delete(bByCanonical, ck)
				continue
			}
			if added == nil {
				added = make(map[any]reflect.Value)
			}
			added[ck] = deepCopyValue(vB)
		}
	}

	if added == nil && removed == nil && modified == nil {
		return nil, nil
	}

	mp := newMapPatch(b.Type().Key())
	for k, v := range added {
		mp.added[k] = v
	}
	for k, v := range removed {
		mp.removed[k] = v
	}
	for k, v := range modified {
		mp.modified[k] = v
	}
	for k, v := range originalKeys {
		mp.originalKeys[k] = v
	}
	return mp, nil
}

func (d *Differ) diffSlice(a, b reflect.Value, ctx *diffContext) (diffPatch, error) {
	if isNilValue(a) && isNilValue(b) {
		return nil, nil
	}
	if isNilValue(a) || isNilValue(b) {
		if isNilValue(b) {
			return newValuePatch(a, reflect.Value{}), nil
		}
		// If move detection is enabled, we don't want to just return a valuePatch
		// because some elements in b might have been moved from elsewhere.
		if !d.config.detectMoves {
			return newValuePatch(a, b), nil
		}
	}

	if a.IsValid() && b.IsValid() && a.Pointer() == b.Pointer() {
		return nil, nil
	}

	lenA := 0
	if a.IsValid() {
		lenA = a.Len()
	}
	lenB := b.Len()

	prefix := 0
	if a.IsValid() {
		for prefix < lenA && prefix < lenB {
			vA := a.Index(prefix)
			vB := b.Index(prefix)
			if valueEqual(vA, vB) {
				prefix++
			} else {
				break
			}
		}
	}

	suffix := 0
	if a.IsValid() {
		for suffix < (lenA-prefix) && suffix < (lenB-prefix) {
			vA := a.Index(lenA - 1 - suffix)
			vB := b.Index(lenB - 1 - suffix)
			if valueEqual(vA, vB) {
				suffix++
			} else {
				break
			}
		}
	}

	midAStart := prefix
	midAEnd := lenA - suffix
	midBStart := prefix
	midBEnd := lenB - suffix

	keyField, hasKey := getKeyField(b.Type().Elem())

	if midAStart == midAEnd && midBStart < midBEnd {
		var ops []sliceOp
		for i := midBStart; i < midBEnd; i++ {
			var prevKey any
			if hasKey {
				if i > 0 {
					prevKey = extractKey(b.Index(i-1), keyField)
				}
			}
			
			// Move/Copy Detection
			val := b.Index(i)
			currentPath := joinPath(ctx.buildPath(), strconv.Itoa(i))
			if fromPath, isMove, ok := d.tryDetectMove(val, currentPath, ctx); ok {
				var p diffPatch
				if isMove {
					p = &movePatch{from: fromPath, path: currentPath}
				} else {
					p = &copyPatch{from: fromPath}
				}

				op := sliceOp{
					Kind:    OpCopy,
					Index:   i,
					Patch:   p,
					PrevKey: prevKey,
				}
				if hasKey {
					op.Key = extractKey(val, keyField)
				}
				ops = append(ops, op)
				continue
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
		for i := midAEnd - 1; i >= midAStart; i-- {
			currentPath := joinPath(ctx.buildPath(), strconv.Itoa(i))
			if ctx.movedPaths[currentPath] {
				continue
			}
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

	ops, err := d.computeSliceEdits(a, b, midAStart, midAEnd, midBStart, midBEnd, keyField, hasKey, ctx)
	if err != nil {
		return nil, err
	}

	return &slicePatch{ops: ops}, nil
}

func (d *Differ) computeSliceEdits(a, b reflect.Value, aStart, aEnd, bStart, bEnd, keyField int, hasKey bool, ctx *diffContext) ([]sliceOp, error) {
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
			return valueEqual(k1.Field(keyField), k2.Field(keyField))
		}
		return valueEqual(v1, v2)
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
				return d.backtrackMyers(a, b, aStart, aEnd, bStart, bEnd, keyField, hasKey, trace, ctx)
			}
		}
	}

	return nil, nil
}

func (d *Differ) backtrackMyers(a, b reflect.Value, aStart, aEnd, bStart, bEnd, keyField int, hasKey bool, trace [][]int, ctx *diffContext) ([]sliceOp, error) {
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
			if !valueEqual(vA, vB) {
				ctx.pathStack = append(ctx.pathStack, fmt.Sprintf("%v", extractKey(vA, keyField)))
				p, err := d.diffRecursive(vA, vB, false, ctx)
				ctx.pathStack = ctx.pathStack[:len(ctx.pathStack)-1]
				if err != nil {
					return nil, err
				}
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
			currentPath := joinPath(ctx.buildPath(), strconv.Itoa(aStart+x-1))
			if !ctx.movedPaths[currentPath] {
				op := sliceOp{
					Kind:  OpRemove,
					Index: aStart + x - 1,
					Val:   deepCopyValue(a.Index(aStart + x - 1)),
				}
				if hasKey {
					op.Key = extractKey(a.Index(aStart+x-1), keyField)
				}
				ops = append(ops, op)
			}
		} else if y > prevY {
			var prevKey any
			if hasKey && (bStart+y-2 >= 0) {
				prevKey = extractKey(b.Index(bStart+y-2), keyField)
			}
			val := b.Index(bStart + y - 1)

			currentPath := joinPath(ctx.buildPath(), strconv.Itoa(aStart+x))
			if fromPath, isMove, ok := d.tryDetectMove(val, currentPath, ctx); ok {
				var p diffPatch
				if isMove {
					p = &movePatch{from: fromPath, path: currentPath}
				} else {
					p = &copyPatch{from: fromPath}
				}
				op := sliceOp{
					Kind:    OpCopy,
					Index:   aStart + x,
					Patch:   p,
					PrevKey: prevKey,
				}
				if hasKey {
					op.Key = extractKey(val, keyField)
				}
				ops = append(ops, op)
				x, y = prevX, prevY
				continue
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
		if !valueEqual(vA, vB) {
			ctx.pathStack = append(ctx.pathStack, fmt.Sprintf("%v", extractKey(vA, keyField)))
			p, err := d.diffRecursive(vA, vB, false, ctx)
			ctx.pathStack = ctx.pathStack[:len(ctx.pathStack)-1]
			if err != nil {
				return nil, err
			}
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

	return ops, nil
}
