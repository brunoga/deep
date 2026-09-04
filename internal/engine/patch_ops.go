package engine

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	icore "github.com/brunoga/deep/v6/internal/core"
	"github.com/brunoga/deep/v6/internal/unsafe"
)

// diffPatch is the internal recursive interface for all patch types.
type diffPatch interface {
	apply(root, v reflect.Value, path string)
	applyChecked(root, v reflect.Value, strict bool, path string) error
	applyResolved(root, v reflect.Value, path string, resolver ConflictResolver) error
	reverse() diffPatch
	format(indent int) string
	walk(path string, fn func(path string, op OpKind, old, new any) error) error
	toJSONPatch(path string) []map[string]any
	summary(path string) string
	dependencies(path string) (reads []string, writes []string)
}

// valuePatch handles replacement of basic types and full replacement of complex types.
type valuePatch struct {
	oldVal reflect.Value
	newVal reflect.Value
}

func newValuePatch(oldVal, newVal reflect.Value) *valuePatch {
	return &valuePatch{
		oldVal: oldVal,
		newVal: newVal,
	}
}

func (p *valuePatch) apply(root, v reflect.Value, path string) {
	if !v.CanSet() {
		unsafe.DisableRO(&v)
	}
	icore.SetValue(v, p.newVal)
}

func (p *valuePatch) applyChecked(root, v reflect.Value, strict bool, path string) error {
	if strict && p.oldVal.IsValid() {
		if v.IsValid() {
			convertedOldVal := icore.ConvertValue(p.oldVal, v.Type())
			if !icore.Equal(v.Interface(), convertedOldVal.Interface()) {
				return fmt.Errorf("value mismatch: expected %v, got %v", convertedOldVal, v)
			}
		} else {
			return fmt.Errorf("value mismatch: expected %v, got invalid", p.oldVal)
		}
	}

	p.apply(root, v, path)
	return nil
}

func (p *valuePatch) applyResolved(root, v reflect.Value, path string, resolver ConflictResolver) error {
	if resolver != nil {
		resolved, ok := resolver.Resolve(path, OpReplace, nil, nil, v, p.newVal)
		if !ok {
			return nil // Skipped by resolver
		}
		p.newVal = resolved
	}
	p.apply(root, v, path)
	return nil
}

func (p *valuePatch) dependencies(path string) (reads []string, writes []string) {
	return nil, []string{path}
}

func (p *valuePatch) reverse() diffPatch {
	return &valuePatch{oldVal: p.newVal, newVal: p.oldVal}
}

func (p *valuePatch) walk(path string, fn func(path string, op OpKind, old, new any) error) error {
	op := OpReplace
	if isNilValue(p.newVal) {
		op = OpRemove
	} else if isNilValue(p.oldVal) {
		op = OpAdd
	}
	return fn(path, op, icore.ValueToInterface(p.oldVal), icore.ValueToInterface(p.newVal))
}

func (p *valuePatch) format(indent int) string {
	if !p.oldVal.IsValid() && !p.newVal.IsValid() {
		return "nil"
	}
	oldStr := "nil"
	if p.oldVal.IsValid() {
		oldStr = fmt.Sprintf("%v", p.oldVal)
	}
	newStr := "nil"
	if p.newVal.IsValid() {
		newStr = fmt.Sprintf("%v", p.newVal)
	}
	return fmt.Sprintf("%s -> %s", oldStr, newStr)
}

func (p *valuePatch) toJSONPatch(path string) []map[string]any {
	fullPath := path
	if fullPath == "" {
		fullPath = "/"
	}
	var op map[string]any
	if !p.newVal.IsValid() {
		op = map[string]any{"op": "remove", "path": fullPath}
	} else if !p.oldVal.IsValid() {
		op = map[string]any{"op": "add", "path": fullPath, "value": icore.ValueToInterface(p.newVal)}
	} else {
		op = map[string]any{"op": "replace", "path": fullPath, "value": icore.ValueToInterface(p.newVal)}
	}
	return []map[string]any{op}
}

func (p *valuePatch) summary(path string) string {
	if !p.newVal.IsValid() {
		return fmt.Sprintf("Removed %s (was %v)", path, icore.ValueToInterface(p.oldVal))
	}
	if !p.oldVal.IsValid() {
		return fmt.Sprintf("Added %s: %v", path, icore.ValueToInterface(p.newVal))
	}
	return fmt.Sprintf("Updated %s from %v to %v", path, icore.ValueToInterface(p.oldVal), icore.ValueToInterface(p.newVal))
}

// copyPatch copies a value from another path.
type copyPatch struct {
	from string
	path string // target path for reversal
}

func (p *copyPatch) apply(root, v reflect.Value, path string) {
	to := path
	if p.path != "" && p.path[0] == '/' {
		to = p.path
	}
	_ = applyCopyOrMoveInternal(p.from, to, path, root, v, false)
}

func (p *copyPatch) applyChecked(root, v reflect.Value, strict bool, path string) error {
	to := path
	if p.path != "" && p.path[0] == '/' {
		to = p.path
	}
	return applyCopyOrMoveInternal(p.from, to, path, root, v, false)
}

func (p *copyPatch) applyResolved(root, v reflect.Value, path string, resolver ConflictResolver) error {
	if resolver != nil {
		_, ok := resolver.Resolve(path, OpCopy, nil, nil, v, reflect.Value{})
		if !ok {
			return nil
		}
	}
	return p.applyChecked(root, v, false, path)
}

func (p *copyPatch) dependencies(path string) (reads []string, writes []string) {
	return []string{p.from}, []string{path}
}

func (p *copyPatch) reverse() diffPatch {
	return &valuePatch{newVal: reflect.Value{}}
}

func (p *copyPatch) walk(path string, fn func(path string, op OpKind, old, new any) error) error {
	return fn(path, OpCopy, p.from, nil)
}

func (p *copyPatch) format(indent int) string {
	return fmt.Sprintf("Copy(from: %s)", p.from)
}

func (p *copyPatch) toJSONPatch(path string) []map[string]any {
	fullPath := path
	if fullPath == "" {
		fullPath = "/"
	}
	p.path = fullPath
	op := map[string]any{"op": "copy", "from": p.from, "path": fullPath}
	return []map[string]any{op}
}

func (p *copyPatch) summary(path string) string {
	return fmt.Sprintf("Copied %s to %s", p.from, path)
}

func applyCopyOrMoveInternal(from, to, currentPath string, root, v reflect.Value, isMove bool) error {
	rvRoot := root
	if rvRoot.Kind() == reflect.Pointer {
		rvRoot = rvRoot.Elem()
	}
	fromVal, err := icore.DeepPath(from).Resolve(rvRoot)
	if err != nil {
		return err
	}

	fromVal = icore.DeepCopyValue(fromVal)

	if isMove {
		if err := icore.DeepPath(from).Delete(rvRoot); err != nil {
			return err
		}
	}

	if v.IsValid() && v.CanSet() && (to == "" || to == currentPath) {
		icore.SetValue(v, fromVal)
	} else if to != "" && to != "/" {
		if err := icore.DeepPath(to).Set(rvRoot, fromVal); err != nil {
			return err
		}
	} else if to == "" || to == "/" {
		if rvRoot.CanSet() {
			icore.SetValue(rvRoot, fromVal)
		}
	}
	return nil
}

// aliasPatch makes its position hold the same object another path holds.
// Where copyPatch installs an independent deep copy, aliasPatch installs the
// resolved value as-is — for a pointer, that shares the object. Diff produces
// it for the second and later routes to a value referenced more than once, so
// the ordering is forgiving by construction: whether the from-subtree's
// operations ran before or after, they mutate the one object both paths hold.
type aliasPatch struct {
	from string
	// oldDst is the value the destination held before, kept for Reverse.
	oldDst reflect.Value
}

func (p *aliasPatch) apply(root, v reflect.Value, path string) {
	_ = p.applyChecked(root, v, false, path)
}

func (p *aliasPatch) applyChecked(root, v reflect.Value, strict bool, path string) error {
	rvRoot := root
	if rvRoot.Kind() == reflect.Pointer {
		rvRoot = rvRoot.Elem()
	}
	fromVal, err := icore.DeepPath(p.from).ResolveMember(rvRoot)
	if err != nil {
		return err
	}
	if v.IsValid() && v.CanSet() {
		icore.SetValue(v, fromVal)
		return nil
	}
	return icore.DeepPath(path).Set(rvRoot, fromVal)
}

func (p *aliasPatch) applyResolved(root, v reflect.Value, path string, resolver ConflictResolver) error {
	if resolver != nil {
		_, ok := resolver.Resolve(path, OpAlias, nil, nil, v, reflect.Value{})
		if !ok {
			return nil
		}
	}
	return p.applyChecked(root, v, false, path)
}

func (p *aliasPatch) dependencies(path string) (reads []string, writes []string) {
	return []string{p.from}, []string{path}
}

func (p *aliasPatch) reverse() diffPatch {
	if p.oldDst.IsValid() {
		return &valuePatch{newVal: p.oldDst}
	}
	return &valuePatch{newVal: reflect.Value{}}
}

func (p *aliasPatch) walk(path string, fn func(path string, op OpKind, old, new any) error) error {
	// Alias inverts the move/copy convention: the source path travels in `new`
	// so `old` can carry the prior destination value, which Reverse needs. The
	// flattener in deep.Diff undoes this.
	var old any
	if p.oldDst.IsValid() {
		old = icore.ValueToInterface(p.oldDst)
	}
	return fn(path, OpAlias, old, p.from)
}

func (p *aliasPatch) format(indent int) string {
	return fmt.Sprintf("Alias(from: %s)", p.from)
}

func (p *aliasPatch) toJSONPatch(path string) []map[string]any {
	fullPath := path
	if fullPath == "" {
		fullPath = "/"
	}
	// RFC 6902 has no aliasing — JSON values carry no identity. "copy" is the
	// closest faithful translation: values right, sharing lost.
	return []map[string]any{{"op": "copy", "from": p.from, "path": fullPath}}
}

func (p *aliasPatch) summary(path string) string {
	return fmt.Sprintf("Aliased %s to %s", p.from, path)
}

// movePatch moves a value from another path.
type movePatch struct {
	from string
	path string // target path for reversal
}

func (p *movePatch) apply(root, v reflect.Value, path string) {
	to := path
	if p.path != "" && p.path[0] == '/' {
		to = p.path
	}
	_ = applyCopyOrMoveInternal(p.from, to, path, root, v, true)
}

func (p *movePatch) applyChecked(root, v reflect.Value, strict bool, path string) error {
	to := path
	if p.path != "" && p.path[0] == '/' {
		to = p.path
	}
	return applyCopyOrMoveInternal(p.from, to, path, root, v, true)
}

func (p *movePatch) applyResolved(root, v reflect.Value, path string, resolver ConflictResolver) error {
	if resolver != nil {
		_, ok := resolver.Resolve(path, OpMove, nil, nil, v, reflect.Value{})
		if !ok {
			return nil
		}
	}
	return p.applyChecked(root, v, false, path)
}

func (p *movePatch) dependencies(path string) (reads []string, writes []string) {
	return []string{p.from}, []string{path, p.from}
}

func (p *movePatch) reverse() diffPatch {
	return &movePatch{from: p.path, path: p.from}
}

func (p *movePatch) walk(path string, fn func(path string, op OpKind, old, new any) error) error {
	return fn(path, OpMove, p.from, nil)
}

func (p *movePatch) format(indent int) string {
	return fmt.Sprintf("Move(from: %s)", p.from)
}

func (p *movePatch) toJSONPatch(path string) []map[string]any {
	fullPath := path
	if fullPath == "" {
		fullPath = "/"
	}
	p.path = fullPath // capture path for potential reversal
	op := map[string]any{"op": "move", "from": p.from, "path": fullPath}
	return []map[string]any{op}
}

func (p *movePatch) summary(path string) string {
	return fmt.Sprintf("Moved %s to %s", p.from, path)
}

func newPtrPatch(elemPatch diffPatch) *ptrPatch {
	return &ptrPatch{
		elemPatch: elemPatch,
	}
}

func newStructPatch() *structPatch {
	return &structPatch{}
}

// field returns the patch recorded for one field, or nil.
func (p *structPatch) field(name string) diffPatch {
	for _, f := range p.fields {
		if f.name == name {
			return f.patch
		}
	}
	return nil
}

// set records the patch for one field, replacing any patch already there.
func (p *structPatch) set(name string, patch diffPatch) {
	for i := range p.fields {
		if p.fields[i].name == name {
			p.fields[i].patch = patch
			return
		}
	}
	p.fields = append(p.fields, structField{name, patch})
}

func newMapPatch(keyType reflect.Type) *mapPatch {
	return &mapPatch{
		added:        make(map[any]reflect.Value),
		removed:      make(map[any]reflect.Value),
		modified:     make(map[any]diffPatch),
		originalKeys: make(map[any]any),
		keyType:      keyType,
	}
}

// ptrPatch handles changes to the content pointed to by a pointer.
type ptrPatch struct {
	elemPatch diffPatch
}

func (p *ptrPatch) apply(root, v reflect.Value, path string) {
	if v.IsNil() {
		val := reflect.New(v.Type().Elem())
		p.elemPatch.apply(root, val.Elem(), path)
		v.Set(val)
		return
	}
	p.elemPatch.apply(root, v.Elem(), path)
}

func (p *ptrPatch) applyChecked(root, v reflect.Value, strict bool, path string) error {
	if v.IsNil() {
		return fmt.Errorf("cannot apply pointer patch to nil value")
	}
	return p.elemPatch.applyChecked(root, v.Elem(), strict, path)
}

func (p *ptrPatch) applyResolved(root, v reflect.Value, path string, resolver ConflictResolver) error {
	if v.IsNil() {
		return fmt.Errorf("cannot apply pointer patch to nil value")
	}
	return p.elemPatch.applyResolved(root, v.Elem(), path, resolver)
}

func (p *ptrPatch) dependencies(path string) (reads []string, writes []string) {
	return p.elemPatch.dependencies(path)
}

func (p *ptrPatch) reverse() diffPatch {
	return &ptrPatch{
		elemPatch: p.elemPatch.reverse(),
	}
}

func (p *ptrPatch) walk(path string, fn func(path string, op OpKind, old, new any) error) error {
	return p.elemPatch.walk(path, fn)
}

func (p *ptrPatch) format(indent int) string {
	return p.elemPatch.format(indent)
}

func (p *ptrPatch) toJSONPatch(path string) []map[string]any {
	return p.elemPatch.toJSONPatch(path)
}

func (p *ptrPatch) summary(path string) string {
	return p.elemPatch.summary(path)
}

// interfacePatch handles changes to the value stored in an interface.
type interfacePatch struct {
	elemPatch diffPatch
}

func (p *interfacePatch) apply(root, v reflect.Value, path string) {
	if v.IsNil() {
		return
	}
	elem := v.Elem()
	newElem := reflect.New(elem.Type()).Elem()
	newElem.Set(elem)
	p.elemPatch.apply(root, newElem, path)
	v.Set(newElem)
}

func (p *interfacePatch) applyChecked(root, v reflect.Value, strict bool, path string) error {
	if v.IsNil() {
		return fmt.Errorf("cannot apply interface patch to nil value")
	}
	elem := v.Elem()
	newElem := reflect.New(elem.Type()).Elem()
	newElem.Set(elem)
	if err := p.elemPatch.applyChecked(root, newElem, strict, path); err != nil {
		return err
	}
	v.Set(newElem)
	return nil
}

func (p *interfacePatch) applyResolved(root, v reflect.Value, path string, resolver ConflictResolver) error {
	if v.IsNil() {
		return fmt.Errorf("cannot apply interface patch to nil value")
	}
	elem := v.Elem()
	newElem := reflect.New(elem.Type()).Elem()
	newElem.Set(elem)
	if err := p.elemPatch.applyResolved(root, newElem, path, resolver); err != nil {
		return err
	}
	v.Set(newElem)
	return nil
}

func (p *interfacePatch) dependencies(path string) (reads []string, writes []string) {
	return p.elemPatch.dependencies(path)
}

func (p *interfacePatch) reverse() diffPatch {
	return &interfacePatch{
		elemPatch: p.elemPatch.reverse(),
	}
}

func (p *interfacePatch) walk(path string, fn func(path string, op OpKind, old, new any) error) error {
	return p.elemPatch.walk(path, fn)
}

func (p *interfacePatch) format(indent int) string {
	return p.elemPatch.format(indent)
}

func (p *interfacePatch) toJSONPatch(path string) []map[string]any {
	return p.elemPatch.toJSONPatch(path)
}

func (p *interfacePatch) summary(path string) string {
	return p.elemPatch.summary(path)
}

// structField is one field's patch, kept alongside its name.
type structField struct {
	name  string
	patch diffPatch
}

// structPatch handles field-level modifications in a struct.
type structPatch struct {
	// fields is a slice rather than a map: a struct has few of them, so the
	// linear scan is cheaper than a map allocation per struct, and keeping
	// them in declaration order makes the operations a diff produces
	// reproducible instead of following map iteration order.
	fields []structField
}

func (p *structPatch) apply(root, v reflect.Value, path string) {
	effectivePatches, order, err := resolveStructDependencies(p, path, root)
	if err != nil {
		panic(fmt.Sprintf("dependency resolution failed: %v", err))
	}

	for _, name := range order {
		patch := effectivePatches[name]
		info := icore.GetTypeInfo(v.Type())
		var f reflect.Value
		for _, fInfo := range info.Fields {
			if fInfo.Name == name {
				f = v.Field(fInfo.Index)
				break
			}
		}
		if f.IsValid() {
			if !f.CanSet() {
				unsafe.DisableRO(&f)
			}
			subPath := icore.JoinPath(path, name)
			patch.apply(root, f, subPath)
		}
	}
}

func (p *structPatch) applyChecked(root, v reflect.Value, strict bool, path string) error {
	effectivePatches, order, err := resolveStructDependencies(p, path, root)
	if err != nil {
		return err
	}

	var errs []error

	processField := func(name string) {
		patch := effectivePatches[name]
		info := icore.GetTypeInfo(v.Type())
		var f reflect.Value
		for _, fInfo := range info.Fields {
			if fInfo.Name == name {
				f = v.Field(fInfo.Index)
				break
			}
		}
		if !f.IsValid() {
			errs = append(errs, fmt.Errorf("field %s not found", name))
			return
		}
		if !f.CanSet() {
			unsafe.DisableRO(&f)
		}

		subPath := icore.JoinPath(path, name)

		if err := patch.applyChecked(root, f, strict, subPath); err != nil {
			errs = append(errs, fmt.Errorf("field %s: %w", name, err))
		}
	}

	for _, name := range order {
		processField(name)
	}
	if len(errs) > 0 {
		return &ApplyError{errors: errs}
	}
	return nil
}

func (p *structPatch) applyResolved(root, v reflect.Value, path string, resolver ConflictResolver) error {
	effectivePatches, order, err := resolveStructDependencies(p, path, root)
	if err != nil {
		return err
	}

	processField := func(name string) error {
		patch := effectivePatches[name]
		info := icore.GetTypeInfo(v.Type())
		var f reflect.Value
		for _, fInfo := range info.Fields {
			if fInfo.Name == name {
				f = v.Field(fInfo.Index)
				break
			}
		}
		if !f.IsValid() {
			return fmt.Errorf("field %s not found", name)
		}
		if !f.CanSet() {
			unsafe.DisableRO(&f)
		}

		subPath := icore.JoinPath(path, name)

		if err := patch.applyResolved(root, f, subPath, resolver); err != nil {
			return fmt.Errorf("field %s: %w", name, err)
		}
		return nil
	}

	for _, name := range order {
		if err := processField(name); err != nil {
			return err
		}
	}
	return nil
}

func (p *structPatch) dependencies(path string) (reads []string, writes []string) {
	for _, sf := range p.fields {
		name, patch := sf.name, sf.patch
		fieldPath := icore.JoinPath(path, name)

		r, w := patch.dependencies(fieldPath)
		reads = append(reads, r...)
		writes = append(writes, w...)
	}
	return
}

func (p *structPatch) reverse() diffPatch {
	newFields := make([]structField, len(p.fields))
	for i, f := range p.fields {
		newFields[i] = structField{f.name, f.patch.reverse()}
	}
	return &structPatch{fields: newFields}
}

func (p *structPatch) walk(path string, fn func(path string, op OpKind, old, new any) error) error {
	for _, f := range p.fields {
		fullPath := path + "/" + f.name
		if err := f.patch.walk(fullPath, fn); err != nil {
			return err
		}
	}
	return nil
}

func (p *structPatch) format(indent int) string {
	var b strings.Builder
	b.WriteString("Struct{\n")
	prefix := strings.Repeat("  ", indent+1)
	for _, sf := range p.fields {
		name, patch := sf.name, sf.patch
		b.WriteString(fmt.Sprintf("%s%s: %s\n", prefix, name, patch.format(indent+1)))
	}
	b.WriteString(strings.Repeat("  ", indent) + "}")
	return b.String()
}

func (p *structPatch) toJSONPatch(path string) []map[string]any {
	var ops []map[string]any
	for _, sf := range p.fields {
		name, patch := sf.name, sf.patch
		fullPath := path + "/" + name
		subOps := patch.toJSONPatch(fullPath)
		ops = append(ops, subOps...)
	}
	return ops
}

func (p *structPatch) summary(path string) string {
	var summaries []string
	for _, sf := range p.fields {
		name, patch := sf.name, sf.patch
		subPath := path
		if !strings.HasSuffix(subPath, "/") {
			subPath += "/"
		}
		subPath += name
		summaries = append(summaries, patch.summary(subPath))
	}
	return strings.Join(summaries, "\n")
}

// arrayPatch handles index-level modifications in a fixed-size array.
type arrayPatch struct {
	indices map[int]diffPatch
}

func (p *arrayPatch) apply(root, v reflect.Value, path string) {
	for i, patch := range p.indices {
		if i < v.Len() {
			e := v.Index(i)
			if !e.CanSet() {
				unsafe.DisableRO(&e)
			}
			fullPath := icore.JoinPath(path, strconv.Itoa(i))
			patch.apply(root, e, fullPath)
		}
	}
}

func (p *arrayPatch) applyChecked(root, v reflect.Value, strict bool, path string) error {
	var errs []error
	for i, patch := range p.indices {
		if i >= v.Len() {
			errs = append(errs, fmt.Errorf("index %d out of bounds", i))
			continue
		}
		e := v.Index(i)
		if !e.CanSet() {
			unsafe.DisableRO(&e)
		}
		fullPath := icore.JoinPath(path, strconv.Itoa(i))
		if err := patch.applyChecked(root, e, strict, fullPath); err != nil {
			errs = append(errs, fmt.Errorf("index %d: %w", i, err))
		}
	}
	if len(errs) > 0 {
		return &ApplyError{errors: errs}
	}
	return nil
}

func (p *arrayPatch) applyResolved(root, v reflect.Value, path string, resolver ConflictResolver) error {
	for i, patch := range p.indices {
		if i >= v.Len() {
			return fmt.Errorf("index %d out of bounds", i)
		}
		e := v.Index(i)
		if !e.CanSet() {
			unsafe.DisableRO(&e)
		}

		subPath := icore.JoinPath(path, strconv.Itoa(i))

		if err := patch.applyResolved(root, e, subPath, resolver); err != nil {
			return fmt.Errorf("index %d: %w", i, err)
		}
	}
	return nil
}

func (p *arrayPatch) dependencies(path string) (reads []string, writes []string) {
	for i, patch := range p.indices {
		fullPath := icore.JoinPath(path, strconv.Itoa(i))
		r, w := patch.dependencies(fullPath)
		reads = append(reads, r...)
		writes = append(writes, w...)
	}
	return
}

func (p *arrayPatch) reverse() diffPatch {
	newIndices := make(map[int]diffPatch)
	for k, v := range p.indices {
		newIndices[k] = v.reverse()
	}
	return &arrayPatch{
		indices: newIndices,
	}
}

func (p *arrayPatch) walk(path string, fn func(path string, op OpKind, old, new any) error) error {
	for i, patch := range p.indices {
		fullPath := fmt.Sprintf("%s/%d", path, i)
		if err := patch.walk(fullPath, fn); err != nil {
			return err
		}
	}
	return nil
}

func (p *arrayPatch) format(indent int) string {
	var b strings.Builder
	b.WriteString("Array{\n")
	prefix := strings.Repeat("  ", indent+1)
	for i, patch := range p.indices {
		b.WriteString(fmt.Sprintf("%s[%d]: %s\n", prefix, i, patch.format(indent+1)))
	}
	b.WriteString(strings.Repeat("  ", indent) + "}")
	return b.String()
}

func (p *arrayPatch) toJSONPatch(path string) []map[string]any {
	var ops []map[string]any
	for i, patch := range p.indices {
		fullPath := fmt.Sprintf("%s/%d", path, i)
		subOps := patch.toJSONPatch(fullPath)
		ops = append(ops, subOps...)
	}
	return ops
}

func (p *arrayPatch) summary(path string) string {
	var summaries []string
	for i, patch := range p.indices {
		subPath := path
		if !strings.HasSuffix(subPath, "/") {
			subPath += "/"
		}
		subPath += strconv.Itoa(i)
		summaries = append(summaries, patch.summary(subPath))
	}
	return strings.Join(summaries, "\n")
}

// mapPatch handles additions, removals, and modifications in a map.
type mapPatch struct {
	added        map[any]reflect.Value
	removed      map[any]reflect.Value
	modified     map[any]diffPatch
	originalKeys map[any]any // Canonical -> Original
	keyType      reflect.Type
}

func (p *mapPatch) apply(root, v reflect.Value, path string) {
	if v.IsNil() {
		if len(p.added) > 0 {
			newMap := reflect.MakeMap(v.Type())
			v.Set(newMap)
		} else {
			return
		}
	}
	for k := range p.removed {
		v.SetMapIndex(p.getOriginalKey(k, v.Type().Key(), v), reflect.Value{})
	}
	for k, patch := range p.modified {
		keyVal := p.getOriginalKey(k, v.Type().Key(), v)
		fullPath := icore.JoinPath(path, fmt.Sprintf("%v", k))
		if cp, ok := patch.(*copyPatch); ok {
			_ = applyCopyOrMoveInternal(cp.from, fullPath, fullPath, root, reflect.Value{}, false)
			continue
		}
		if mp, ok := patch.(*movePatch); ok {
			_ = applyCopyOrMoveInternal(mp.from, fullPath, fullPath, root, reflect.Value{}, true)
			continue
		}
		elem := v.MapIndex(keyVal)
		if elem.IsValid() {
			newElem := reflect.New(elem.Type()).Elem()
			newElem.Set(elem)
			patch.apply(root, newElem, fullPath)
			v.SetMapIndex(keyVal, newElem)
		}
	}
	for k, val := range p.added {
		keyVal := p.getOriginalKey(k, v.Type().Key(), v)
		v.SetMapIndex(keyVal, icore.ConvertValue(val, v.Type().Elem()))
	}
}

func (p *mapPatch) getOriginalKey(k any, targetType reflect.Type, v reflect.Value) reflect.Value {
	if orig, ok := p.originalKeys[k]; ok {
		return icore.ConvertValue(reflect.ValueOf(orig), targetType)
	}

	// If it's a Keyer, we can search the target map for a matching canonical key.
	mv := v
	for mv.Kind() == reflect.Pointer || mv.Kind() == reflect.Interface {
		if mv.IsNil() {
			break
		}
		mv = mv.Elem()
	}

	if mv.Kind() == reflect.Map {
		iter := mv.MapRange()
		for iter.Next() {
			mk := iter.Key()
			if mk.CanInterface() {
				if keyer, ok := mk.Interface().(Keyer); ok {
					if keyer.CanonicalKey() == k {
						return mk
					}
				}
			}
		}
	}

	return icore.ConvertValue(reflect.ValueOf(k), targetType)
}

func (p *mapPatch) applyChecked(root, v reflect.Value, strict bool, path string) error {
	if v.IsNil() {
		if len(p.added) > 0 {
			newMap := reflect.MakeMap(v.Type())
			v.Set(newMap)
		} else if len(p.removed) > 0 || len(p.modified) > 0 {
			return fmt.Errorf("cannot modify/remove from nil map")
		}
	}
	var errs []error
	for k, oldVal := range p.removed {
		keyVal := p.getOriginalKey(k, v.Type().Key(), v)
		val := v.MapIndex(keyVal)
		if !val.IsValid() {
			errs = append(errs, fmt.Errorf("key %v not found for removal", k))
			continue
		}
		if strict && !icore.Equal(val.Interface(), oldVal.Interface()) {
			errs = append(errs, fmt.Errorf("map removal mismatch for key %v: expected %v, got %v", k, oldVal, val))
		}
	}
	for k, patch := range p.modified {
		keyVal := p.getOriginalKey(k, v.Type().Key(), v)
		fullPath := icore.JoinPath(path, fmt.Sprintf("%v", k))
		if cp, ok := patch.(*copyPatch); ok {
			if err := applyCopyOrMoveInternal(cp.from, fullPath, fullPath, root, reflect.Value{}, false); err != nil {
				errs = append(errs, fmt.Errorf("map copy from %s failed: %w", cp.from, err))
			}
			continue
		}
		if mp, ok := patch.(*movePatch); ok {
			if err := applyCopyOrMoveInternal(mp.from, fullPath, fullPath, root, reflect.Value{}, true); err != nil {
				errs = append(errs, fmt.Errorf("map move from %s failed: %w", mp.from, err))
			}
			continue
		}
		val := v.MapIndex(keyVal)
		if !val.IsValid() {
			errs = append(errs, fmt.Errorf("key %v not found for modification", k))
			continue
		}
		newElem := reflect.New(val.Type()).Elem()
		newElem.Set(val)
		if err := patch.applyChecked(root, newElem, strict, fullPath); err != nil {
			errs = append(errs, fmt.Errorf("key %v: %w", k, err))
		}
		v.SetMapIndex(keyVal, newElem)
	}
	for k := range p.removed {
		v.SetMapIndex(p.getOriginalKey(k, v.Type().Key(), v), reflect.Value{})
	}
	for k, val := range p.added {
		keyVal := p.getOriginalKey(k, v.Type().Key(), v)
		curr := v.MapIndex(keyVal)
		if strict && curr.IsValid() {
			errs = append(errs, fmt.Errorf("key %v already exists", k))
		}
		v.SetMapIndex(keyVal, icore.ConvertValue(val, v.Type().Elem()))
	}
	if len(errs) > 0 {
		return &ApplyError{errors: errs}
	}
	return nil
}

func (p *mapPatch) applyResolved(root, v reflect.Value, path string, resolver ConflictResolver) error {
	if v.IsNil() {
		if len(p.added) > 0 {
			newMap := reflect.MakeMap(v.Type())
			v.Set(newMap)
		} else if len(p.removed) > 0 || len(p.modified) > 0 {
			return fmt.Errorf("cannot modify/remove from nil map")
		}
	}

	// Removals
	for k, val := range p.removed {
		subPath := icore.JoinPath(path, fmt.Sprintf("%v", k))
		keyVal := p.getOriginalKey(k, v.Type().Key(), v)
		current := v.MapIndex(keyVal)

		if resolver != nil {
			_, ok := resolver.Resolve(subPath, OpRemove, k, nil, current, reflect.Value{})
			if !ok {
				continue
			}
		}
		v.SetMapIndex(keyVal, reflect.Value{})
		_ = val
	}

	// Modifications
	for k, patch := range p.modified {
		keyVal := p.getOriginalKey(k, v.Type().Key(), v)
		val := v.MapIndex(keyVal)
		if !val.IsValid() {
			continue
		}

		subPath := icore.JoinPath(path, fmt.Sprintf("%v", k))

		newElem := reflect.New(val.Type()).Elem()
		newElem.Set(val)
		if err := patch.applyResolved(root, newElem, subPath, resolver); err != nil {
			return fmt.Errorf("key %v: %w", k, err)
		}
		v.SetMapIndex(keyVal, newElem)
	}

	// Additions
	for k, val := range p.added {
		subPath := icore.JoinPath(path, fmt.Sprintf("%v", k))

		if resolver != nil {
			resolved, ok := resolver.Resolve(subPath, OpAdd, k, nil, reflect.Value{}, val)
			if !ok {
				continue
			}
			val = resolved
		}
		v.SetMapIndex(p.getOriginalKey(k, v.Type().Key(), v), icore.ConvertValue(val, v.Type().Elem()))
	}
	return nil
}

func (p *mapPatch) dependencies(path string) (reads []string, writes []string) {
	for k, patch := range p.modified {
		fullPath := icore.JoinPath(path, fmt.Sprintf("%v", k))
		r, w := patch.dependencies(fullPath)
		reads = append(reads, r...)
		writes = append(writes, w...)
	}
	for k := range p.added {
		writes = append(writes, icore.JoinPath(path, fmt.Sprintf("%v", k)))
	}
	for k := range p.removed {
		writes = append(writes, icore.JoinPath(path, fmt.Sprintf("%v", k)))
	}
	return
}

func (p *mapPatch) reverse() diffPatch {
	newModified := make(map[any]diffPatch)
	for k, v := range p.modified {
		newModified[k] = v.reverse()
	}
	return &mapPatch{
		added:    p.removed,
		removed:  p.added,
		modified: newModified,
		keyType:  p.keyType,
	}
}

// keyPath joins path and a map or slice key as a JSON Pointer, escaping the
// key per RFC 6901 so keys containing "/" or "~" survive the round-trip
// through the flat operation form.
func keyPath(path string, k any) string {
	return path + "/" + icore.EscapeKey(fmt.Sprintf("%v", k))
}

func (p *mapPatch) walk(path string, fn func(path string, op OpKind, old, new any) error) error {
	// Keys are visited in a fixed order rather than the map's own, so that
	// diffing the same pair of values twice produces the same operations in
	// the same order — which is what makes a patch worth logging, caching,
	// comparing or signing.
	for _, k := range sortedMapKeys(p.added) {
		if err := fn(keyPath(path, k), OpAdd, nil, icore.ValueToInterface(p.added[k])); err != nil {
			return err
		}
	}
	for _, k := range sortedMapKeys(p.removed) {
		if err := fn(keyPath(path, k), OpRemove, icore.ValueToInterface(p.removed[k]), nil); err != nil {
			return err
		}
	}
	for _, k := range sortedMapKeys(p.modified) {
		if err := p.modified[k].walk(keyPath(path, k), fn); err != nil {
			return err
		}
	}
	return nil
}

// sortedMapKeys returns a map's keys ordered by their rendered form, which is
// also the form they take in a path.
func sortedMapKeys[V any](m map[any]V) []any {
	if len(m) == 0 {
		return nil
	}
	keys := make([]any, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return fmt.Sprintf("%v", keys[i]) < fmt.Sprintf("%v", keys[j])
	})
	return keys
}

func (p *mapPatch) format(indent int) string {
	var b strings.Builder
	b.WriteString("Map{\n")
	prefix := strings.Repeat("  ", indent+1)
	for k, v := range p.added {
		b.WriteString(fmt.Sprintf("%s+ %v: %v\n", prefix, k, v))
	}
	for k := range p.removed {
		b.WriteString(fmt.Sprintf("%s- %v\n", prefix, k))
	}
	for k, patch := range p.modified {
		b.WriteString(fmt.Sprintf("%s  %v: %s\n", prefix, k, patch.format(indent+1)))
	}
	b.WriteString(strings.Repeat("  ", indent) + "}")
	return b.String()
}

func (p *mapPatch) toJSONPatch(path string) []map[string]any {
	var ops []map[string]any
	for k := range p.removed {
		fullPath := keyPath(path, k)
		op := map[string]any{"op": "remove", "path": fullPath}
		ops = append(ops, op)
	}
	for k, patch := range p.modified {
		fullPath := keyPath(path, k)
		subOps := patch.toJSONPatch(fullPath)
		ops = append(ops, subOps...)
	}
	for k, val := range p.added {
		fullPath := keyPath(path, k)
		op := map[string]any{"op": "add", "path": fullPath, "value": icore.ValueToInterface(val)}
		ops = append(ops, op)
	}
	return ops
}

func (p *mapPatch) summary(path string) string {
	var summaries []string
	prefix := path
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	for k, val := range p.added {
		subPath := prefix + fmt.Sprintf("%v", k)
		summaries = append(summaries, fmt.Sprintf("Added %s: %v", subPath, icore.ValueToInterface(val)))
	}
	for k, oldVal := range p.removed {
		subPath := prefix + fmt.Sprintf("%v", k)
		summaries = append(summaries, fmt.Sprintf("Removed %s (was %v)", subPath, icore.ValueToInterface(oldVal)))
	}
	for k, patch := range p.modified {
		subPath := prefix + fmt.Sprintf("%v", k)
		summaries = append(summaries, patch.summary(subPath))
	}
	return strings.Join(summaries, "\n")
}

type sliceOp struct {
	Kind    OpKind
	Index   int
	From    int // For OpMove
	Val     reflect.Value
	Patch   diffPatch
	Key     any // Key of the element being operated on (if keyed)
	PrevKey any // Key of the previous element in the target slice (for insertion/move)
}

// ConflictResolver allows custom logic to be injected during patch application.
// It is used to implement CRDTs, 3-way merges, and other conflict resolution strategies.
type ConflictResolver interface {
	// Resolve allows the resolver to intervene before an operation is applied.
	// It returns the value to be applied and true if the operation should proceed,
	// or the zero reflect.Value and false to skip it.
	Resolve(path string, op OpKind, key, prevKey any, current, proposed reflect.Value) (reflect.Value, bool)
}

// slicePatch handles complex edits (insertions, deletions, modifications) in a slice.
type slicePatch struct {
	ops []sliceOp
	// oldSlice and newSlice are the whole slices this patch was computed from.
	// They are what walk emits when the edit script cannot be expressed as
	// independent flat operations — see indexStable.
	oldSlice reflect.Value
	newSlice reflect.Value
}

// indexStable reports whether ops can be flattened into independent operations.
// The ops of a slicePatch form an edit script: indices refer to positions in
// the original slice and apply interprets them with a moving cursor. Flattened
// operations, in contrast, are applied one at a time against a slice that has
// already been mutated by the previous ones, so any structural change to an
// unkeyed slice invalidates every index after it. Keyed elements address by
// key rather than position, so they stay stable.
func (p *slicePatch) indexStable() bool {
	for _, op := range p.ops {
		if op.Key != nil {
			continue
		}
		if op.Kind == OpAdd || op.Kind == OpRemove || op.Kind == OpMove {
			return false
		}
	}
	return true
}

func (p *slicePatch) apply(root, v reflect.Value, path string) {
	newSlice := reflect.MakeSlice(v.Type(), 0, v.Len())
	curIdx := 0
	for _, op := range p.ops {
		if op.Index > curIdx {
			for k := curIdx; k < op.Index; k++ {
				if k < v.Len() {
					newSlice = reflect.Append(newSlice, v.Index(k))
				}
			}
			curIdx = op.Index
		}
		switch op.Kind {
		case OpAdd:
			newSlice = reflect.Append(newSlice, icore.ConvertValue(op.Val, v.Type().Elem()))
		case OpRemove:
			curIdx++
		case OpCopy, OpMove:
			elem := reflect.New(v.Type().Elem()).Elem()
			if cp, ok := op.Patch.(*copyPatch); ok {
				_ = applyCopyOrMoveInternal(cp.from, "", "", root, elem, false)
			} else if mp, ok := op.Patch.(*movePatch); ok {
				_ = applyCopyOrMoveInternal(mp.from, "", "", root, elem, true)
			}
			newSlice = reflect.Append(newSlice, elem)
			curIdx++
		case OpReplace:
			if curIdx < v.Len() {
				elem := reflect.New(v.Type().Elem()).Elem()
				elem.Set(icore.DeepCopyValue(v.Index(curIdx)))
				if op.Patch != nil {
					fullPath := icore.JoinPath(path, strconv.Itoa(curIdx))
					op.Patch.apply(root, elem, fullPath)
				}
				newSlice = reflect.Append(newSlice, elem)
				curIdx++
			}
		}
	}
	for k := curIdx; k < v.Len(); k++ {
		newSlice = reflect.Append(newSlice, v.Index(k))
	}
	v.Set(newSlice)
}

func (p *slicePatch) applyChecked(root, v reflect.Value, strict bool, path string) error {
	newSlice := reflect.MakeSlice(v.Type(), 0, v.Len())
	curIdx := 0
	var errs []error
	for _, op := range p.ops {
		if op.Index > curIdx {
			for k := curIdx; k < op.Index; k++ {
				if k < v.Len() {
					newSlice = reflect.Append(newSlice, v.Index(k))
				}
			}
			curIdx = op.Index
		}
		switch op.Kind {
		case OpAdd:
			newSlice = reflect.Append(newSlice, icore.ConvertValue(op.Val, v.Type().Elem()))
		case OpRemove:
			if curIdx >= v.Len() {
				errs = append(errs, fmt.Errorf("slice deletion index %d out of bounds", curIdx))
				continue
			}
			curr := v.Index(curIdx)
			if strict && op.Val.IsValid() {
				convertedVal := icore.ConvertValue(op.Val, v.Type().Elem())
				if !icore.Equal(curr.Interface(), convertedVal.Interface()) {
					errs = append(errs, fmt.Errorf("slice deletion mismatch at %d: expected %v, got %v", curIdx, convertedVal, curr))
				}
			}
			curIdx++
		case OpCopy, OpMove:
			elem := reflect.New(v.Type().Elem()).Elem()
			if cp, ok := op.Patch.(*copyPatch); ok {
				if err := applyCopyOrMoveInternal(cp.from, "", "", root, elem, false); err != nil {
					errs = append(errs, fmt.Errorf("slice copy from %s failed: %w", cp.from, err))
				}
			} else if mp, ok := op.Patch.(*movePatch); ok {
				if err := applyCopyOrMoveInternal(mp.from, "", "", root, elem, true); err != nil {
					errs = append(errs, fmt.Errorf("slice move from %s failed: %w", mp.from, err))
				}
			}
			newSlice = reflect.Append(newSlice, elem)
			curIdx++
		case OpReplace:
			if curIdx >= v.Len() {
				errs = append(errs, fmt.Errorf("slice modification index %d out of bounds", curIdx))
				continue
			}
			elem := reflect.New(v.Type().Elem()).Elem()
			elem.Set(icore.DeepCopyValue(v.Index(curIdx)))
			if op.Patch != nil {
				fullPath := icore.JoinPath(path, strconv.Itoa(curIdx))
				if err := op.Patch.applyChecked(root, elem, strict, fullPath); err != nil {
					errs = append(errs, fmt.Errorf("slice index %d: %w", curIdx, err))
				}
			}
			newSlice = reflect.Append(newSlice, elem)
			curIdx++
		}
	}
	for k := curIdx; k < v.Len(); k++ {
		newSlice = reflect.Append(newSlice, v.Index(k))
	}
	v.Set(newSlice)
	if len(errs) > 0 {
		return &ApplyError{errors: errs}
	}
	return nil
}

func (p *slicePatch) applyResolved(root, v reflect.Value, path string, resolver ConflictResolver) error {
	if resolver == nil {
		return p.applyChecked(root, v, false, path)
	}

	keyField, hasKey := icore.GetKeyField(v.Type().Elem())

	if !hasKey {
		newSlice := reflect.MakeSlice(v.Type(), 0, v.Len())
		curIdx := 0
		for _, op := range p.ops {
			if op.Index > curIdx {
				for k := curIdx; k < op.Index; k++ {
					if k < v.Len() {
						newSlice = reflect.Append(newSlice, v.Index(k))
					}
				}
				curIdx = op.Index
			}

			subPath := icore.JoinPath(path, strconv.Itoa(curIdx))

			switch op.Kind {
			case OpAdd:
				resolved, ok := resolver.Resolve(subPath, OpAdd, nil, nil, reflect.Value{}, op.Val)
				if ok {
					newSlice = reflect.Append(newSlice, icore.ConvertValue(resolved, v.Type().Elem()))
				}
			case OpRemove:
				var current reflect.Value
				if curIdx < v.Len() {
					current = v.Index(curIdx)
				}
				_, ok := resolver.Resolve(subPath, OpRemove, nil, nil, current, reflect.Value{})
				if ok {
					curIdx++
				} else {
					if curIdx < v.Len() {
						newSlice = reflect.Append(newSlice, v.Index(curIdx))
						curIdx++
					}
				}
			case OpReplace:
				if curIdx < v.Len() {
					elem := reflect.New(v.Type().Elem()).Elem()
					elem.Set(icore.DeepCopyValue(v.Index(curIdx)))
					if op.Patch != nil {
						if err := op.Patch.applyResolved(root, elem, subPath, resolver); err != nil {
							return err
						}
					}
					newSlice = reflect.Append(newSlice, elem)
					curIdx++
				}
			}
		}
		for k := curIdx; k < v.Len(); k++ {
			newSlice = reflect.Append(newSlice, v.Index(k))
		}
		v.Set(newSlice)
		return nil
	}

	type elemInfo struct {
		val   reflect.Value
		index int
	}
	existingMap := make(map[any]*elemInfo)
	var orderedKeys []any

	for i := 0; i < v.Len(); i++ {
		val := v.Index(i)
		k := icore.ExtractKey(val, keyField)
		existingMap[k] = &elemInfo{val: val, index: i}
		orderedKeys = append(orderedKeys, k)
	}

	for _, op := range p.ops {
		subPath := icore.JoinPath(path, fmt.Sprintf("%v", op.Key))

		switch op.Kind {
		case OpRemove:
			var current reflect.Value
			if info, ok := existingMap[op.Key]; ok {
				current = info.val
			}
			_, ok := resolver.Resolve(subPath, OpRemove, op.Key, nil, current, reflect.Value{})
			if ok {
				delete(existingMap, op.Key)
			}
		case OpReplace:
			if info, ok := existingMap[op.Key]; ok {
				newVal := reflect.New(v.Type().Elem()).Elem()
				newVal.Set(icore.DeepCopyValue(info.val))
				if err := op.Patch.applyResolved(root, newVal, subPath, resolver); err == nil {
					info.val = newVal
				}
			}
		}
	}

	for _, op := range p.ops {
		if op.Kind == OpAdd {
			subPath := icore.JoinPath(path, fmt.Sprintf("%v", op.Key))
			resolved, ok := resolver.Resolve(subPath, OpAdd, op.Key, op.PrevKey, reflect.Value{}, op.Val)
			if ok {
				insertIdx := 0
				foundPrev := false
				if op.PrevKey != nil {
					for i, k := range orderedKeys {
						if k == op.PrevKey {
							insertIdx = i + 1
							foundPrev = true
							break
						}
					}
					if !foundPrev {
						insertIdx = 0
					}
				}

				for insertIdx < len(orderedKeys) {
					k1 := fmt.Sprintf("%v", op.Key)
					k2 := fmt.Sprintf("%v", orderedKeys[insertIdx])

					if k1 > k2 {
						insertIdx++
					} else {
						break
					}
				}

				if insertIdx >= len(orderedKeys) {
					orderedKeys = append(orderedKeys, op.Key)
				} else {
					orderedKeys = append(orderedKeys, nil)
					copy(orderedKeys[insertIdx+1:], orderedKeys[insertIdx:])
					orderedKeys[insertIdx] = op.Key
				}
				existingMap[op.Key] = &elemInfo{val: icore.ConvertValue(resolved, v.Type().Elem())}

			}
		}
	}

	newSlice := reflect.MakeSlice(v.Type(), 0, 0)
	seen := make(map[any]bool)

	for _, k := range orderedKeys {
		if _, exists := existingMap[k]; exists && !seen[k] {
			newSlice = reflect.Append(newSlice, existingMap[k].val)
			seen[k] = true
		}
	}

	v.Set(newSlice)
	return nil
}

func (p *slicePatch) dependencies(path string) (reads []string, writes []string) {
	writes = append(writes, path)
	for _, op := range p.ops {
		if op.Patch != nil {
			r, w := op.Patch.dependencies(icore.JoinPath(path, "?"))
			reads = append(reads, r...)
			writes = append(writes, w...)
		}
	}
	return
}

func (p *slicePatch) reverse() diffPatch {
	var revOps []sliceOp
	curA := 0
	curB := 0
	for _, op := range p.ops {
		delta := op.Index - curA
		curB += delta
		curA = op.Index
		switch op.Kind {
		case OpAdd:
			revOps = append(revOps, sliceOp{
				Kind:  OpRemove,
				Index: curB,
				Val:   op.Val,
				Key:   op.Key,
			})
			curB++
		case OpRemove:
			revOps = append(revOps, sliceOp{
				Kind:  OpAdd,
				Index: curB,
				Val:   op.Val,
				Key:   op.Key,
			})
			curA++
		case OpReplace:
			revOps = append(revOps, sliceOp{
				Kind:  OpReplace,
				Index: curB,
				Patch: op.Patch.reverse(),
				Key:   op.Key,
			})
			curA++
			curB++
		}
	}
	return &slicePatch{
		ops:      revOps,
		oldSlice: p.newSlice,
		newSlice: p.oldSlice,
	}
}

func (p *slicePatch) walk(path string, fn func(path string, op OpKind, old, new any) error) error {
	if !p.indexStable() && (p.oldSlice.IsValid() || p.newSlice.IsValid()) {
		// Structural changes to an unkeyed slice cannot survive being split
		// into independent operations; emit the whole slice instead.
		return fn(path, OpReplace, icore.ValueToInterface(p.oldSlice), icore.ValueToInterface(p.newSlice))
	}
	for _, op := range p.ops {
		fullPath := fmt.Sprintf("%s/%d", path, op.Index)
		if op.Key != nil {
			fullPath = keyPath(path, op.Key)
		}
		switch op.Kind {
		case OpAdd:
			if err := fn(fullPath, OpAdd, nil, icore.ValueToInterface(op.Val)); err != nil {
				return err
			}
		case OpRemove:
			if err := fn(fullPath, OpRemove, icore.ValueToInterface(op.Val), nil); err != nil {
				return err
			}
		case OpReplace:
			if op.Patch != nil {
				if err := op.Patch.walk(fullPath, fn); err != nil {
					return err
				}
			}
		case OpCopy:
			if cp, ok := op.Patch.(*copyPatch); ok {
				if err := fn(fullPath, OpCopy, cp.from, nil); err != nil {
					return err
				}
			} else if mp, ok := op.Patch.(*movePatch); ok {
				if err := fn(fullPath, OpMove, mp.from, nil); err != nil {
					return err
				}
			}
		case OpMove:
			if err := fn(fullPath, OpMove, op.From, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

func (p *slicePatch) format(indent int) string {
	var b strings.Builder
	b.WriteString("Slice{\n")
	prefix := strings.Repeat("  ", indent+1)
	for _, op := range p.ops {
		switch op.Kind {
		case OpAdd:
			b.WriteString(fmt.Sprintf("%s+ [%d]: %v\n", prefix, op.Index, op.Val))
		case OpRemove:
			b.WriteString(fmt.Sprintf("%s- [%d]\n", prefix, op.Index))
		case OpReplace:
			b.WriteString(fmt.Sprintf("%s  [%d]: %s\n", prefix, op.Index, op.Patch.format(indent+1)))
		}
	}
	b.WriteString(strings.Repeat("  ", indent) + "}")
	return b.String()
}

func (p *slicePatch) toJSONPatch(path string) []map[string]any {
	var ops []map[string]any

	shift := 0
	for _, op := range p.ops {
		fullPath := fmt.Sprintf("%s/%d", path, op.Index+shift)
		switch op.Kind {
		case OpAdd:
			jsonOp := map[string]any{"op": "add", "path": fullPath, "value": icore.ValueToInterface(op.Val)}
			ops = append(ops, jsonOp)
			shift++
		case OpRemove:
			jsonOp := map[string]any{"op": "remove", "path": fullPath}
			ops = append(ops, jsonOp)
			shift--
		case OpReplace:
			subOps := op.Patch.toJSONPatch(fullPath)
			ops = append(ops, subOps...)
		}
	}
	return ops
}

func (p *slicePatch) summary(path string) string {
	var summaries []string
	prefix := path
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	for _, op := range p.ops {
		subPath := prefix + strconv.Itoa(op.Index)
		if op.Key != nil {
			subPath = prefix + fmt.Sprintf("%v", op.Key)
		}
		switch op.Kind {
		case OpAdd:
			summaries = append(summaries, fmt.Sprintf("Added to %s: %v", subPath, icore.ValueToInterface(op.Val)))
		case OpRemove:
			summaries = append(summaries, fmt.Sprintf("Removed from %s: %v", subPath, icore.ValueToInterface(op.Val)))
		case OpReplace:
			if op.Patch != nil {
				summaries = append(summaries, op.Patch.summary(subPath))
			}
		}
	}
	return strings.Join(summaries, "\n")
}

type readOnlyPatch struct {
	inner diffPatch
}

func (p *readOnlyPatch) apply(root, v reflect.Value, path string) {
	// No-op for read-only fields
}

func (p *readOnlyPatch) applyChecked(root, v reflect.Value, strict bool, path string) error {
	// No-op for read-only fields
	return nil
}

func (p *readOnlyPatch) applyResolved(root, v reflect.Value, path string, resolver ConflictResolver) error {
	// No-op for read-only fields
	return nil
}

func (p *readOnlyPatch) reverse() diffPatch {
	return &readOnlyPatch{inner: p.inner.reverse()}
}

func (p *readOnlyPatch) format(indent int) string {
	return "ReadOnly(" + p.inner.format(indent) + ")"
}

func (p *readOnlyPatch) walk(path string, fn func(path string, op OpKind, old, new any) error) error {
	return p.inner.walk(path, fn)
}

func (p *readOnlyPatch) toJSONPatch(path string) []map[string]any {
	return nil
}

func (p *readOnlyPatch) summary(path string) string {
	return p.inner.summary(path)
}

func (p *readOnlyPatch) dependencies(path string) (reads []string, writes []string) {
	return p.inner.dependencies(path)
}

type customDiffPatch struct {
	patch any
	// oldValue and newValue are the values the custom differ was given. A
	// custom patch describes a change in terms only its own type understands,
	// which is what makes it efficient to apply — but the flat operation form
	// has no way to carry that. Keeping the values lets the flat form fall back
	// to replacing the whole value, which any consumer can apply. Without them
	// a custom differ flattens to nothing at all and the change is silently
	// lost.
	oldValue any
	newValue any
}

func (p *customDiffPatch) apply(root, v reflect.Value, path string) {
	m := reflect.ValueOf(p.patch).MethodByName("Apply")
	if m.IsValid() {
		m.Call([]reflect.Value{v.Addr()})
	}
}

func (p *customDiffPatch) applyChecked(root, v reflect.Value, strict bool, path string) error {
	// Custom patch should implement ApplyChecked ideally.
	// If not, just Apply.
	m := reflect.ValueOf(p.patch).MethodByName("ApplyChecked")
	if m.IsValid() {
		res := m.Call([]reflect.Value{v.Addr()})
		if !res[0].IsNil() {
			return res[0].Interface().(error)
		}
		return nil
	}
	p.apply(root, v, path)
	return nil
}

func (p *customDiffPatch) applyResolved(root, v reflect.Value, path string, resolver ConflictResolver) error {
	m := reflect.ValueOf(p.patch).MethodByName("ApplyResolved")
	if m.IsValid() {
		res := m.Call([]reflect.Value{v.Addr(), reflect.ValueOf(resolver)})
		if !res[0].IsNil() {
			return res[0].Interface().(error)
		}
		return nil
	}
	return p.applyChecked(root, v, false, path)
}

func (p *customDiffPatch) reverse() diffPatch {
	m := reflect.ValueOf(p.patch).MethodByName("Reverse")
	if m.IsValid() {
		res := m.Call(nil)
		return &customDiffPatch{
			patch:    res[0].Interface(),
			oldValue: p.newValue,
			newValue: p.oldValue,
		}
	}
	return p // Cannot reverse?
}

func (p *customDiffPatch) format(indent int) string {
	return fmt.Sprintf("CustomPatch(%v)", p.patch)
}

func (p *customDiffPatch) dependencies(path string) (reads []string, writes []string) {
	m := reflect.ValueOf(p.patch).MethodByName("Dependencies")
	if m.IsValid() {
		res := m.Call([]reflect.Value{reflect.ValueOf(path)})
		// Expects ([]string, []string)
		return res[0].Interface().([]string), res[1].Interface().([]string)
	}
	return nil, nil
}

// flatOperationer is a custom patch that can say what to put in the flat
// operation form. Without it the flat form has to carry the whole new value,
// which for something like a large document means sending all of it to describe
// a small change; a patch that knows how to describe itself compactly says so
// here.
type flatOperationer interface {
	FlatOperation() (old, new any)
}

func (p *customDiffPatch) walk(path string, fn func(path string, op OpKind, old, new any) error) error {
	if flat, ok := p.patch.(flatOperationer); ok {
		old, new := flat.FlatOperation()
		return fn(path, OpReplace, old, new)
	}
	if p.oldValue == nil && p.newValue == nil {
		return nil
	}
	// The custom patch cannot describe itself as operations, so the flat form
	// says what the value became rather than how it got there.
	return fn(path, OpReplace, p.oldValue, p.newValue)
}

func (p *customDiffPatch) toJSONPatch(path string) []map[string]any {
	m := reflect.ValueOf(p.patch).MethodByName("ToJSONPatch")
	if m.IsValid() {
		res := m.Call(nil)
		// Return type is ([]byte, error)
		if !res[1].IsNil() {
			return nil
		}
		bytes := res[0].Bytes()
		var ops []map[string]any
		if err := json.Unmarshal(bytes, &ops); err == nil {
			return ops
		}
	}
	return nil
}

func (p *customDiffPatch) summary(path string) string {
	type summarizer interface {
		Summary() string
	}
	if s, ok := p.patch.(summarizer); ok {
		return s.Summary()
	}
	return "CustomPatch"
}
