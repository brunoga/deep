package deep

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/brunoga/deep/v2/internal/unsafe"
)

var ErrConditionSkipped = fmt.Errorf("condition skipped")

// diffPatch is the internal recursive interface for all patch types.
type diffPatch interface {
	apply(root, v reflect.Value)
	applyChecked(root, v reflect.Value, strict bool) error
	applyResolved(root, v reflect.Value, path string, resolver ConflictResolver) error
	reverse() diffPatch
	format(indent int) string
	walk(path string, fn func(path string, op OpKind, old, new any) error) error
	setCondition(cond any)
	setIfCondition(cond any)
	setUnlessCondition(cond any)
	conditions() (cond, ifCond, unlessCond any)
	toJSONPatch(path string) []map[string]any
	summary(path string) string
}

type basePatch struct {
	cond any

	ifCond any

	unlessCond any
}

func (p *basePatch) setCondition(cond any) { p.cond = cond }

func (p *basePatch) setIfCondition(cond any) { p.ifCond = cond }

func (p *basePatch) setUnlessCondition(cond any) { p.unlessCond = cond }

func (p *basePatch) conditions() (any, any, any) { return p.cond, p.ifCond, p.unlessCond }

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

	// For custom conditions that implement Evaluate(T) bool, they should ideally
	// wrap themselves in a way that provides evaluateAny or we should have a 
	// common interface for them.
	// Since we are doing a breaking change, we require them to implement internalCondition 
	// or we can use reflection more selectively if we really want to support custom types.
	// But let's stick to the interface for now.
	
	if gc, ok := cond.(interface {
		Evaluate(any) (bool, error)
	}); ok {
		return gc.Evaluate(v.Interface())
	}

	return false, fmt.Errorf("local condition: %T does not implement required interface", cond)
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
	basePatch
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

func (p *valuePatch) applyResolved(root, v reflect.Value, path string, resolver ConflictResolver) error {
	if resolver != nil {
		if !resolver.Resolve(path, OpReplace, nil, nil, v) {
			return nil // Skipped by resolver
		}
	}
	p.apply(root, v)
	return nil
}

func (p *valuePatch) reverse() diffPatch {
	return &valuePatch{oldVal: p.newVal, newVal: p.oldVal, basePatch: p.basePatch}
}

func (p *valuePatch) walk(path string, fn func(path string, op OpKind, old, new any) error) error {
	op := OpReplace
	if !p.newVal.IsValid() {
		op = OpRemove
	} else if !p.oldVal.IsValid() {
		op = OpAdd
	}
	return fn(path, op, valueToInterface(p.oldVal), valueToInterface(p.newVal))
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

func (p *valuePatch) summary(path string) string {
	if !p.newVal.IsValid() {
		return fmt.Sprintf("Removed %s (was %v)", path, valueToInterface(p.oldVal))
	}
	if !p.oldVal.IsValid() {
		return fmt.Sprintf("Added %s: %v", path, valueToInterface(p.newVal))
	}
	return fmt.Sprintf("Updated %s from %v to %v", path, valueToInterface(p.oldVal), valueToInterface(p.newVal))
}

// testPatch handles equality checks without modifying the value.
type testPatch struct {
	basePatch
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

func (p *testPatch) applyResolved(root, v reflect.Value, path string, resolver ConflictResolver) error {
	return p.applyChecked(root, v, true)
}

func (p *testPatch) reverse() diffPatch {
	return p // Reversing a test is still a test
}

func (p *testPatch) walk(path string, fn func(path string, op OpKind, old, new any) error) error {
	return fn(path, OpTest, nil, valueToInterface(p.expected))
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

func (p *testPatch) summary(path string) string {
	return fmt.Sprintf("Tested %s == %v", path, valueToInterface(p.expected))
}

// copyPatch copies a value from another path.
type copyPatch struct {
	basePatch
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
	if rvRoot.Kind() == reflect.Pointer {
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

func (p *copyPatch) applyResolved(root, v reflect.Value, path string, resolver ConflictResolver) error {
	// For now, treat copy as a simple value change or delegate?
	// Resolving copy operations is tricky because they depend on source.
	// We'll treat it as a replace-like op.
	if resolver != nil {
		if !resolver.Resolve(path, OpCopy, nil, nil, v) {
			return nil
		}
	}
	return p.applyChecked(root, v, false)
}

func (p *copyPatch) reverse() diffPatch {
	// Reversing a copy is a removal of the target.
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
	addConditionsToOp(op, p)
	return []map[string]any{op}
}

func (p *copyPatch) summary(path string) string {
	return fmt.Sprintf("Copied %s to %s", p.from, path)
}

// movePatch moves a value from another path.
type movePatch struct {
	basePatch
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
	if rvRoot.Kind() != reflect.Pointer {
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

func (p *movePatch) applyResolved(root, v reflect.Value, path string, resolver ConflictResolver) error {
	if resolver != nil {
		if !resolver.Resolve(path, OpMove, nil, nil, v) {
			return nil
		}
	}
	return p.applyChecked(root, v, false)
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
	addConditionsToOp(op, p)
	return []map[string]any{op}
}

func (p *movePatch) summary(path string) string {
	return fmt.Sprintf("Moved %s to %s", p.from, path)
}

// logPatch logs a message without modifying the value.
type logPatch struct {
	basePatch
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

func (p *logPatch) applyResolved(root, v reflect.Value, path string, resolver ConflictResolver) error {
	return p.applyChecked(root, v, false)
}

func (p *logPatch) reverse() diffPatch {
	return p // Reversing a log is still a log
}

func (p *logPatch) walk(path string, fn func(path string, op OpKind, old, new any) error) error {
	return fn(path, OpLog, nil, p.message)
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

func (p *logPatch) summary(path string) string {
	return fmt.Sprintf("Log: %s", p.message)
}

// ptrPatch handles changes to the content pointed to by a pointer.

type ptrPatch struct {
	basePatch
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

func (p *ptrPatch) applyResolved(root, v reflect.Value, path string, resolver ConflictResolver) error {
	if v.IsNil() {
		return fmt.Errorf("cannot apply pointer patch to nil value")
	}
	return p.elemPatch.applyResolved(root, v.Elem(), path, resolver)
}

func (p *ptrPatch) reverse() diffPatch {
	return &ptrPatch{
		basePatch: p.basePatch,
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
	ops := p.elemPatch.toJSONPatch(path)
	for _, op := range ops {
		addConditionsToOp(op, p)
	}
	return ops
}

func (p *ptrPatch) summary(path string) string {
	return p.elemPatch.summary(path)
}

// interfacePatch handles changes to the value stored in an interface.
type interfacePatch struct {
	basePatch
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

func (p *interfacePatch) reverse() diffPatch {
	return &interfacePatch{
		basePatch: p.basePatch,
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
	ops := p.elemPatch.toJSONPatch(path)
	for _, op := range ops {
		addConditionsToOp(op, p)
	}
	return ops
}

func (p *interfacePatch) summary(path string) string {
	return p.elemPatch.summary(path)
}

// structPatch handles field-level modifications in a struct.
type structPatch struct {
	basePatch
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
	var errs []error
	for name, patch := range p.fields {
		f := v.FieldByName(name)
		if !f.IsValid() {
			errs = append(errs, fmt.Errorf("field %s not found", name))
			continue
		}
		if !f.CanSet() {
			unsafe.DisableRO(&f)
		}
		if err := patch.applyChecked(root, f, strict); err != nil {
			errs = append(errs, fmt.Errorf("field %s: %w", name, err))
		}
	}
	if len(errs) > 0 {
		return &ApplyError{Errors: errs}
	}
	return nil
}

func (p *structPatch) applyResolved(root, v reflect.Value, path string, resolver ConflictResolver) error {
	for name, patch := range p.fields {
		f := v.FieldByName(name)
		if !f.IsValid() {
			return fmt.Errorf("field %s not found", name)
		}
		if !f.CanSet() {
			unsafe.DisableRO(&f)
		}

		subPath := path
		if !strings.HasSuffix(subPath, "/") {
			subPath += "/"
		}
		subPath += name

		if err := patch.applyResolved(root, f, subPath, resolver); err != nil {
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
		basePatch: p.basePatch,
		fields:    newFields,
	}
}

func (p *structPatch) walk(path string, fn func(path string, op OpKind, old, new any) error) error {
	for name, patch := range p.fields {
		fullPath := path + "/" + name
		if err := patch.walk(fullPath, fn); err != nil {
			return err
		}
	}
	return nil
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

func (p *structPatch) summary(path string) string {
	var summaries []string
	for name, patch := range p.fields {
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
	basePatch
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
		if err := patch.applyChecked(root, e, strict); err != nil {
			errs = append(errs, fmt.Errorf("index %d: %w", i, err))
		}
	}
	if len(errs) > 0 {
		return &ApplyError{Errors: errs}
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

		subPath := path
		if !strings.HasSuffix(subPath, "/") {
			subPath += "/"
		}
		subPath += strconv.Itoa(i)

		if err := patch.applyResolved(root, e, subPath, resolver); err != nil {
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
		basePatch: p.basePatch,
		indices:   newIndices,
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
		for _, op := range subOps {
			addConditionsToOp(op, p)
		}
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
	basePatch
	added        map[any]reflect.Value
	removed      map[any]reflect.Value
	modified     map[any]diffPatch
	originalKeys map[any]any // Canonical -> Original
	keyType      reflect.Type
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
		v.SetMapIndex(p.getOriginalKey(k, v.Type().Key(), v), reflect.Value{})
	}
	for k, patch := range p.modified {
		keyVal := p.getOriginalKey(k, v.Type().Key(), v)
		if cp, ok := patch.(*copyPatch); ok {
			rvRoot := root
			if rvRoot.Kind() == reflect.Pointer {
				rvRoot = rvRoot.Elem()
			}
			fromVal, err := Path(cp.from).resolve(rvRoot)
			if err == nil {
				v.SetMapIndex(keyVal, convertValue(fromVal, v.Type().Elem()))
			}
			continue
		}
		elem := v.MapIndex(keyVal)
		if elem.IsValid() {
			newElem := reflect.New(elem.Type()).Elem()
			newElem.Set(elem)
			patch.apply(root, newElem)
			v.SetMapIndex(keyVal, newElem)
		}
	}
	for k, val := range p.added {
		keyVal := p.getOriginalKey(k, v.Type().Key(), v)
		v.SetMapIndex(keyVal, convertValue(val, v.Type().Elem()))
	}
}

func (p *mapPatch) getOriginalKey(k any, targetType reflect.Type, v reflect.Value) reflect.Value {
	if orig, ok := p.originalKeys[k]; ok {
		return convertValue(reflect.ValueOf(orig), targetType)
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

	return convertValue(reflect.ValueOf(k), targetType)
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
	var errs []error
	for k, oldVal := range p.removed {
		keyVal := p.getOriginalKey(k, v.Type().Key(), v)
		val := v.MapIndex(keyVal)
		if !val.IsValid() {
			errs = append(errs, fmt.Errorf("key %v not found for removal", k))
			continue
		}
		if strict && !reflect.DeepEqual(val.Interface(), oldVal.Interface()) {
			errs = append(errs, fmt.Errorf("map removal mismatch for key %v: expected %v, got %v", k, oldVal, val))
		}
	}
	for k, patch := range p.modified {
		keyVal := p.getOriginalKey(k, v.Type().Key(), v)
		if cp, ok := patch.(*copyPatch); ok {
			rvRoot := root
			if rvRoot.Kind() == reflect.Pointer {
				rvRoot = rvRoot.Elem()
			}
			fromVal, err := Path(cp.from).resolve(rvRoot)
			if err != nil {
				errs = append(errs, fmt.Errorf("map copy from %s failed: %w", cp.from, err))
			} else {
				v.SetMapIndex(keyVal, convertValue(fromVal, v.Type().Elem()))
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
		if err := patch.applyChecked(root, newElem, strict); err != nil {
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
		v.SetMapIndex(keyVal, convertValue(val, v.Type().Elem()))
	}
	if len(errs) > 0 {
		return &ApplyError{Errors: errs}
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
	for k, _ := range p.removed {
		subPath := path
		if !strings.HasSuffix(subPath, "/") {
			subPath += "/"
		}
		subPath += fmt.Sprintf("%v", k)

		if resolver != nil {
			if !resolver.Resolve(subPath, OpRemove, k, nil, reflect.Value{}) {
				continue
			}
		}
		v.SetMapIndex(p.getOriginalKey(k, v.Type().Key(), v), reflect.Value{})
	}

	// Modifications
	for k, patch := range p.modified {
		keyVal := p.getOriginalKey(k, v.Type().Key(), v)
		val := v.MapIndex(keyVal)
		if !val.IsValid() {
			continue // Or error? Let's skip if missing, concurrent delete handling.
		}

		subPath := path
		if !strings.HasSuffix(subPath, "/") {
			subPath += "/"
		}
		subPath += fmt.Sprintf("%v", k)

		newElem := reflect.New(val.Type()).Elem()
		newElem.Set(val)
		if err := patch.applyResolved(root, newElem, subPath, resolver); err != nil {
			return fmt.Errorf("key %v: %w", k, err)
		}
		v.SetMapIndex(keyVal, newElem)
	}

	// Additions
	for k, val := range p.added {
		subPath := path
		if !strings.HasSuffix(subPath, "/") {
			subPath += "/"
		}
		subPath += fmt.Sprintf("%v", k)

		if resolver != nil {
			if !resolver.Resolve(subPath, OpAdd, k, nil, val) {
				continue
			}
		}
		v.SetMapIndex(p.getOriginalKey(k, v.Type().Key(), v), convertValue(val, v.Type().Elem()))
	}
	return nil
}

func (p *mapPatch) reverse() diffPatch {
	newModified := make(map[any]diffPatch)
	for k, v := range p.modified {
		newModified[k] = v.reverse()
	}
	return &mapPatch{
		basePatch: p.basePatch,
		added:     p.removed,
		removed:   p.added,
		modified:  newModified,
		keyType:   p.keyType,
	}
}

func (p *mapPatch) walk(path string, fn func(path string, op OpKind, old, new any) error) error {
	for k, val := range p.added {
		fullPath := fmt.Sprintf("%s/%v", path, k)
		if err := fn(fullPath, OpAdd, nil, valueToInterface(val)); err != nil {
			return err
		}
	}
	for k, oldVal := range p.removed {
		fullPath := fmt.Sprintf("%s/%v", path, k)
		if err := fn(fullPath, OpRemove, valueToInterface(oldVal), nil); err != nil {
			return err
		}
	}
	for k, patch := range p.modified {
		fullPath := fmt.Sprintf("%s/%v", path, k)
		if err := patch.walk(fullPath, fn); err != nil {
			return err
		}
	}
	return nil
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

func (p *mapPatch) summary(path string) string {
	var summaries []string
	prefix := path
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	for k, val := range p.added {
		subPath := prefix + fmt.Sprintf("%v", k)
		summaries = append(summaries, fmt.Sprintf("Added %s: %v", subPath, valueToInterface(val)))
	}
	for k, oldVal := range p.removed {
		subPath := prefix + fmt.Sprintf("%v", k)
		summaries = append(summaries, fmt.Sprintf("Removed %s (was %v)", subPath, valueToInterface(oldVal)))
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
	// It returns true if the operation should be applied, false to skip it.
	// The resolver can also modify the operation or target value directly.
	Resolve(path string, op OpKind, key, prevKey any, val reflect.Value) bool
}

// slicePatch handles complex edits (insertions, deletions, modifications) in a slice.
type slicePatch struct {
	basePatch
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
		case OpAdd:
			newSlice = reflect.Append(newSlice, convertValue(op.Val, v.Type().Elem()))
		case OpRemove:
			curIdx++
		case OpCopy, OpMove:
			// Resolve source from root
			if cp, ok := op.Patch.(*copyPatch); ok {
				rvRoot := root
				if rvRoot.Kind() == reflect.Pointer {
					rvRoot = rvRoot.Elem()
				}
				fromVal, err := Path(cp.from).resolve(rvRoot)
				if err == nil {
					newSlice = reflect.Append(newSlice, convertValue(fromVal, v.Type().Elem()))
				}
			}
			curIdx++
		case OpReplace:
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
			newSlice = reflect.Append(newSlice, convertValue(op.Val, v.Type().Elem()))
		case OpRemove:
			if curIdx >= v.Len() {
				errs = append(errs, fmt.Errorf("slice deletion index %d out of bounds", curIdx))
				continue
			}
			curr := v.Index(curIdx)
			if strict && op.Val.IsValid() {
				convertedVal := convertValue(op.Val, v.Type().Elem())
				if !reflect.DeepEqual(curr.Interface(), convertedVal.Interface()) {
					errs = append(errs, fmt.Errorf("slice deletion mismatch at %d: expected %v, got %v", curIdx, convertedVal, curr))
				}
			}
			curIdx++
		case OpCopy, OpMove:
			// Resolve source from root
			if cp, ok := op.Patch.(*copyPatch); ok {
				rvRoot := root
				if rvRoot.Kind() == reflect.Pointer {
					rvRoot = rvRoot.Elem()
				}
				fromVal, err := Path(cp.from).resolve(rvRoot)
				if err != nil {
					errs = append(errs, fmt.Errorf("slice copy from %s failed: %w", cp.from, err))
				} else {
					newSlice = reflect.Append(newSlice, convertValue(fromVal, v.Type().Elem()))
				}
			}
			curIdx++
		case OpReplace:
			if curIdx >= v.Len() {
				errs = append(errs, fmt.Errorf("slice modification index %d out of bounds", curIdx))
				continue
			}
			elem := reflect.New(v.Type().Elem()).Elem()
			elem.Set(deepCopyValue(v.Index(curIdx)))
			if op.Patch != nil {
				if err := op.Patch.applyChecked(root, elem, strict); err != nil {
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
		return &ApplyError{Errors: errs}
	}
	return nil
}

func (p *slicePatch) applyResolved(root, v reflect.Value, path string, resolver ConflictResolver) error {
	// If no resolver, fallback to standard checked apply (flexible)
	if resolver == nil {
		return p.applyChecked(root, v, false)
	}

	// Semantic application for keyed slices
	keyField, hasKey := getKeyField(v.Type().Elem())

	// We'll build a new slice by copying the current state and applying ops
	// But simply appending ops won't work because indices shift.
	// We need to apply ops "in place" into v, handling shifts dynamically.
	// Or better: construct a new slice from scratch? No, that's hard if we only have ops.

	// Better strategy:
	// Convert v to a list of elements.
	// Apply deletions first (marking as deleted).
	// Apply insertions/replacements based on keys.

	// BUT, `sliceOp` comes from a specific diff.
	// If the slice is NOT keyed, we just use indices but check the resolver.

	if !hasKey {
		// Non-keyed slice: treat as atomic updates by index, but respect resolver
		// This is tricky because insertions shift indices.
		// A robust way is to re-calculate indices or just fail for concurrent edits on non-keyed slices.
		// For now, let's implement standard indexed application but call Resolve.

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

			subPath := path
			if !strings.HasSuffix(subPath, "/") {
				subPath += "/"
			}
			subPath += strconv.Itoa(curIdx)

			switch op.Kind {
			case OpAdd:
				if resolver.Resolve(subPath, OpAdd, nil, nil, op.Val) {
					newSlice = reflect.Append(newSlice, convertValue(op.Val, v.Type().Elem()))
				}
			case OpRemove:
				if curIdx < v.Len() {
					if resolver.Resolve(subPath, OpRemove, nil, nil, reflect.Value{}) {
						curIdx++ // Skip (remove)
					} else {
						// Keep
						newSlice = reflect.Append(newSlice, v.Index(curIdx))
						curIdx++
					}
				}
			case OpReplace:
				if curIdx < v.Len() {
					elem := reflect.New(v.Type().Elem()).Elem()
					elem.Set(deepCopyValue(v.Index(curIdx)))
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

	// Keyed Slice Logic
	// 1. Map existing elements by Key
	type elemInfo struct {
		val   reflect.Value
		index int
	}
	existingMap := make(map[any]*elemInfo)
	var orderedKeys []any

	for i := 0; i < v.Len(); i++ {
		val := v.Index(i)
		k := extractKey(val, keyField)
		existingMap[k] = &elemInfo{val: val, index: i}
		orderedKeys = append(orderedKeys, k)
	}

	// 2. Process Ops
	// We need to handle concurrent edits.
	// Deletions: easy, remove from map.
	// Modifications: easy, update value in map.
	// Insertions: tricky. We need to insert relative to PrevKey.

	// We will build the new ordered list of keys.

	// First, applying Replacements and Removals (tombstoning)
	for _, op := range p.ops {
		subPath := path
		if !strings.HasSuffix(subPath, "/") {
			subPath += "/"
		}
		subPath += fmt.Sprintf("%v", op.Key)

		switch op.Kind {
		case OpRemove:
			if resolver.Resolve(subPath, OpRemove, op.Key, nil, reflect.Value{}) {
				delete(existingMap, op.Key)
				// Remove from orderedKeys? We'll reconstruct later.
			}
		case OpReplace:
			if info, ok := existingMap[op.Key]; ok {
				newVal := reflect.New(v.Type().Elem()).Elem()
				newVal.Set(deepCopyValue(info.val))
				if err := op.Patch.applyResolved(root, newVal, subPath, resolver); err == nil {
					info.val = newVal
				}
			}
		}
	}

	// Now Additions. This is where Yjs logic comes in.
	// We need to insert op.Val after op.PrevKey.

	// To support efficient insertion, let's use a linked list or just insert into slice.
	// Since slices are small, inserting into a slice of keys is fine.

	for _, op := range p.ops {
		if op.Kind == OpAdd {
			subPath := path
			if !strings.HasSuffix(subPath, "/") {
				subPath += "/"
			}
			subPath += fmt.Sprintf("%v", op.Key)
			if resolver.Resolve(subPath, OpAdd, op.Key, op.PrevKey, op.Val) {
				// Insert into orderedKeys
				// Find PrevKey index
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
					// If PrevKey not found (deleted?), what do we do?
					// Yjs logic: find the nearest predecessor that still exists.
					// For simplicity: if prev not found, insert at beginning (or end?).
					// Let's default to beginning if PrevKey was specified but missing,
					// or maybe we should keep tombstone keys?
					// CRDTs usually keep tombstones. We don't have them here explicitly.
					// Let's assume strict predecessor for now: if prev missing, try index 0.
					if !foundPrev {
						insertIdx = 0
					}
				}

				// Conflict Resolution: Scan forward to handle concurrent insertions
				// We compare op.Key with existing keys at insertIdx to ensure deterministic order.
				for insertIdx < len(orderedKeys) {
					// We assume elements starting at insertIdx are either:
					// 1. Concurrent siblings (inserted after same PrevKey)
					// 2. Successors of PrevKey (conceptually)
					// By sorting them, we ensure convergence.

					// Compare keys as strings for stability
					k1 := fmt.Sprintf("%v", op.Key)
					k2 := fmt.Sprintf("%v", orderedKeys[insertIdx])

					if k1 > k2 {
						insertIdx++
					} else {
						break
					}
				}

				// Insert at insertIdx
				if insertIdx >= len(orderedKeys) {
					orderedKeys = append(orderedKeys, op.Key)
				} else {
					orderedKeys = append(orderedKeys, nil)
					copy(orderedKeys[insertIdx+1:], orderedKeys[insertIdx:])
					orderedKeys[insertIdx] = op.Key
				}
				// Add to map
				existingMap[op.Key] = &elemInfo{val: convertValue(op.Val, v.Type().Elem())}

			}
		}
	}

	// Reconstruct Slice
	// Filter orderedKeys to only those in existingMap
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

func extractKey(v reflect.Value, fieldIdx int) any {
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return nil
	}
	return v.Field(fieldIdx).Interface()
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
			})
			curB++
		case OpRemove:
			revOps = append(revOps, sliceOp{
				Kind:  OpAdd,
				Index: curB,
				Val:   op.Val,
			})
			curA++
		case OpReplace:
			revOps = append(revOps, sliceOp{
				Kind:  OpReplace,
				Index: curB,
				Patch: op.Patch.reverse(),
			})
			curA++
			curB++
		}
	}
	return &slicePatch{
		basePatch: p.basePatch,
		ops:       revOps,
	}
}

func (p *slicePatch) walk(path string, fn func(path string, op OpKind, old, new any) error) error {
	for _, op := range p.ops {
		fullPath := fmt.Sprintf("%s/%d", path, op.Index)
		if op.Key != nil {
			fullPath = fmt.Sprintf("%s/%v", path, op.Key)
		}
		switch op.Kind {
		case OpAdd:
			if err := fn(fullPath, OpAdd, nil, valueToInterface(op.Val)); err != nil {
				return err
			}
		case OpRemove:
			if err := fn(fullPath, OpRemove, valueToInterface(op.Val), nil); err != nil {
				return err
			}
		case OpReplace:
			if op.Patch != nil {
				if err := op.Patch.walk(fullPath, fn); err != nil {
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
			jsonOp := map[string]any{"op": "add", "path": fullPath, "value": valueToInterface(op.Val)}
			addConditionsToOp(jsonOp, p)
			ops = append(ops, jsonOp)
			shift++
		case OpRemove:
			jsonOp := map[string]any{"op": "remove", "path": fullPath}
			addConditionsToOp(jsonOp, p)
			ops = append(ops, jsonOp)
			shift--
		case OpReplace:
			subOps := op.Patch.toJSONPatch(fullPath)
			for _, sop := range subOps {
				addConditionsToOp(sop, p)
			}
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
			summaries = append(summaries, fmt.Sprintf("Added to %s: %v", subPath, valueToInterface(op.Val)))
		case OpRemove:
			summaries = append(summaries, fmt.Sprintf("Removed from %s: %v", subPath, valueToInterface(op.Val)))
		case OpReplace:
			if op.Patch != nil {
				summaries = append(summaries, op.Patch.summary(subPath))
			}
		}
	}
	return strings.Join(summaries, "\n")
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
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
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

// customDiffPatch wraps an exported Patch[T] into the internal diffPatch interface.
type customDiffPatch struct {
	basePatch
	patch any // This is a Patch[T]
}

func (p *customDiffPatch) apply(root, v reflect.Value) {
	if !v.CanAddr() {
		return
	}
	method := reflect.ValueOf(p.patch).MethodByName("Apply")
	method.Call([]reflect.Value{v.Addr()})
}

func (p *customDiffPatch) applyChecked(root, v reflect.Value, strict bool) error {
	if err := checkConditions(p, root, v); err != nil {
		if err == ErrConditionSkipped {
			return nil
		}
		return err
	}
	if !v.CanAddr() {
		return fmt.Errorf("cannot apply custom patch to non-addressable value")
	}
	method := reflect.ValueOf(p.patch).MethodByName("ApplyChecked")
	results := method.Call([]reflect.Value{v.Addr()})
	if !results[0].IsNil() {
		return results[0].Interface().(error)
	}
	return nil
}

func (p *customDiffPatch) applyResolved(root, v reflect.Value, path string, resolver ConflictResolver) error {
	// Custom patches don't support resolution hooks yet.
	// They are opaque. We just check if the op itself is allowed?
	// But custom patch implies multiple changes.
	// Fallback to standard applyChecked or fail?
	// Let's call ApplyChecked.
	if resolver != nil {
		if !resolver.Resolve(path, OpReplace, nil, nil, v) {
			return nil
		}
	}
	return p.applyChecked(root, v, false)
}

func (p *customDiffPatch) reverse() diffPatch {
	method := reflect.ValueOf(p.patch).MethodByName("Reverse")
	res := method.Call(nil)
	return &customDiffPatch{
		basePatch: p.basePatch,
		patch:     res[0].Interface(),
	}
}

func (p *customDiffPatch) walk(path string, fn func(path string, op OpKind, old, new any) error) error {
	method := reflect.ValueOf(p.patch).MethodByName("Walk")
	if !method.IsValid() {
		return fmt.Errorf("custom patch does not support Walk")
	}
	// We need to wrap the user callback to handle the path correctly.
	wrappedFn := func(subPath string, op OpKind, old, new any) error {
		fullPath := path + subPath
		return fn(fullPath, op, old, new)
	}
	results := method.Call([]reflect.Value{reflect.ValueOf(wrappedFn)})
	if !results[0].IsNil() {
		return results[0].Interface().(error)
	}
	return nil
}

func (p *customDiffPatch) format(indent int) string {
	method := reflect.ValueOf(p.patch).MethodByName("String")
	res := method.Call(nil)
	return res[0].String()
}

func (p *customDiffPatch) toJSONPatch(path string) []map[string]any {
	method := reflect.ValueOf(p.patch).MethodByName("ToJSONPatch")
	res := method.Call(nil)
	if !res[1].IsNil() {
		return nil
	}
	data := res[0].Bytes()
	var ops []map[string]any
	if err := json.Unmarshal(data, &ops); err != nil {
		return nil
	}

	if path == "" || path == "/" {
		return ops
	}

	// Prepend path to each op's path.
	for i := range ops {
		pVal, ok := ops[i]["path"].(string)
		if !ok {
			continue
		}
		if pVal == "/" {
			ops[i]["path"] = path
		} else {
			ops[i]["path"] = path + pVal
		}
	}

	return ops
}

func (p *customDiffPatch) summary(path string) string {
	method := reflect.ValueOf(p.patch).MethodByName("Summary")
	if method.IsValid() {
		res := method.Call(nil)
		return res[0].String()
	}
	// Fallback to String() if Summary() is not available
	return p.format(0)
}

// readOnlyPatch wraps another patch and prevents it from being applied.
type readOnlyPatch struct {
	inner diffPatch
}

func (p *readOnlyPatch) apply(root, v reflect.Value) {
	// No-op
}

func (p *readOnlyPatch) applyChecked(root, v reflect.Value, strict bool) error {
	// No-op for actual modification, but we might want to check conditions?
	// For now, let's just make it a no-op as it's readonly.
	return nil
}

func (p *readOnlyPatch) applyResolved(root, v reflect.Value, path string, resolver ConflictResolver) error {
	return nil
}

func (p *readOnlyPatch) reverse() diffPatch {
	return &readOnlyPatch{inner: p.inner.reverse()}
}

func (p *readOnlyPatch) walk(path string, fn func(path string, op OpKind, old, new any) error) error {
	return p.inner.walk(path, fn)
}

func (p *readOnlyPatch) format(indent int) string {
	return fmt.Sprintf("ReadOnly(%s)", p.inner.format(indent))
}

func (p *readOnlyPatch) setCondition(cond any)       { p.inner.setCondition(cond) }
func (p *readOnlyPatch) setIfCondition(cond any)     { p.inner.setIfCondition(cond) }
func (p *readOnlyPatch) setUnlessCondition(cond any) { p.inner.setUnlessCondition(cond) }
func (p *readOnlyPatch) conditions() (any, any, any) { return p.inner.conditions() }

func (p *readOnlyPatch) toJSONPatch(path string) []map[string]any {
	// For JSON Patch, we don't really have a way to say "don't apply this but keep it".
	// Maybe we should just return empty?
	// Actually, if it's readonly, we probably shouldn't even include it in the JSON patch.
	return nil
}

func (p *readOnlyPatch) summary(path string) string {
	return fmt.Sprintf("[ReadOnly] %s", p.inner.summary(path))
}
