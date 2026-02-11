package deep

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/brunoga/deep/v2/internal/unsafe"
)

var ErrConditionSkipped = fmt.Errorf("condition skipped")

// diffPatch is the internal recursive interface for all patch types.
type diffPatch interface {
	apply(root, v reflect.Value)
	applyChecked(root, v reflect.Value, strict bool) error
	reverse() diffPatch
	format(indent int) string
	setCondition(cond any)
	setIfCondition(cond any)
	setUnlessCondition(cond any)
	conditions() (cond, ifCond, unlessCond any)
	toJSONPatch(path string) []map[string]any
}

type patchMetadata struct {
	cond       any
	ifCond     any
	unlessCond any
}

func (m *patchMetadata) setCondition(cond any)       { m.cond = cond }
func (m *patchMetadata) setIfCondition(cond any)     { m.ifCond = cond }
func (m *patchMetadata) setUnlessCondition(cond any) { m.unlessCond = cond }
func (m *patchMetadata) conditions() (any, any, any) { return m.cond, m.ifCond, m.unlessCond }

func checkConditions(p diffPatch, root, v reflect.Value) error {
	cond, ifC, unlessC := p.conditions()
	if err := checkIfUnless(ifC, unlessC, root); err != nil {
		return err
	}
	return evaluateLocalCondition(cond, v)
}

func evaluateLocalCondition(cond any, v reflect.Value) error {
	if cond == nil {
		return nil
	}
	ok, err := evaluateCondition(cond, v)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("local condition failed for value %v", v.Interface())
	}
	return nil
}

func evaluateCondition(cond any, v reflect.Value) (bool, error) {
	if ic, ok := cond.(internalCondition); ok {
		return ic.evaluateAny(v.Interface())
	}

	// Fallback for types that might not implement the interface but have Evaluate method.
	// This maintains backward compatibility for custom types.
	method := reflect.ValueOf(cond).MethodByName("Evaluate")
	if !method.IsValid() {
		return false, fmt.Errorf("local condition: method Evaluate not found on %T", cond)
	}
	argType := method.Type().In(0)
	var arg reflect.Value
	if v.Type().AssignableTo(argType) {
		arg = v
	} else if reflect.PtrTo(v.Type()).AssignableTo(argType) {
		arg = reflect.New(v.Type())
		arg.Elem().Set(v)
	} else if v.Kind() == reflect.Ptr && v.Elem().Type().AssignableTo(argType) {
		arg = v.Elem()
	} else {
		// Try to convert
		if v.CanConvert(argType) {
			arg = v.Convert(argType)
		} else {
			return false, fmt.Errorf("cannot call Evaluate: argument type mismatch, expected %v, got %v", argType, v.Type())
		}
	}
	results := method.Call([]reflect.Value{arg})
	if !results[1].IsNil() {
		return false, results[1].Interface().(error)
	}
	return results[0].Bool(), nil
}

func checkIfUnless(ifCond, unlessCond any, v reflect.Value) error {
	if ifCond != nil {
		ok, err := evaluateCondition(ifCond, v)
		if err != nil {
			return err
		}
		if !ok {
			return ErrConditionSkipped
		}
	}
	if unlessCond != nil {
		ok, err := evaluateCondition(unlessCond, v)
		if err != nil {
			return err
		}
		if ok {
			return ErrConditionSkipped
		}
	}
	return nil
}

// valuePatch handles replacement of basic types and full replacement of complex types.
type valuePatch struct {
	patchMetadata
	oldVal reflect.Value
	newVal reflect.Value
}

func (p *valuePatch) apply(root, v reflect.Value) {
	if !v.CanSet() {
		unsafe.DisableRO(&v)
	}
	setValue(v, p.newVal)
}

func (p *valuePatch) applyChecked(root, v reflect.Value, strict bool) error {
	if err := checkConditions(p, root, v); err != nil {
		if err == ErrConditionSkipped {
			return nil
		}
		return err
	}
	if strict && p.oldVal.IsValid() {
		if v.IsValid() {
			convertedOldVal := convertValue(p.oldVal, v.Type())
			if !reflect.DeepEqual(v.Interface(), convertedOldVal.Interface()) {
				return fmt.Errorf("value mismatch: expected %v, got %v", convertedOldVal, v)
			}
		} else {
			return fmt.Errorf("value mismatch: expected %v, got invalid", p.oldVal)
		}
	}

	p.apply(root, v)
	return nil
}

func (p *valuePatch) reverse() diffPatch {
	return &valuePatch{oldVal: p.newVal, newVal: p.oldVal, patchMetadata: p.patchMetadata}
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
		op = map[string]any{"op": "add", "path": fullPath, "value": valueToInterface(p.newVal)}
	} else {
		op = map[string]any{"op": "replace", "path": fullPath, "value": valueToInterface(p.newVal)}
	}
	addConditionsToOp(op, p)
	return []map[string]any{op}
}

// testPatch handles equality checks without modifying the value.
type testPatch struct {
	patchMetadata
	expected reflect.Value
}

func (p *testPatch) apply(root, v reflect.Value) {
	// No-op
}

func (p *testPatch) applyChecked(root, v reflect.Value, strict bool) error {
	if err := checkConditions(p, root, v); err != nil {
		if err == ErrConditionSkipped {
			return nil
		}
		return err
	}
	if p.expected.IsValid() {
		if !v.IsValid() {
			return fmt.Errorf("test failed: expected %v, got invalid", p.expected)
		}
		convertedExpected := convertValue(p.expected, v.Type())
		if !reflect.DeepEqual(v.Interface(), convertedExpected.Interface()) {
			return fmt.Errorf("test failed: expected %v, got %v", convertedExpected, v)
		}
	}

	return nil
}

func (p *testPatch) reverse() diffPatch {
	return p // Reversing a test is still a test
}

func (p *testPatch) format(indent int) string {
	if p.expected.IsValid() {
		return fmt.Sprintf("Test(%v)", p.expected)
	}
	return "Test()"
}

func (p *testPatch) toJSONPatch(path string) []map[string]any {
	fullPath := path
	if fullPath == "" {
		fullPath = "/"
	}
	op := map[string]any{"op": "test", "path": fullPath, "value": valueToInterface(p.expected)}
	addConditionsToOp(op, p)
	return []map[string]any{op}
}

// copyPatch copies a value from another path.
type copyPatch struct {
	patchMetadata
	from string
	path string // target path for reversal
}

func (p *copyPatch) apply(root, v reflect.Value) {
	p.applyChecked(root, v, false)
}

func (p *copyPatch) applyChecked(root, v reflect.Value, strict bool) error {
	if err := checkConditions(p, root, v); err != nil {
		if err == ErrConditionSkipped {
			return nil
		}
		return err
	}
	rvRoot := root
	if rvRoot.Kind() == reflect.Ptr {
		rvRoot = rvRoot.Elem()
	}
	fromVal, err := Path(p.from).resolve(rvRoot)
	if err != nil {
		return fmt.Errorf("copy from %s failed: %w", p.from, err)
	}
	if !v.CanSet() {
		unsafe.DisableRO(&v)
	}
	setValue(v, fromVal)
	return nil
}

func (p *copyPatch) reverse() diffPatch {
	// Reversing a copy is a removal of the target.
	return &valuePatch{newVal: reflect.Value{}}
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
	addConditionsToOp(op, p)
	return []map[string]any{op}
}

// movePatch moves a value from another path.
type movePatch struct {
	patchMetadata
	from string
	path string // target path for reversal
}

func (p *movePatch) apply(root, v reflect.Value) {
	p.applyChecked(root, v, false)
}

func (p *movePatch) applyChecked(root, v reflect.Value, strict bool) error {
	if err := checkConditions(p, root, v); err != nil {
		if err == ErrConditionSkipped {
			return nil
		}
		return err
	}
	rvRoot := root
	if rvRoot.Kind() != reflect.Ptr {
		// We need a pointer to be able to delete/set values.
		return fmt.Errorf("root must be a pointer for move operation")
	}
	rvRoot = rvRoot.Elem()

	fromVal, err := Path(p.from).resolve(rvRoot)
	if err != nil {
		return fmt.Errorf("move from %s failed: %w", p.from, err)
	}

	// Deep copy because we might be deleting it from source next.
	fromVal = deepCopyValue(fromVal)

	// Remove from source.
	if err := Path(p.from).delete(rvRoot); err != nil {
		return fmt.Errorf("move delete from %s failed: %w", p.from, err)
	}

	if err := Path(p.path).set(rvRoot, fromVal); err != nil {
		return fmt.Errorf("move set to %s failed: %w", p.path, err)
	}
	return nil
}

func (p *movePatch) reverse() diffPatch {
	return &movePatch{from: p.path, path: p.from}
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
	addConditionsToOp(op, p)
	return []map[string]any{op}
}

// logPatch logs a message without modifying the value.
type logPatch struct {
	patchMetadata
	message string
}

func (p *logPatch) apply(root, v reflect.Value) {
	fmt.Printf("DEEP LOG: %s (value: %v)\n", p.message, v.Interface())
}

func (p *logPatch) applyChecked(root, v reflect.Value, strict bool) error {
	if err := checkConditions(p, root, v); err != nil {
		if err == ErrConditionSkipped {
			return nil
		}
		return err
	}
	p.apply(root, v)
	return nil
}

func (p *logPatch) reverse() diffPatch {
	return p // Reversing a log is still a log
}

func (p *logPatch) format(indent int) string {
	return fmt.Sprintf("Log(%q)", p.message)
}

func (p *logPatch) toJSONPatch(path string) []map[string]any {
	fullPath := path
	if fullPath == "" {
		fullPath = "/"
	}
	op := map[string]any{"op": "log", "path": fullPath, "value": p.message}
	addConditionsToOp(op, p)
	return []map[string]any{op}
}

// ptrPatch handles changes to the content pointed to by a pointer.

type ptrPatch struct {
	patchMetadata
	elemPatch diffPatch
}

func (p *ptrPatch) apply(root, v reflect.Value) {
	if v.IsNil() {
		val := reflect.New(v.Type().Elem())
		p.elemPatch.apply(root, val.Elem())
		v.Set(val)
		return
	}
	p.elemPatch.apply(root, v.Elem())
}

func (p *ptrPatch) applyChecked(root, v reflect.Value, strict bool) error {
	if err := checkConditions(p, root, v); err != nil {
		if err == ErrConditionSkipped {
			return nil
		}
		return err
	}
	if v.IsNil() {
		return fmt.Errorf("cannot apply pointer patch to nil value")
	}
	return p.elemPatch.applyChecked(root, v.Elem(), strict)
}

func (p *ptrPatch) reverse() diffPatch {
	return &ptrPatch{
		patchMetadata: p.patchMetadata,
		elemPatch:     p.elemPatch.reverse(),
	}
}

func (p *ptrPatch) format(indent int) string {
	return p.elemPatch.format(indent)
}

func (p *ptrPatch) toJSONPatch(path string) []map[string]any {
	ops := p.elemPatch.toJSONPatch(path)
	for _, op := range ops {
		addConditionsToOp(op, p)
	}
	return ops
}

// interfacePatch handles changes to the value stored in an interface.
type interfacePatch struct {
	patchMetadata
	elemPatch diffPatch
}

func (p *interfacePatch) apply(root, v reflect.Value) {
	if v.IsNil() {
		return
	}
	elem := v.Elem()
	newElem := reflect.New(elem.Type()).Elem()
	newElem.Set(elem)
	p.elemPatch.apply(root, newElem)
	v.Set(newElem)
}

func (p *interfacePatch) applyChecked(root, v reflect.Value, strict bool) error {
	if err := checkConditions(p, root, v); err != nil {
		if err == ErrConditionSkipped {
			return nil
		}
		return err
	}
	if v.IsNil() {
		return fmt.Errorf("cannot apply interface patch to nil value")
	}
	elem := v.Elem()
	newElem := reflect.New(elem.Type()).Elem()
	newElem.Set(elem)
	if err := p.elemPatch.applyChecked(root, newElem, strict); err != nil {
		return err
	}
	v.Set(newElem)
	return nil
}

func (p *interfacePatch) reverse() diffPatch {
	return &interfacePatch{
		patchMetadata: p.patchMetadata,
		elemPatch:     p.elemPatch.reverse(),
	}
}

func (p *interfacePatch) format(indent int) string {
	return p.elemPatch.format(indent)
}

func (p *interfacePatch) toJSONPatch(path string) []map[string]any {
	ops := p.elemPatch.toJSONPatch(path)
	for _, op := range ops {
		addConditionsToOp(op, p)
	}
	return ops
}

// structPatch handles field-level modifications in a struct.
type structPatch struct {
	patchMetadata
	fields map[string]diffPatch
}

func (p *structPatch) apply(root, v reflect.Value) {
	for name, patch := range p.fields {
		f := v.FieldByName(name)
		if f.IsValid() {
			if !f.CanSet() {
				unsafe.DisableRO(&f)
			}
			patch.apply(root, f)
		}
	}
}

func (p *structPatch) applyChecked(root, v reflect.Value, strict bool) error {
	if err := checkConditions(p, root, v); err != nil {
		if err == ErrConditionSkipped {
			return nil
		}
		return err
	}
	for name, patch := range p.fields {
		f := v.FieldByName(name)
		if !f.IsValid() {
			return fmt.Errorf("field %s not found", name)
		}
		if !f.CanSet() {
			unsafe.DisableRO(&f)
		}
		if err := patch.applyChecked(root, f, strict); err != nil {
			return fmt.Errorf("field %s: %w", name, err)
		}
	}
	return nil
}

func (p *structPatch) reverse() diffPatch {
	newFields := make(map[string]diffPatch)
	for k, v := range p.fields {
		newFields[k] = v.reverse()
	}
	return &structPatch{
		patchMetadata: p.patchMetadata,
		fields:        newFields,
	}
}

func (p *structPatch) format(indent int) string {
	var b strings.Builder
	b.WriteString("Struct{\n")
	prefix := strings.Repeat("  ", indent+1)
	for name, patch := range p.fields {
		b.WriteString(fmt.Sprintf("%s%s: %s\n", prefix, name, patch.format(indent+1)))
	}
	b.WriteString(strings.Repeat("  ", indent) + "}")
	return b.String()
}

func (p *structPatch) toJSONPatch(path string) []map[string]any {
	var ops []map[string]any
	for name, patch := range p.fields {
		fullPath := path + "/" + name
		subOps := patch.toJSONPatch(fullPath)
		for _, op := range subOps {
			addConditionsToOp(op, p)
		}
		ops = append(ops, subOps...)
	}
	return ops
}

// arrayPatch handles index-level modifications in a fixed-size array.
type arrayPatch struct {
	patchMetadata
	indices map[int]diffPatch
}

func (p *arrayPatch) apply(root, v reflect.Value) {
	for i, patch := range p.indices {
		if i < v.Len() {
			e := v.Index(i)
			if !e.CanSet() {
				unsafe.DisableRO(&e)
			}
			patch.apply(root, e)
		}
	}
}

func (p *arrayPatch) applyChecked(root, v reflect.Value, strict bool) error {
	if err := checkConditions(p, root, v); err != nil {
		if err == ErrConditionSkipped {
			return nil
		}
		return err
	}
	for i, patch := range p.indices {
		if i >= v.Len() {
			return fmt.Errorf("index %d out of bounds", i)
		}
		e := v.Index(i)
		if !e.CanSet() {
			unsafe.DisableRO(&e)
		}
		if err := patch.applyChecked(root, e, strict); err != nil {
			return fmt.Errorf("index %d: %w", i, err)
		}
	}
	return nil
}

func (p *arrayPatch) reverse() diffPatch {
	newIndices := make(map[int]diffPatch)
	for k, v := range p.indices {
		newIndices[k] = v.reverse()
	}
	return &arrayPatch{
		patchMetadata: p.patchMetadata,
		indices:       newIndices,
	}
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
		for _, op := range subOps {
			addConditionsToOp(op, p)
		}
		ops = append(ops, subOps...)
	}
	return ops
}

// mapPatch handles additions, removals, and modifications in a map.
type mapPatch struct {
	patchMetadata
	added    map[interface{}]reflect.Value
	removed  map[interface{}]reflect.Value
	modified map[interface{}]diffPatch
	keyType  reflect.Type
}

func (p *mapPatch) apply(root, v reflect.Value) {
	if v.IsNil() {
		if len(p.added) > 0 {
			newMap := reflect.MakeMap(v.Type())
			v.Set(newMap)
		} else {
			return
		}
	}
	for k := range p.removed {
		v.SetMapIndex(convertValue(reflect.ValueOf(k), v.Type().Key()), reflect.Value{})
	}
	for k, patch := range p.modified {
		keyVal := convertValue(reflect.ValueOf(k), v.Type().Key())
		elem := v.MapIndex(keyVal)
		if elem.IsValid() {
			newElem := reflect.New(elem.Type()).Elem()
			newElem.Set(elem)
			patch.apply(root, newElem)
			v.SetMapIndex(keyVal, newElem)
		}
	}
	for k, val := range p.added {
		keyVal := convertValue(reflect.ValueOf(k), v.Type().Key())
		v.SetMapIndex(keyVal, convertValue(val, v.Type().Elem()))
	}
}

func (p *mapPatch) applyChecked(root, v reflect.Value, strict bool) error {
	if err := checkConditions(p, root, v); err != nil {
		if err == ErrConditionSkipped {
			return nil
		}
		return err
	}
	if v.IsNil() {
		if len(p.added) > 0 {
			newMap := reflect.MakeMap(v.Type())
			v.Set(newMap)
		} else if len(p.removed) > 0 || len(p.modified) > 0 {
			return fmt.Errorf("cannot modify/remove from nil map")
		}
	}
	for k, oldVal := range p.removed {
		keyVal := convertValue(reflect.ValueOf(k), v.Type().Key())
		val := v.MapIndex(keyVal)
		if !val.IsValid() {
			return fmt.Errorf("key %v not found for removal", k)
		}
		if strict && !reflect.DeepEqual(val.Interface(), oldVal.Interface()) {
			return fmt.Errorf("map removal mismatch for key %v: expected %v, got %v", k, oldVal, val)
		}
	}
	for k, patch := range p.modified {
		keyVal := convertValue(reflect.ValueOf(k), v.Type().Key())
		val := v.MapIndex(keyVal)
		if !val.IsValid() {
			return fmt.Errorf("key %v not found for modification", k)
		}
		newElem := reflect.New(val.Type()).Elem()
		newElem.Set(val)
		if err := patch.applyChecked(root, newElem, strict); err != nil {
			return fmt.Errorf("key %v: %w", k, err)
		}
		v.SetMapIndex(keyVal, newElem)
	}
	for k := range p.removed {
		v.SetMapIndex(convertValue(reflect.ValueOf(k), v.Type().Key()), reflect.Value{})
	}
	for k, val := range p.added {
		keyVal := convertValue(reflect.ValueOf(k), v.Type().Key())
		curr := v.MapIndex(keyVal)
		if strict && curr.IsValid() {
			return fmt.Errorf("key %v already exists", k)
		}
		v.SetMapIndex(keyVal, convertValue(val, v.Type().Elem()))
	}
	return nil
}

func (p *mapPatch) reverse() diffPatch {
	newModified := make(map[interface{}]diffPatch)
	for k, v := range p.modified {
		newModified[k] = v.reverse()
	}
	return &mapPatch{
		patchMetadata: p.patchMetadata,
		added:         p.removed,
		removed:       p.added,
		modified:      newModified,
		keyType:       p.keyType,
	}
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
		fullPath := fmt.Sprintf("%s/%v", path, k)
		op := map[string]any{"op": "remove", "path": fullPath}
		addConditionsToOp(op, p)
		ops = append(ops, op)
	}
	for k, patch := range p.modified {
		fullPath := fmt.Sprintf("%s/%v", path, k)
		subOps := patch.toJSONPatch(fullPath)
		for _, op := range subOps {
			addConditionsToOp(op, p)
		}
		ops = append(ops, subOps...)
	}
	for k, val := range p.added {
		fullPath := fmt.Sprintf("%s/%v", path, k)
		op := map[string]any{"op": "add", "path": fullPath, "value": valueToInterface(val)}
		addConditionsToOp(op, p)
		ops = append(ops, op)
	}
	return ops
}

type opKind int

const (
	opAdd opKind = iota
	opDel
	opMod
)

type sliceOp struct {
	Kind  opKind
	Index int
	Val   reflect.Value
	Patch diffPatch
}

// slicePatch handles complex edits (insertions, deletions, modifications) in a slice.
type slicePatch struct {
	patchMetadata
	ops []sliceOp
}

func (p *slicePatch) apply(root, v reflect.Value) {
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
		case opAdd:
			newSlice = reflect.Append(newSlice, convertValue(op.Val, v.Type().Elem()))
		case opDel:
			curIdx++
		case opMod:
			if curIdx < v.Len() {
				elem := reflect.New(v.Type().Elem()).Elem()
				elem.Set(deepCopyValue(v.Index(curIdx)))
				if op.Patch != nil {
					op.Patch.apply(root, elem)
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

func (p *slicePatch) applyChecked(root, v reflect.Value, strict bool) error {
	if err := checkConditions(p, root, v); err != nil {
		if err == ErrConditionSkipped {
			return nil
		}
		return err
	}
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
		case opAdd:
			newSlice = reflect.Append(newSlice, convertValue(op.Val, v.Type().Elem()))
		case opDel:
			if curIdx >= v.Len() {
				return fmt.Errorf("slice deletion index %d out of bounds", curIdx)
			}
			curr := v.Index(curIdx)
			if strict && op.Val.IsValid() {
				convertedVal := convertValue(op.Val, v.Type().Elem())
				if !reflect.DeepEqual(curr.Interface(), convertedVal.Interface()) {
					return fmt.Errorf("slice deletion mismatch at %d: expected %v, got %v", curIdx, convertedVal, curr)
				}
			}
			curIdx++
		case opMod:
			if curIdx >= v.Len() {
				return fmt.Errorf("slice modification index %d out of bounds", curIdx)
			}
			elem := reflect.New(v.Type().Elem()).Elem()
			elem.Set(deepCopyValue(v.Index(curIdx)))
			if err := op.Patch.applyChecked(root, elem, strict); err != nil {
				return fmt.Errorf("slice index %d: %w", curIdx, err)
			}
			newSlice = reflect.Append(newSlice, elem)
			curIdx++
		}
	}
	for k := curIdx; k < v.Len(); k++ {
		newSlice = reflect.Append(newSlice, v.Index(k))
	}
	v.Set(newSlice)
	return nil
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
		case opAdd:
			revOps = append(revOps, sliceOp{
				Kind:  opDel,
				Index: curB,
				Val:   op.Val,
			})
			curB++
		case opDel:
			revOps = append(revOps, sliceOp{
				Kind:  opAdd,
				Index: curB,
				Val:   op.Val,
			})
			curA++
		case opMod:
			revOps = append(revOps, sliceOp{
				Kind:  opMod,
				Index: curB,
				Patch: op.Patch.reverse(),
			})
			curA++
			curB++
		}
	}
	return &slicePatch{
		patchMetadata: p.patchMetadata,
		ops:           revOps,
	}
}

func (p *slicePatch) format(indent int) string {
	var b strings.Builder
	b.WriteString("Slice{\n")
	prefix := strings.Repeat("  ", indent+1)
	for _, op := range p.ops {
		switch op.Kind {
		case opAdd:
			b.WriteString(fmt.Sprintf("%s+ [%d]: %v\n", prefix, op.Index, op.Val))
		case opDel:
			b.WriteString(fmt.Sprintf("%s- [%d]\n", prefix, op.Index))
		case opMod:
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
		case opAdd:
			jsonOp := map[string]any{"op": "add", "path": fullPath, "value": valueToInterface(op.Val)}
			addConditionsToOp(jsonOp, p)
			ops = append(ops, jsonOp)
			shift++
		case opDel:
			jsonOp := map[string]any{"op": "remove", "path": fullPath}
			addConditionsToOp(jsonOp, p)
			ops = append(ops, jsonOp)
			shift--
		case opMod:
			subOps := op.Patch.toJSONPatch(fullPath)
			for _, sop := range subOps {
				addConditionsToOp(sop, p)
			}
			ops = append(ops, subOps...)
		}
	}
	return ops
}

func addConditionsToOp(op map[string]any, p diffPatch) {
	_, ifC, unlessC := p.conditions()
	if ifC != nil {
		op["if"] = conditionToPredicate(ifC)
	}
	if unlessC != nil {
		op["unless"] = conditionToPredicate(unlessC)
	}
}

func conditionToPredicate(c any) any {
	if c == nil {
		return nil
	}

	v := reflect.ValueOf(c)
	for v.Kind() == reflect.Ptr || v.Kind() == reflect.Interface {
		v = v.Elem()
	}

	typeName := v.Type().Name()
	if strings.HasPrefix(typeName, "typedRawCondition") || strings.HasPrefix(typeName, "typedCondition") {
		raw := v.FieldByName("raw")
		unsafe.DisableRO(&raw)
		return conditionToPredicate(raw.Interface())
	}

	if strings.HasPrefix(typeName, "rawCompareCondition") || strings.HasPrefix(typeName, "CompareCondition") {
		path := v.FieldByName("Path").String()
		val := v.FieldByName("Val").Interface()
		op := v.FieldByName("Op").String()
		ignoreCase := v.FieldByName("IgnoreCase").Bool()

		switch op {
		case "==":
			jsonOp := "test"
			if ignoreCase {
				jsonOp = "test-"
			}
			return map[string]any{"op": jsonOp, "path": path, "value": val}
		case "!=":
			jsonOp := "test"
			if ignoreCase {
				jsonOp = "test-"
			}
			return map[string]any{"op": "not", "apply": []any{map[string]any{"op": jsonOp, "path": path, "value": val}}}
		case "<":
			return map[string]any{"op": "less", "path": path, "value": val}
		case ">":
			return map[string]any{"op": "more", "path": path, "value": val}
		case "<=":
			return map[string]any{"op": "or", "apply": []any{
				map[string]any{"op": "less", "path": path, "value": val},
				map[string]any{"op": "test", "path": path, "value": val},
			}}
		case ">=":
			return map[string]any{"op": "or", "apply": []any{
				map[string]any{"op": "more", "path": path, "value": val},
				map[string]any{"op": "test", "path": path, "value": val},
			}}
		}
	}

	if strings.HasPrefix(typeName, "DefinedCondition") {
		path := v.FieldByName("Path").String()
		return map[string]any{"op": "defined", "path": path}
	}

	if strings.HasPrefix(typeName, "UndefinedCondition") {
		path := v.FieldByName("Path").String()
		return map[string]any{"op": "undefined", "path": path}
	}

	if strings.HasPrefix(typeName, "TypeCondition") {
		path := v.FieldByName("Path").String()
		typeName := v.FieldByName("TypeName").String()
		return map[string]any{"op": "type", "path": path, "value": typeName}
	}

	if strings.HasPrefix(typeName, "StringCondition") {
		path := v.FieldByName("Path").String()
		val := v.FieldByName("Val").String()
		op := v.FieldByName("Op").String()
		ignoreCase := v.FieldByName("IgnoreCase").Bool()

		if ignoreCase && op != "matches" {
			op += "-"
		}
		if op == "matches" && ignoreCase {
			return map[string]any{"op": op, "path": path, "value": val, "ignoreCase": true}
		}
		return map[string]any{"op": op, "path": path, "value": val}
	}

	if strings.HasPrefix(typeName, "InCondition") {
		path := v.FieldByName("Path").String()
		vals := v.FieldByName("Values").Interface()
		ignoreCase := v.FieldByName("IgnoreCase").Bool()

		op := "in"
		if ignoreCase {
			op = "in-"
		}
		return map[string]any{"op": op, "path": path, "value": vals}
	}

	if strings.HasPrefix(typeName, "LogCondition") {
		msg := v.FieldByName("Message").String()
		return map[string]any{"op": "log", "value": msg}
	}

	if strings.HasPrefix(typeName, "AndCondition") {
		condsVal := v.FieldByName("Conditions")
		apply := make([]any, 0, condsVal.Len())
		for i := 0; i < condsVal.Len(); i++ {
			apply = append(apply, conditionToPredicate(condsVal.Index(i).Interface()))
		}
		return map[string]any{"op": "and", "apply": apply}
	}

	if strings.HasPrefix(typeName, "OrCondition") {
		condsVal := v.FieldByName("Conditions")
		apply := make([]any, 0, condsVal.Len())
		for i := 0; i < condsVal.Len(); i++ {
			apply = append(apply, conditionToPredicate(condsVal.Index(i).Interface()))
		}
		return map[string]any{"op": "or", "apply": apply}
	}

	if strings.HasPrefix(typeName, "NotCondition") {
		sub := conditionToPredicate(v.FieldByName("C").Interface())
		return map[string]any{"op": "not", "apply": []any{sub}}
	}

	return nil
}
