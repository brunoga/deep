package deep

import (
	"fmt"
	"reflect"
	"strconv"
)

// Builder allows constructing a Patch[T] manually with on-the-fly type validation.
type Builder[T any] struct {
	typ   reflect.Type
	patch diffPatch
	err   error
}

// NewBuilder returns a new Builder for type T.
func NewBuilder[T any]() *Builder[T] {
	var t T
	typ := reflect.TypeOf(t)
	return &Builder[T]{
		typ: typ,
	}
}

// Build returns the constructed Patch or an error if any operation was invalid.
func (b *Builder[T]) Build() (Patch[T], error) {
	if b.err != nil {
		return nil, b.err
	}
	if b.patch == nil {
		return nil, nil
	}
	return &typedPatch[T]{
		inner:  b.patch,
		strict: true,
	}, nil
}

// Root returns a Node representing the root of the value being patched.
func (b *Builder[T]) Root() *Node {
	return &Node{
		typ: b.typ,
		update: func(p diffPatch) {
			b.patch = p
		},
		current:  b.patch,
		fullPath: "",
	}
}

// AddCondition parses a string expression and attaches it to the appropriate
// node in the patch tree based on the paths used in the expression.
// It finds the longest common prefix of all paths in the expression and
// navigates to that node before attaching the condition.
func (b *Builder[T]) AddCondition(expr string) *Builder[T] {
	if b.err != nil {
		return b
	}
	raw, err := parseRawCondition(expr)
	if err != nil {
		b.err = err
		return b
	}

	paths := raw.paths()
	prefix := lcpParts(paths)

	node, err := b.Root().navigateParts(prefix)
	if err != nil {
		b.err = err
		return b
	}

	node.WithCondition(raw.withRelativeParts(prefix))
	return b
}

func lcpParts(paths []Path) []pathPart {
	if len(paths) == 0 {
		return nil
	}

	allParts := make([][]pathPart, len(paths))
	for i, p := range paths {
		allParts[i] = parsePath(string(p))
	}

	common := allParts[0]
	for i := 1; i < len(allParts); i++ {
		n := len(common)
		if len(allParts[i]) < n {
			n = len(allParts[i])
		}
		common = common[:n]
		for j := 0; j < n; j++ {
			if !common[j].equals(allParts[i][j]) {
				common = common[:j]
				break
			}
		}
	}
	return common
}

func (n *Node) navigateParts(parts []pathPart) (*Node, error) {
	curr := n
	var err error
	for _, part := range parts {
		if part.isIndex {
			curr, err = curr.Index(part.index)
		} else {
			curr, err = curr.FieldOrMapKey(part.key)
		}
		if err != nil {
			return nil, err
		}
	}
	return curr, nil
}

// Node represents a specific location within a value's structure.
type Node struct {
	typ      reflect.Type
	update   func(diffPatch)
	current  diffPatch
	fullPath string
}

func (n *Node) navigate(path string) (*Node, error) {
	if path == "" {
		return n, nil
	}
	return n.navigateParts(parsePath(path))
}

func (n *Node) FieldOrMapKey(key string) (*Node, error) {
	curr := n.Elem()
	if curr.typ != nil && curr.typ.Kind() == reflect.Map {
		keyType := curr.typ.Key()
		var keyVal any
		if keyType.Kind() == reflect.String {
			keyVal = key
		} else if keyType.Kind() == reflect.Int {
			i, err := strconv.Atoi(key)
			if err != nil {
				return nil, fmt.Errorf("invalid int key for map: %s", key)
			}
			keyVal = i
		} else {
			return nil, fmt.Errorf("unsupported map key type for navigation: %v", keyType)
		}
		return curr.MapKey(keyVal)
	}
	return curr.Field(key)
}

// Set replaces the value at the current node. It requires the 'old' value
// to enable patch reversibility and strict application checking.
func (n *Node) Set(old, new any) *Node {
	vOld := reflect.ValueOf(old)
	vNew := reflect.ValueOf(new)
	p := &valuePatch{
		oldVal: deepCopyValue(vOld),
		newVal: deepCopyValue(vNew),
	}
	if n.current != nil {
		p.cond, p.ifCond, p.unlessCond = n.current.conditions()
	}
	n.update(p)
	n.current = p
	return n
}

// Test adds a test operation to the current node. The patch application
// will fail if the value at this node does not match the expected value.
func (n *Node) Test(expected any) *Node {
	vExpected := reflect.ValueOf(expected)
	p := &testPatch{
		expected: deepCopyValue(vExpected),
	}
	if n.current != nil {
		p.cond, p.ifCond, p.unlessCond = n.current.conditions()
	}
	n.update(p)
	n.current = p
	return n
}

// Copy copies a value from another path to the current node.
func (n *Node) Copy(from string) *Node {
	p := &copyPatch{
		from: from,
		path: n.fullPath,
	}
	if n.current != nil {
		p.cond, p.ifCond, p.unlessCond = n.current.conditions()
	}
	n.update(p)
	n.current = p
	return n
}

// Move moves a value from another path to the current node.
func (n *Node) Move(from string) *Node {
	p := &movePatch{
		from: from,
		path: n.fullPath,
	}
	if n.current != nil {
		p.cond, p.ifCond, p.unlessCond = n.current.conditions()
	}
	n.update(p)
	n.current = p
	return n
}

// Log adds a log operation to the current node. It prints a message
// and the current value at the node during patch application.
func (n *Node) Log(message string) *Node {
	p := &logPatch{
		message: message,
	}
	if n.current != nil {
		p.cond, p.ifCond, p.unlessCond = n.current.conditions()
	}
	n.update(p)
	n.current = p
	return n
}

func (n *Node) ensurePatch() {
	if n.current != nil {
		return
	}
	var p diffPatch
	if n.typ == nil {
		p = &valuePatch{}
	} else {
		switch n.typ.Kind() {
		case reflect.Struct:
			p = &structPatch{fields: make(map[string]diffPatch)}
		case reflect.Slice:
			p = &slicePatch{}
		case reflect.Map:
			p = &mapPatch{
				added:    make(map[interface{}]reflect.Value),
				removed:  make(map[interface{}]reflect.Value),
				modified: make(map[interface{}]diffPatch),
				keyType:  n.typ.Key(),
			}
		case reflect.Ptr:
			p = &ptrPatch{}
		case reflect.Interface:
			p = &interfacePatch{}
		case reflect.Array:
			p = &arrayPatch{indices: make(map[int]diffPatch)}
		default:
			// For basic types, valuePatch is usually created by Set().
			p = &valuePatch{}
		}
	}
	n.current = p
	n.update(p)
}

// WithCondition attaches a local condition to the current node.
// This condition is evaluated against the value at this node during ApplyChecked.
func (n *Node) WithCondition(c any) *Node {
	n.ensurePatch()
	if n.current != nil {
		n.current.setCondition(c)
	}
	return n
}

// If attaches an 'if' condition to the current node. If the condition
// evaluates to false, the operation at this node is skipped.
func (n *Node) If(c any) *Node {
	n.ensurePatch()
	if n.current != nil {
		n.current.setIfCondition(c)
	}
	return n
}

// Unless attaches an 'unless' condition to the current node. If the condition
// evaluates to true, the operation at this node is skipped.
func (n *Node) Unless(c any) *Node {
	n.ensurePatch()
	if n.current != nil {
		n.current.setUnlessCondition(c)
	}
	return n
}

// Field returns a Node for the specified struct field. It automatically descends
// into pointers and interfaces if necessary.
func (n *Node) Field(name string) (*Node, error) {
	n = n.Elem()
	if n.typ.Kind() != reflect.Struct {
		return nil, fmt.Errorf("not a struct: %v", n.typ)
	}
	field, ok := n.typ.FieldByName(name)
	if !ok {
		return nil, fmt.Errorf("field not found: %s", name)
	}
	sp, ok := n.current.(*structPatch)
	if !ok {
		sp = &structPatch{fields: make(map[string]diffPatch)}
		n.update(sp)
		n.current = sp
	}
	return &Node{
		typ: field.Type,
		update: func(p diffPatch) {
			sp.fields[name] = p
		},
		current:  sp.fields[name],
		fullPath: n.fullPath + "/" + name,
	}, nil
}

// Index returns a Node for the specified array or slice index.
func (n *Node) Index(i int) (*Node, error) {
	n = n.Elem()
	kind := n.typ.Kind()
	if kind != reflect.Slice && kind != reflect.Array {
		return nil, fmt.Errorf("not a slice or array: %v", n.typ)
	}
	if kind == reflect.Array && (i < 0 || i >= n.typ.Len()) {
		return nil, fmt.Errorf("index out of bounds: %d", i)
	}
	if kind == reflect.Array {
		ap, ok := n.current.(*arrayPatch)
		if !ok {
			ap = &arrayPatch{indices: make(map[int]diffPatch)}
			n.update(ap)
			n.current = ap
		}
		return &Node{
			typ: n.typ.Elem(),
			update: func(p diffPatch) {
				ap.indices[i] = p
			},
			current:  ap.indices[i],
			fullPath: n.fullPath + "/" + strconv.Itoa(i),
		}, nil
	}
	sp, ok := n.current.(*slicePatch)
	if !ok {
		sp = &slicePatch{}
		n.update(sp)
		n.current = sp
	}
	var modOp *sliceOp
	for j := range sp.ops {
		if sp.ops[j].Index == i && sp.ops[j].Kind == opMod {
			modOp = &sp.ops[j]
			break
		}
	}
	if modOp == nil {
		sp.ops = append(sp.ops, sliceOp{
			Kind:  opMod,
			Index: i,
		})
		modOp = &sp.ops[len(sp.ops)-1]
	}
	return &Node{
		typ: n.typ.Elem(),
		update: func(p diffPatch) {
			modOp.Patch = p
		},
		current:  modOp.Patch,
		fullPath: n.fullPath + "/" + strconv.Itoa(i),
	}, nil
}

// MapKey returns a Node for the specified map key.
func (n *Node) MapKey(key any) (*Node, error) {
	n = n.Elem()
	if n.typ.Kind() != reflect.Map {
		return nil, fmt.Errorf("not a map: %v", n.typ)
	}
	vKey := reflect.ValueOf(key)
	if vKey.Type() != n.typ.Key() {
		return nil, fmt.Errorf("invalid key type: expected %v, got %v", n.typ.Key(), vKey.Type())
	}
	mp, ok := n.current.(*mapPatch)
	if !ok {
		mp = &mapPatch{
			added:    make(map[interface{}]reflect.Value),
			removed:  make(map[interface{}]reflect.Value),
			modified: make(map[interface{}]diffPatch),
			keyType:  n.typ.Key(),
		}
		n.update(mp)
		n.current = mp
	}
	return &Node{
		typ: n.typ.Elem(),
		update: func(p diffPatch) {
			mp.modified[key] = p
		},
		current:  mp.modified[key],
		fullPath: n.fullPath + "/" + fmt.Sprintf("%v", key),
	}, nil
}

// Elem returns a Node for the element type of a pointer or interface.
func (n *Node) Elem() *Node {
	if n.typ == nil || (n.typ.Kind() != reflect.Ptr && n.typ.Kind() != reflect.Interface) {
		return n
	}
	updateFunc := n.update
	var currentPatch diffPatch
	if n.typ.Kind() == reflect.Ptr {
		pp, ok := n.current.(*ptrPatch)
		if !ok {
			pp = &ptrPatch{}
			n.update(pp)
			n.current = pp
		}
		updateFunc = func(p diffPatch) { pp.elemPatch = p }
		currentPatch = pp.elemPatch
	} else {
		ip, ok := n.current.(*interfacePatch)
		if !ok {
			ip = &interfacePatch{}
			n.update(ip)
			n.current = ip
		}
		updateFunc = func(p diffPatch) { ip.elemPatch = p }
		currentPatch = ip.elemPatch
	}
	var nextTyp reflect.Type
	if n.typ.Kind() == reflect.Ptr {
		nextTyp = n.typ.Elem()
	}
	return &Node{
		typ:      nextTyp,
		update:   updateFunc,
		current:  currentPatch,
		fullPath: n.fullPath, // Elem doesn't add to path in JSON Pointer
	}
}

// Add appends an addition operation to a slice node.
func (n *Node) Add(i int, val any) error {
	if n.typ.Kind() != reflect.Slice {
		return fmt.Errorf("Add only supported on slices, got %v", n.typ)
	}
	v := reflect.ValueOf(val)
	if v.Type() != n.typ.Elem() {
		return fmt.Errorf("invalid value type: expected %v, got %v", n.typ.Elem(), v.Type())
	}
	sp, ok := n.current.(*slicePatch)
	if !ok {
		sp = &slicePatch{}
		n.update(sp)
		n.current = sp
	}
	sp.ops = append(sp.ops, sliceOp{
		Kind:  opAdd,
		Index: i,
		Val:   deepCopyValue(v),
	})
	return nil
}

// Delete appends a deletion operation to a slice or map node.
func (n *Node) Delete(keyOrIndex any, oldVal any) error {
	if n.typ.Kind() == reflect.Slice {
		i, ok := keyOrIndex.(int)
		if !ok {
			return fmt.Errorf("index must be int for slices")
		}
		vOld := reflect.ValueOf(oldVal)
		if vOld.Type() != n.typ.Elem() {
			return fmt.Errorf("invalid old value type: expected %v, got %v", n.typ.Elem(), vOld.Type())
		}
		sp, ok := n.current.(*slicePatch)
		if !ok {
			sp = &slicePatch{}
			n.update(sp)
			n.current = sp
		}
		sp.ops = append(sp.ops, sliceOp{
			Kind:  opDel,
			Index: i,
			Val:   deepCopyValue(vOld),
		})
		return nil
	}
	if n.typ.Kind() == reflect.Map {
		vKey := reflect.ValueOf(keyOrIndex)
		if vKey.Type() != n.typ.Key() {
			return fmt.Errorf("invalid key type: expected %v, got %v", n.typ.Key(), vKey.Type())
		}
		vOld := reflect.ValueOf(oldVal)
		if vOld.Type() != n.typ.Elem() {
			return fmt.Errorf("invalid old value type: expected %v, got %v", n.typ.Elem(), vOld.Type())
		}
		mp, ok := n.current.(*mapPatch)
		if !ok {
			mp = &mapPatch{
				added:    make(map[interface{}]reflect.Value),
				removed:  make(map[interface{}]reflect.Value),
				modified: make(map[interface{}]diffPatch),
				keyType:  n.typ.Key(),
			}
			n.update(mp)
			n.current = mp
		}
		mp.removed[keyOrIndex] = deepCopyValue(vOld)
		return nil
	}
	return fmt.Errorf("Delete only supported on slices and maps, got %v", n.typ)
}

// AddMapEntry adds a new entry to a map node.
func (n *Node) AddMapEntry(key, val any) error {
	if n.typ.Kind() != reflect.Map {
		return fmt.Errorf("AddMapEntry only supported on maps, got %v", n.typ)
	}
	vKey := reflect.ValueOf(key)
	if vKey.Type() != n.typ.Key() {
		return fmt.Errorf("invalid key type: expected %v, got %v", n.typ.Key(), vKey.Type())
	}
	vVal := reflect.ValueOf(val)
	if vVal.Type() != n.typ.Elem() {
		return fmt.Errorf("invalid value type: expected %v, got %v", n.typ.Elem(), vVal.Type())
	}
	mp, ok := n.current.(*mapPatch)
	if !ok {
		mp = &mapPatch{
			added:    make(map[interface{}]reflect.Value),
			removed:  make(map[interface{}]reflect.Value),
			modified: make(map[interface{}]diffPatch),
			keyType:  n.typ.Key(),
		}
		n.update(mp)
		n.current = mp
	}
	mp.added[key] = deepCopyValue(vVal)
	return nil
}
