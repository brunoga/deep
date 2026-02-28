package engine

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/brunoga/deep/v5/cond"
	"github.com/brunoga/deep/v5/internal/core"
)

// PatchBuilder allows constructing a Patch[T] manually with on-the-fly type validation.
// It acts as a cursor within the value's structure, allowing for fluent navigation
// and modification.
type PatchBuilder[T any] struct {
	state *builderState[T]

	typ      reflect.Type
	update   func(diffPatch)
	current  diffPatch
	fullPath string

	parent *PatchBuilder[T]
	key    string
	index  int
	isIdx  bool
}

type builderState[T any] struct {
	patch diffPatch
	err   error
}

// NewPatchBuilder returns a new PatchBuilder for type T, pointing at the root.
func NewPatchBuilder[T any]() *PatchBuilder[T] {
	var t T
	typ := reflect.TypeOf(t)
	b := &PatchBuilder[T]{
		state: &builderState[T]{},
		typ:   typ,
	}
	b.update = func(p diffPatch) {
		b.state.patch = p
	}
	return b
}

// Build returns the constructed Patch or an error if any operation was invalid.
func (b *PatchBuilder[T]) Build() (Patch[T], error) {
	if b.state.err != nil {
		return nil, b.state.err
	}
	if b.state.patch == nil {
		return nil, nil
	}
	return &typedPatch[T]{
		inner:  b.state.patch,
		strict: true,
	}, nil
}

// AddCondition parses a string expression and attaches it to the appropriate
// node in the patch tree based on the paths used in the expression.
// It finds the longest common prefix of all paths in the expression and
// navigates to that node before attaching the condition.
// The expression is evaluated relative to the current node.
func (b *PatchBuilder[T]) AddCondition(expr string) *PatchBuilder[T] {
	if b.state.err != nil {
		return b
	}
	c, err := cond.ParseCondition[T](expr)
	if err != nil {
		b.state.err = err
		return b
	}

	paths := c.Paths()
	prefix := lcpParts(paths)

	node := b.Navigate(prefix)
	if b.state.err != nil {
		return b
	}

	node.WithCondition(c.WithRelativePath(prefix))
	return b
}

// Navigate returns a new PatchBuilder for the specified path relative to the current node.
// It supports JSON Pointers ("/Field/Sub").
func (b *PatchBuilder[T]) Navigate(path string) *PatchBuilder[T] {
	if b.state.err != nil || path == "" {
		return b
	}
	return b.navigateParts(core.ParsePath(path))
}

func (b *PatchBuilder[T]) navigateParts(parts []core.PathPart) *PatchBuilder[T] {
	curr := b
	for _, part := range parts {
		if part.IsIndex {
			curr = curr.Index(part.Index)
		} else {
			curr = curr.FieldOrMapKey(part.Key)
		}
		if b.state.err != nil {
			return b
		}
	}
	return curr
}

// Put replaces the value at the current node without requiring the 'old' value.
// Strict consistency checks for this specific value will be disabled.
func (b *PatchBuilder[T]) Put(value any) *PatchBuilder[T] {
	if b.state.err != nil {
		return b
	}
	vNew := reflect.ValueOf(value)
	p := &valuePatch{
		newVal: core.DeepCopyValue(vNew),
	}
	if b.current != nil {
		p.cond, p.ifCond, p.unlessCond = b.current.conditions()
	}
	b.update(p)
	b.current = p
	return b
}

// Set replaces the value at the current node. It requires the 'old' value
// to enable patch reversibility and strict application checking.
func (b *PatchBuilder[T]) Set(old, new any) *PatchBuilder[T] {
	if b.state.err != nil {
		return b
	}
	vOld := reflect.ValueOf(old)
	vNew := reflect.ValueOf(new)
	p := &valuePatch{
		oldVal: core.DeepCopyValue(vOld),
		newVal: core.DeepCopyValue(vNew),
	}
	if b.current != nil {
		p.cond, p.ifCond, p.unlessCond = b.current.conditions()
	}
	b.update(p)
	b.current = p
	return b
}

// Test adds a test operation to the current node. The patch application
// will fail if the value at this node does not match the expected value.
func (b *PatchBuilder[T]) Test(expected any) *PatchBuilder[T] {
	if b.state.err != nil {
		return b
	}
	vExpected := reflect.ValueOf(expected)
	p := &testPatch{
		expected: core.DeepCopyValue(vExpected),
	}
	if b.current != nil {
		p.cond, p.ifCond, p.unlessCond = b.current.conditions()
	}
	b.update(p)
	b.current = p
	return b
}

// Copy copies a value from another path to the current node.
func (b *PatchBuilder[T]) Copy(from string) *PatchBuilder[T] {
	if b.state.err != nil {
		return b
	}
	absPath := b.fullPath
	if absPath == "" {
		absPath = "/"
	} else if absPath[0] != '/' {
		absPath = "/" + absPath
	}

	absFrom := from
	if absFrom == "" {
		absFrom = "/"
	} else if absFrom[0] != '/' {
		absFrom = "/" + absFrom
	}

	p := &copyPatch{
		from: absFrom,
		path: absPath,
	}
	if b.current != nil {
		p.cond, p.ifCond, p.unlessCond = b.current.conditions()
	}
	b.update(p)
	b.current = p
	return b
}

// Move moves a value from another path to the current node.
func (b *PatchBuilder[T]) Move(from string) *PatchBuilder[T] {
	if b.state.err != nil {
		return b
	}
	absPath := b.fullPath
	if absPath == "" {
		absPath = "/"
	} else if absPath[0] != '/' {
		absPath = "/" + absPath
	}

	absFrom := from
	if absFrom == "" {
		absFrom = "/"
	} else if absFrom[0] != '/' {
		absFrom = "/" + absFrom
	}

	p := &movePatch{
		from: absFrom,
		path: absPath,
	}
	if b.current != nil {
		p.cond, p.ifCond, p.unlessCond = b.current.conditions()
	}
	b.update(p)
	b.current = p
	return b
}

// Log adds a log operation to the current node. It prints a message
// and the current value at the node during patch application.
func (b *PatchBuilder[T]) Log(message string) *PatchBuilder[T] {
	if b.state.err != nil {
		return b
	}
	p := &logPatch{
		message: message,
	}
	if b.current != nil {
		p.cond, p.ifCond, p.unlessCond = b.current.conditions()
	}
	b.update(p)
	b.current = p
	return b
}

func (b *PatchBuilder[T]) ensurePatch() {
	if b.current != nil {
		return
	}
	var p diffPatch
	if b.typ == nil {
		p = &valuePatch{}
	} else {
		switch b.typ.Kind() {
		case reflect.Struct:
			p = &structPatch{fields: make(map[string]diffPatch)}
		case reflect.Slice:
			p = &slicePatch{}
		case reflect.Map:
			p = &mapPatch{
				added:        make(map[any]reflect.Value),
				removed:      make(map[any]reflect.Value),
				modified:     make(map[any]diffPatch),
				originalKeys: make(map[any]any),
				keyType:      b.typ.Key(),
			}
		case reflect.Pointer:
			p = &ptrPatch{}
		case reflect.Interface:
			p = &interfacePatch{}
		case reflect.Array:
			p = &arrayPatch{indices: make(map[int]diffPatch)}
		default:
			p = &valuePatch{}
		}
	}
	b.current = p
	b.update(p)
}

// WithCondition attaches a local condition to the current node.
// This condition is evaluated against the value at this node during ApplyChecked.
func (b *PatchBuilder[T]) WithCondition(c any) *PatchBuilder[T] {
	if b.state.err != nil {
		return b
	}
	b.ensurePatch()
	if b.current != nil {
		b.current.setCondition(c)
	}
	return b
}

// If attaches an 'if' condition to the current node. If the condition
// evaluates to false, the operation at this node is skipped.
func (b *PatchBuilder[T]) If(c any) *PatchBuilder[T] {
	if b.state.err != nil {
		return b
	}
	b.ensurePatch()
	if b.current != nil {
		b.current.setIfCondition(c)
	}
	return b
}

// Unless attaches an 'unless' condition to the current node. If the condition
// evaluates to true, the operation at this node is skipped.
func (b *PatchBuilder[T]) Unless(c any) *PatchBuilder[T] {
	if b.state.err != nil {
		return b
	}
	b.ensurePatch()
	if b.current != nil {
		b.current.setUnlessCondition(c)
	}
	return b
}

// Field returns a new PatchBuilder for the specified struct field. It automatically
// descends into pointers and interfaces if necessary.
func (b *PatchBuilder[T]) Field(name string) *PatchBuilder[T] {
	if b.state.err != nil {
		return b
	}
	curr := b.Elem()
	if curr.typ.Kind() != reflect.Struct {
		b.state.err = fmt.Errorf("not a struct: %v", curr.typ)
		return b
	}
	field, ok := curr.typ.FieldByName(name)
	if !ok {
		b.state.err = fmt.Errorf("field not found: %s", name)
		return b
	}
	sp, ok := curr.current.(*structPatch)
	if !ok {
		sp = &structPatch{fields: make(map[string]diffPatch)}
		curr.update(sp)
		curr.current = sp
	}
	return &PatchBuilder[T]{
		state: b.state,
		typ:   field.Type,
		update: func(p diffPatch) {
			sp.fields[name] = p
		},
		current:  sp.fields[name],
		fullPath: curr.fullPath + "/" + name,
		parent:   curr,
		key:      name,
	}
}

// Index returns a new PatchBuilder for the specified array or slice index.
func (b *PatchBuilder[T]) Index(i int) *PatchBuilder[T] {
	if b.state.err != nil {
		return b
	}
	curr := b.Elem()
	kind := curr.typ.Kind()
	if kind != reflect.Slice && kind != reflect.Array {
		b.state.err = fmt.Errorf("not a slice or array: %v", curr.typ)
		return b
	}
	if kind == reflect.Array && (i < 0 || i >= curr.typ.Len()) {
		b.state.err = fmt.Errorf("index out of bounds: %d", i)
		return b
	}
	if kind == reflect.Array {
		ap, ok := curr.current.(*arrayPatch)
		if !ok {
			ap = &arrayPatch{indices: make(map[int]diffPatch)}
			curr.update(ap)
			curr.current = ap
		}
		return &PatchBuilder[T]{
			state: b.state,
			typ:   curr.typ.Elem(),
			update: func(p diffPatch) {
				ap.indices[i] = p
			},
			current:  ap.indices[i],
			fullPath: curr.fullPath + "/" + strconv.Itoa(i),
			parent:   curr,
			index:    i,
			isIdx:    true,
		}
	}
	sp, ok := curr.current.(*slicePatch)
	if !ok {
		sp = &slicePatch{}
		curr.update(sp)
		curr.current = sp
	}
	var modOp *sliceOp
	for j := range sp.ops {
		if sp.ops[j].Index == i && sp.ops[j].Kind == OpReplace {
			modOp = &sp.ops[j]
			break
		}
	}
	if modOp == nil {
		sp.ops = append(sp.ops, sliceOp{
			Kind:  OpReplace,
			Index: i,
		})
		modOp = &sp.ops[len(sp.ops)-1]
	}
	return &PatchBuilder[T]{
		state: b.state,
		typ:   curr.typ.Elem(),
		update: func(p diffPatch) {
			modOp.Patch = p
		},
		current:  modOp.Patch,
		fullPath: curr.fullPath + "/" + strconv.Itoa(i),
		parent:   curr,
		index:    i,
		isIdx:    true,
	}
}

// MapKey returns a new PatchBuilder for the specified map key.
func (b *PatchBuilder[T]) MapKey(key any) *PatchBuilder[T] {
	if b.state.err != nil {
		return b
	}
	curr := b.Elem()
	if curr.typ.Kind() != reflect.Map {
		b.state.err = fmt.Errorf("not a map: %v", curr.typ)
		return b
	}
	vKey := reflect.ValueOf(key)
	if vKey.Type() != curr.typ.Key() {
		if _, ok := key.(string); ok {
			// Special handling for canonical keys during navigation
		} else {
			b.state.err = fmt.Errorf("invalid key type: expected %v, got %v", curr.typ.Key(), vKey.Type())
			return b
		}
	}
	mp, ok := curr.current.(*mapPatch)
	if !ok {
		mp = &mapPatch{
			added:    make(map[any]reflect.Value),
			removed:  make(map[any]reflect.Value),
			modified: make(map[any]diffPatch),
			keyType:  curr.typ.Key(),
		}
		curr.update(mp)
		curr.current = mp
	}
	return &PatchBuilder[T]{
		state: b.state,
		typ:   curr.typ.Elem(),
		update: func(p diffPatch) {
			mp.modified[key] = p
		},
		current:  mp.modified[key],
		fullPath: curr.fullPath + "/" + fmt.Sprintf("%v", key),
		parent:   curr,
		key:      fmt.Sprintf("%v", key),
	}
}

// Elem returns a new PatchBuilder for the element type of a pointer or interface.
func (b *PatchBuilder[T]) Elem() *PatchBuilder[T] {
	if b.state.err != nil || b.typ == nil || (b.typ.Kind() != reflect.Pointer && b.typ.Kind() != reflect.Interface) {
		return b
	}
	var updateFunc func(diffPatch)
	var currentPatch diffPatch
	if b.typ.Kind() == reflect.Pointer {
		pp, ok := b.current.(*ptrPatch)
		if !ok {
			pp = &ptrPatch{}
			b.update(pp)
			b.current = pp
		}
		updateFunc = func(p diffPatch) { pp.elemPatch = p }
		currentPatch = pp.elemPatch
	} else {
		ip, ok := b.current.(*interfacePatch)
		if !ok {
			ip = &interfacePatch{}
			b.update(ip)
			b.current = ip
		}
		updateFunc = func(p diffPatch) { ip.elemPatch = p }
		currentPatch = ip.elemPatch
	}
	var nextTyp reflect.Type
	if b.typ.Kind() == reflect.Pointer {
		nextTyp = b.typ.Elem()
	}
	return &PatchBuilder[T]{
		state:    b.state,
		typ:      nextTyp,
		update:   updateFunc,
		current:  currentPatch,
		fullPath: b.fullPath,
		parent:   b.parent,
		key:      b.key,
		index:    b.index,
		isIdx:    b.isIdx,
	}
}

// Add appends an addition operation to a slice or map node.
func (b *PatchBuilder[T]) Add(keyOrIndex, val any) *PatchBuilder[T] {
	if b.state.err != nil {
		return b
	}
	if b.typ.Kind() == reflect.Slice {
		i, ok := keyOrIndex.(int)
		if !ok {
			b.state.err = fmt.Errorf("index must be int for slices")
			return b
		}
		var v reflect.Value
		if rv, ok := val.(reflect.Value); ok {
			v = rv
		} else {
			v = reflect.ValueOf(val)
		}

		if !v.IsValid() {
			v = reflect.Zero(b.typ.Elem())
		}

		if v.Type() != b.typ.Elem() {
			b.state.err = fmt.Errorf("invalid value type: expected %v, got %v", b.typ.Elem(), v.Type())
			return b
		}
		sp, ok := b.current.(*slicePatch)
		if !ok {
			sp = &slicePatch{}
			b.update(sp)
			b.current = sp
		}
		sp.ops = append(sp.ops, sliceOp{
			Kind:  OpAdd,
			Index: i,
			Val:   core.DeepCopyValue(v),
		})
		return b
	}
	if b.typ.Kind() == reflect.Map {
		vKey := reflect.ValueOf(keyOrIndex)
		if vKey.Type() != b.typ.Key() {
			if s, ok := keyOrIndex.(string); ok {
				if b.typ.Key().Kind() == reflect.String {
					vKey = reflect.ValueOf(s)
				}
			}
			if vKey.Type() != b.typ.Key() {
				b.state.err = fmt.Errorf("invalid key type: expected %v, got %v", b.typ.Key(), vKey.Type())
				return b
			}
		}
		var vVal reflect.Value
		if rv, ok := val.(reflect.Value); ok {
			vVal = rv
		} else {
			vVal = reflect.ValueOf(val)
		}

		if !vVal.IsValid() {
			vVal = reflect.Zero(b.typ.Elem())
		}

		if vVal.Type() != b.typ.Elem() {
			b.state.err = fmt.Errorf("invalid value type: expected %v, got %v", b.typ.Elem(), vVal.Type())
			return b
		}
		mp, ok := b.current.(*mapPatch)
		if !ok {
			mp = &mapPatch{
				added:        make(map[any]reflect.Value),
				removed:      make(map[any]reflect.Value),
				modified:     make(map[any]diffPatch),
				originalKeys: make(map[any]any),
				keyType:      b.typ.Key(),
			}
			b.update(mp)
			b.current = mp
		}
		mp.added[keyOrIndex] = core.DeepCopyValue(vVal)
		return b
	}
	b.state.err = fmt.Errorf("Add only supported on slices and maps, got %v", b.typ.Kind())
	return b
}

// Delete appends a deletion operation to a slice or map node.
func (b *PatchBuilder[T]) Delete(keyOrIndex any, oldVal any) *PatchBuilder[T] {
	if b.state.err != nil {
		return b
	}
	if b.typ.Kind() == reflect.Slice {
		i, ok := keyOrIndex.(int)
		if !ok {
			b.state.err = fmt.Errorf("index must be int for slices")
			return b
		}
		var vOld reflect.Value
		if rv, ok := oldVal.(reflect.Value); ok {
			vOld = rv
		} else {
			vOld = reflect.ValueOf(oldVal)
		}

		if !vOld.IsValid() {
			vOld = reflect.Zero(b.typ.Elem())
		}

		if vOld.Type() != b.typ.Elem() {
			b.state.err = fmt.Errorf("invalid old value type: expected %v, got %v", b.typ.Elem(), vOld.Type())
			return b
		}
		sp, ok := b.current.(*slicePatch)
		if !ok {
			sp = &slicePatch{}
			b.update(sp)
			b.current = sp
		}
		sp.ops = append(sp.ops, sliceOp{
			Kind:  OpRemove,
			Index: i,
			Val:   core.DeepCopyValue(vOld),
		})
		return b
	}
	if b.typ.Kind() == reflect.Map {
		vKey := reflect.ValueOf(keyOrIndex)
		if vKey.Type() != b.typ.Key() {
			if s, ok := keyOrIndex.(string); ok {
				if b.typ.Key().Kind() == reflect.String {
					vKey = reflect.ValueOf(s)
				}
			}
			if vKey.Type() != b.typ.Key() {
				b.state.err = fmt.Errorf("invalid key type: expected %v, got %v", b.typ.Key(), vKey.Type())
				return b
			}
		}
		var vOld reflect.Value
		if rv, ok := oldVal.(reflect.Value); ok {
			vOld = rv
		} else {
			vOld = reflect.ValueOf(oldVal)
		}

		if !vOld.IsValid() {
			vOld = reflect.Zero(b.typ.Elem())
		}

		if vOld.Type() != b.typ.Elem() {
			b.state.err = fmt.Errorf("invalid old value type: expected %v, got %v", b.typ.Elem(), vOld.Type())
			return b
		}
		mp, ok := b.current.(*mapPatch)
		if !ok {
			mp = &mapPatch{
				added:        make(map[any]reflect.Value),
				removed:      make(map[any]reflect.Value),
				modified:     make(map[any]diffPatch),
				originalKeys: make(map[any]any),
				keyType:      b.typ.Key(),
			}
			b.update(mp)
			b.current = mp
		}
		mp.removed[keyOrIndex] = core.DeepCopyValue(vOld)
		return b
	}
	b.state.err = fmt.Errorf("Delete only supported on slices and maps, got %v", b.typ)
	return b
}

// Remove removes the current node from its parent.
func (b *PatchBuilder[T]) Remove(oldVal any) *PatchBuilder[T] {
	if b.state.err != nil {
		return b
	}
	if b.parent == nil {
		b.state.err = fmt.Errorf("cannot remove root node")
		return b
	}
	if b.isIdx {
		b.parent.Delete(b.index, oldVal)
	} else {
		b.parent.Delete(b.key, oldVal)
	}
	return b
}

// FieldOrMapKey returns a new PatchBuilder for the specified field or map key.
func (b *PatchBuilder[T]) FieldOrMapKey(key string) *PatchBuilder[T] {
	if b.state.err != nil {
		return b
	}
	curr := b.Elem()
	if curr.typ != nil && curr.typ.Kind() == reflect.Map {
		keyType := curr.typ.Key()
		var keyVal any
		if keyType.Kind() == reflect.String {
			keyVal = key
		} else if keyType.Kind() == reflect.Int {
			i, err := strconv.Atoi(key)
			if err != nil {
				b.state.err = fmt.Errorf("invalid int key for map: %s", key)
				return b
			}
			keyVal = i
		} else {
			return curr.MapKey(key)
		}
		return curr.MapKey(keyVal)
	}
	return curr.Field(key)
}

// lcpParts returns the longest common prefix of the given paths.
func lcpParts(paths []string) string {
	if len(paths) == 0 {
		return ""
	}

	allParts := make([][]core.PathPart, len(paths))
	for i, p := range paths {
		allParts[i] = core.ParsePath(p)
	}

	common := allParts[0]
	for i := 1; i < len(allParts); i++ {
		n := len(common)
		if len(allParts[i]) < n {
			n = len(allParts[i])
		}
		common = common[:n]
		for j := 0; j < n; j++ {
			if !common[j].Equals(allParts[i][j]) {
				common = common[:j]
				break
			}
		}
	}

	// Convert common parts back to string path
	if len(common) == 0 {
		return ""
	}
	var b strings.Builder
	for i, p := range common {
		if p.IsIndex {
			if i == 0 {
				// Special case
			}
			b.WriteByte('/')
			b.WriteString(strconv.Itoa(p.Index))
		} else {
			b.WriteByte('/')
			b.WriteString(p.Key)
		}
	}
	return b.String()
}
