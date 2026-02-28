package engine

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"

	"github.com/brunoga/deep/v5/internal/core"
	"github.com/brunoga/deep/v5/internal/unsafe"
)

// DiffOption allows configuring the behavior of the Diff function.
// Note: DiffOption is defined in options.go

type diffConfig struct {
	ignoredPaths map[string]bool
	detectMoves  bool
}

type diffOptionFunc func(*diffConfig)

func (f diffOptionFunc) applyDiffOption() {}

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
		if f, ok := opt.(diffOptionFunc); ok {
			f(config)
		} else if u, ok := opt.(unifiedOption); ok {
			config.ignoredPaths[core.NormalizePath(string(u))] = true
		}
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
	visited    map[core.VisitKey]bool
	pathStack  []string
	rootB      reflect.Value
}

var diffContextPool = sync.Pool{
	New: func() any {
		return &diffContext{
			valueIndex: make(map[any]string),
			movedPaths: make(map[string]bool),
			visited:    make(map[core.VisitKey]bool),
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
		b.WriteString(core.EscapeKey(s))
	}
	return b.String()
}

var (
	defaultDiffer = NewDiffer()
	mu            sync.RWMutex
)

// RegisterCustomDiff registers a custom diff function for a specific type globally.
func RegisterCustomDiff[T any](fn func(a, b T) (Patch[T], error)) {
	mu.Lock()
	d := defaultDiffer
	mu.Unlock()

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
func (d *Differ) Diff(a, b any) (Patch[any], error) {
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
		return nil, err
	}
	if patch == nil {
		return nil, nil
	}
	return &typedPatch[any]{inner: patch, strict: true}, nil
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
		info := core.GetTypeInfo(v.Type())
		for _, fInfo := range info.Fields {
			if fInfo.Tag.Ignore {
				continue
			}
			ctx.pathStack = append(ctx.pathStack, fInfo.Name)
			d.detectMovesRecursive(v.Field(fInfo.Index), ctx)
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

// Diff compares two values a and b and returns a Patch that can be applied.
// It returns an error if the comparison fails (e.g., due to custom diff failure).
func Diff[T any](a, b T, opts ...DiffOption) (Patch[T], error) {
	var d *Differ
	if len(opts) == 0 {
		mu.RLock()
		d = defaultDiffer
		mu.RUnlock()
	} else {
		d = NewDiffer(opts...)
	}
	return DiffUsing(d, a, b)
}

// DiffUsing compares two values a and b using the specified Differ and returns a Patch.
func DiffUsing[T any](d *Differ, a, b T) (Patch[T], error) {
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
		return nil, err
	}

	if patch == nil {
		return nil, nil
	}

	return &typedPatch[T]{
		inner:  patch,
		strict: true,
	}, nil
}

// MustDiff compares two values a and b and returns a Patch that can be applied.
// It panics if the comparison fails.
func MustDiff[T any](a, b T, opts ...DiffOption) Patch[T] {
	p, err := Diff(a, b, opts...)
	if err != nil {
		panic(err)
	}
	return p
}

// MustDiffUsing compares two values a and b using the specified Differ and returns a Patch.
// It panics if the comparison fails.
func MustDiffUsing[T any](d *Differ, a, b T) Patch[T] {
	p, err := DiffUsing(d, a, b)
	if err != nil {
		panic(err)
	}
	return p
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
			if ctx.visited[core.VisitKey{A: ptr}] {
				return
			}
			ctx.visited[core.VisitKey{A: ptr}] = true
		}
		d.indexValues(v.Elem(), ctx)
		return
	}

	switch kind {
	case reflect.Struct:
		info := core.GetTypeInfo(v.Type())
		for _, fInfo := range info.Fields {
			if fInfo.Tag.Ignore {
				continue
			}
			ctx.pathStack = append(ctx.pathStack, fInfo.Name)
			d.indexValues(v.Field(fInfo.Index), ctx)
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
	targetVal, err := core.DeepPath(path).Resolve(ctx.rootB)
	if err != nil {
		return false
	}
	if !targetVal.IsValid() {
		return false
	}
	return core.Equal(targetVal.Interface(), val)
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
		if core.ValueEqual(a, b, nil) {
			return nil, nil
		}
		return newValuePatch(core.DeepCopyValue(a), core.DeepCopyValue(b)), nil
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
			if core.ValueEqual(a, b, nil) {
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
	return newValuePatch(core.DeepCopyValue(a), core.DeepCopyValue(b)), nil
}

func (d *Differ) diffPtr(a, b reflect.Value, ctx *diffContext) (diffPatch, error) {
	if a.IsNil() && b.IsNil() {
		return nil, nil
	}
	if a.IsNil() {
		return newValuePatch(a, b), nil
	}
	if b.IsNil() {
		return newValuePatch(core.DeepCopyValue(a), reflect.Zero(a.Type())), nil
	}

	if a.Pointer() == b.Pointer() {
		return nil, nil
	}

	k := core.VisitKey{A: a.Pointer(), B: b.Pointer(), Typ: a.Type()}
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
	info := core.GetTypeInfo(b.Type())

	for _, fInfo := range info.Fields {
		if fInfo.Tag.Ignore {
			continue
		}

		var fA reflect.Value
		if a.IsValid() {
			fA = a.Field(fInfo.Index)
			if !fA.CanInterface() {
				unsafe.DisableRO(&fA)
			}
		}
		fB := b.Field(fInfo.Index)
		if !fB.CanInterface() {
			unsafe.DisableRO(&fB)
		}

		ctx.pathStack = append(ctx.pathStack, fInfo.Name)
		patch, err := d.diffRecursive(fA, fB, fInfo.Tag.Atomic, ctx)
		ctx.pathStack = ctx.pathStack[:len(ctx.pathStack)-1]

		if err != nil {
			return nil, err
		}
		if patch != nil {
			if fInfo.Tag.ReadOnly {
				patch = &readOnlyPatch{inner: patch}
			}
			if fields == nil {
				fields = make(map[string]diffPatch)
			}
			fields[fInfo.Name] = patch
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
					removed[ck] = core.DeepCopyValue(vA)
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
		// Escape the key before joining
		currentPath := core.JoinPath(ctx.buildPath(), core.EscapeKey(fmt.Sprintf("%v", ck)))
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
			added[ck] = core.DeepCopyValue(vB)
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
			if core.ValueEqual(vA, vB, nil) {
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
			if core.ValueEqual(vA, vB, nil) {
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

	keyField, hasKey := core.GetKeyField(b.Type().Elem())

	if midAStart == midAEnd && midBStart < midBEnd {
		var ops []sliceOp
		for i := midBStart; i < midBEnd; i++ {
			var prevKey any
			if hasKey {
				if i > 0 {
					prevKey = core.ExtractKey(b.Index(i-1), keyField)
				}
			}

			// Move/Copy Detection
			val := b.Index(i)
			currentPath := core.JoinPath(ctx.buildPath(), strconv.Itoa(i))
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
					op.Key = core.ExtractKey(val, keyField)
				}
				ops = append(ops, op)
				continue
			}

			op := sliceOp{
				Kind:    OpAdd,
				Index:   i,
				Val:     core.DeepCopyValue(b.Index(i)),
				PrevKey: prevKey,
			}
			if hasKey {
				op.Key = core.ExtractKey(b.Index(i), keyField)
			}
			ops = append(ops, op)
		}
		return &slicePatch{ops: ops}, nil
	}

	if midBStart == midBEnd && midAStart < midAEnd {
		var ops []sliceOp
		for i := midAEnd - 1; i >= midAStart; i-- {
			currentPath := core.JoinPath(ctx.buildPath(), strconv.Itoa(i))
			if ctx.movedPaths[currentPath] {
				continue
			}
			op := sliceOp{
				Kind:  OpRemove,
				Index: i,
				Val:   core.DeepCopyValue(a.Index(i)),
			}
			if hasKey {
				op.Key = core.ExtractKey(a.Index(i), keyField)
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
			return core.ValueEqual(k1.Field(keyField), k2.Field(keyField), nil)
		}
		return core.ValueEqual(v1, v2, nil)
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
			if !core.ValueEqual(vA, vB, nil) {
				ctx.pathStack = append(ctx.pathStack, fmt.Sprintf("%v", core.ExtractKey(vA, keyField)))
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
					op.Key = core.ExtractKey(vA, keyField)
				}
				ops = append(ops, op)
			}
			x--
			y--
		}

		if x > prevX {
			currentPath := core.JoinPath(ctx.buildPath(), strconv.Itoa(aStart+x-1))
			if !ctx.movedPaths[currentPath] {
				op := sliceOp{
					Kind:  OpRemove,
					Index: aStart + x - 1,
					Val:   core.DeepCopyValue(a.Index(aStart + x - 1)),
				}
				if hasKey {
					op.Key = core.ExtractKey(a.Index(aStart+x-1), keyField)
				}
				ops = append(ops, op)
			}
		} else if y > prevY {
			var prevKey any
			if hasKey && (bStart+y-2 >= 0) {
				prevKey = core.ExtractKey(b.Index(bStart+y-2), keyField)
			}
			val := b.Index(bStart + y - 1)

			currentPath := core.JoinPath(ctx.buildPath(), strconv.Itoa(aStart+x))
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
					op.Key = core.ExtractKey(val, keyField)
				}
				ops = append(ops, op)
				x, y = prevX, prevY
				continue
			}

			op := sliceOp{
				Kind:    OpAdd,
				Index:   aStart + x,
				Val:     core.DeepCopyValue(val),
				PrevKey: prevKey,
			}
			if hasKey {
				op.Key = core.ExtractKey(b.Index(bStart+y-1), keyField)
			}
			ops = append(ops, op)
		}
		x, y = prevX, prevY
	}

	for x > 0 && y > 0 {
		vA := a.Index(aStart + x - 1)
		vB := b.Index(bStart + y - 1)
		if !core.ValueEqual(vA, vB, nil) {
			ctx.pathStack = append(ctx.pathStack, fmt.Sprintf("%v", core.ExtractKey(vA, keyField)))
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
				op.Key = core.ExtractKey(vA, keyField)
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
