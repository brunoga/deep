package deep

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/brunoga/deep/internal/unsafe"
)

// Patch represents a set of changes that can be applied to a value of type T.
type Patch[T any] interface {
	fmt.Stringer

	// Apply applies the patch to the value pointed to by v.
	// The value v must not be nil.
	Apply(v *T)

	// ApplyChecked applies the patch only if specific conditions are met.
	// 1. If the patch has a global Condition, it must evaluate to true.
	// 2. For every modification, the target value must match the 'oldVal' recorded in the patch.
	ApplyChecked(v *T) error

	// WithCondition returns a new Patch with the given condition attached.
	WithCondition(c Condition[T]) Patch[T]

	// Reverse returns a new Patch that undoes the changes in this patch.
	Reverse() Patch[T]
}

type typedPatch[T any] struct {
	inner diffPatch
	cond  Condition[T]
}

func (p *typedPatch[T]) Apply(v *T) {
	if p.inner == nil {
		return
	}
	rv := reflect.ValueOf(v).Elem()
	p.inner.apply(rv)
}

func (p *typedPatch[T]) ApplyChecked(v *T) error {
	if p.cond != nil {
		ok, err := p.cond.Evaluate(v)
		if err != nil {
			return fmt.Errorf("condition evaluation failed: %w", err)
		}
		if !ok {
			return fmt.Errorf("condition failed")
		}
	}

	if p.inner == nil {
		return nil
	}

	rv := reflect.ValueOf(v).Elem()
	return p.inner.applyChecked(rv)
}

func (p *typedPatch[T]) WithCondition(c Condition[T]) Patch[T] {
	return &typedPatch[T]{
		inner: p.inner,
		cond:  c,
	}
}

func (p *typedPatch[T]) Reverse() Patch[T] {
	if p.inner == nil {
		return &typedPatch[T]{}
	}
	return &typedPatch[T]{inner: p.inner.reverse()}
}

func (p *typedPatch[T]) String() string {
	if p.inner == nil {
		return "<nil>"
	}
	return p.inner.format(0)
}

// diffPatch is the internal recursive interface for all patch types.
type diffPatch interface {
	apply(v reflect.Value)
	applyChecked(v reflect.Value) error
	reverse() diffPatch
	format(indent int) string
}

// valuePatch handles replacement of basic types and full replacement of complex types.
type valuePatch struct {
	oldVal reflect.Value
	newVal reflect.Value
}

func (p *valuePatch) apply(v reflect.Value) {
	if !v.CanSet() {
		unsafe.DisableRO(&v)
	}
	if !p.newVal.IsValid() {
		v.Set(reflect.Zero(v.Type()))
	} else {
		v.Set(p.newVal)
	}
}

func (p *valuePatch) applyChecked(v reflect.Value) error {
	if p.oldVal.IsValid() {
		if !reflect.DeepEqual(v.Interface(), p.oldVal.Interface()) {
			return fmt.Errorf("value mismatch: expected %v, got %v", p.oldVal, v)
		}
	}
	p.apply(v)
	return nil
}

func (p *valuePatch) reverse() diffPatch {
	return &valuePatch{oldVal: p.newVal, newVal: p.oldVal}
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

// ptrPatch handles changes to the content pointed to by a pointer.
type ptrPatch struct {
	elemPatch diffPatch
}

func (p *ptrPatch) apply(v reflect.Value) {
	if v.IsNil() {
		val := reflect.New(v.Type().Elem())
		p.elemPatch.apply(val.Elem())
		v.Set(val)
		return
	}
	p.elemPatch.apply(v.Elem())
}

func (p *ptrPatch) applyChecked(v reflect.Value) error {
	if v.IsNil() {
		return fmt.Errorf("cannot apply pointer patch to nil value")
	}
	return p.elemPatch.applyChecked(v.Elem())
}

func (p *ptrPatch) reverse() diffPatch {
	return &ptrPatch{elemPatch: p.elemPatch.reverse()}
}

func (p *ptrPatch) format(indent int) string {
	return p.elemPatch.format(indent)
}

// interfacePatch handles changes to the value stored in an interface.
type interfacePatch struct {
	elemPatch diffPatch
}

func (p *interfacePatch) apply(v reflect.Value) {
	if v.IsNil() {
		return
	}
	elem := v.Elem()
	newElem := reflect.New(elem.Type()).Elem()
	newElem.Set(elem)
	p.elemPatch.apply(newElem)
	v.Set(newElem)
}

func (p *interfacePatch) applyChecked(v reflect.Value) error {
	if v.IsNil() {
		return fmt.Errorf("cannot apply interface patch to nil value")
	}
	elem := v.Elem()
	newElem := reflect.New(elem.Type()).Elem()
	newElem.Set(elem)
	if err := p.elemPatch.applyChecked(newElem); err != nil {
		return err
	}
	v.Set(newElem)
	return nil
}

func (p *interfacePatch) reverse() diffPatch {
	return &interfacePatch{elemPatch: p.elemPatch.reverse()}
}

func (p *interfacePatch) format(indent int) string {
	return p.elemPatch.format(indent)
}

// structPatch handles field-level modifications in a struct.
type structPatch struct {
	fields map[string]diffPatch
}

func (p *structPatch) apply(v reflect.Value) {
	for name, patch := range p.fields {
		f := v.FieldByName(name)
		if f.IsValid() {
			if !f.CanSet() {
				unsafe.DisableRO(&f)
			}
			patch.apply(f)
		}
	}
}

func (p *structPatch) applyChecked(v reflect.Value) error {
	for name, patch := range p.fields {
		f := v.FieldByName(name)
		if !f.IsValid() {
			return fmt.Errorf("field %s not found", name)
		}
		if !f.CanSet() {
			unsafe.DisableRO(&f)
		}
		if err := patch.applyChecked(f); err != nil {
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
	return &structPatch{fields: newFields}
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

// arrayPatch handles index-level modifications in a fixed-size array.
type arrayPatch struct {
	indices map[int]diffPatch
}

func (p *arrayPatch) apply(v reflect.Value) {
	for i, patch := range p.indices {
		if i < v.Len() {
			e := v.Index(i)
			if !e.CanSet() {
				unsafe.DisableRO(&e)
			}
			patch.apply(e)
		}
	}
}

func (p *arrayPatch) applyChecked(v reflect.Value) error {
	for i, patch := range p.indices {
		if i >= v.Len() {
			return fmt.Errorf("index %d out of bounds", i)
		}
		e := v.Index(i)
		if !e.CanSet() {
			unsafe.DisableRO(&e)
		}
		if err := patch.applyChecked(e); err != nil {
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
	return &arrayPatch{indices: newIndices}
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

// mapPatch handles additions, removals, and modifications in a map.
type mapPatch struct {
	added    map[interface{}]reflect.Value
	removed  map[interface{}]reflect.Value
	modified map[interface{}]diffPatch
	keyType  reflect.Type
}

func (p *mapPatch) apply(v reflect.Value) {
	if v.IsNil() {
		if len(p.added) > 0 {
			newMap := reflect.MakeMap(v.Type())
			v.Set(newMap)
		} else {
			return
		}
	}
	for k := range p.removed {
		v.SetMapIndex(reflect.ValueOf(k), reflect.Value{})
	}
	for k, patch := range p.modified {
		keyVal := reflect.ValueOf(k)
		elem := v.MapIndex(keyVal)
		if elem.IsValid() {
			newElem := reflect.New(elem.Type()).Elem()
			newElem.Set(elem)
			patch.apply(newElem)
			v.SetMapIndex(keyVal, newElem)
		}
	}
	for k, val := range p.added {
		v.SetMapIndex(reflect.ValueOf(k), val)
	}
}

func (p *mapPatch) applyChecked(v reflect.Value) error {
	if v.IsNil() {
		if len(p.added) > 0 {
			newMap := reflect.MakeMap(v.Type())
			v.Set(newMap)
		} else if len(p.removed) > 0 || len(p.modified) > 0 {
			return fmt.Errorf("cannot modify/remove from nil map")
		}
	}
	for k, oldVal := range p.removed {
		val := v.MapIndex(reflect.ValueOf(k))
		if !val.IsValid() {
			return fmt.Errorf("key %v not found for removal", k)
		}
		if !reflect.DeepEqual(val.Interface(), oldVal.Interface()) {
			return fmt.Errorf("map removal mismatch for key %v: expected %v, got %v", k, oldVal, val)
		}
	}
	for k, patch := range p.modified {
		val := v.MapIndex(reflect.ValueOf(k))
		if !val.IsValid() {
			return fmt.Errorf("key %v not found for modification", k)
		}
		newElem := reflect.New(val.Type()).Elem()
		newElem.Set(val)
		if err := patch.applyChecked(newElem); err != nil {
			return fmt.Errorf("key %v: %w", k, err)
		}
		v.SetMapIndex(reflect.ValueOf(k), newElem)
	}
	for k := range p.removed {
		v.SetMapIndex(reflect.ValueOf(k), reflect.Value{})
	}
	for k, val := range p.added {
		curr := v.MapIndex(reflect.ValueOf(k))
		if curr.IsValid() {
			return fmt.Errorf("key %v already exists", k)
		}
		v.SetMapIndex(reflect.ValueOf(k), val)
	}
	return nil
}

func (p *mapPatch) reverse() diffPatch {
	newModified := make(map[interface{}]diffPatch)
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
	ops []sliceOp
}

func (p *slicePatch) apply(v reflect.Value) {
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
			newSlice = reflect.Append(newSlice, op.Val)
		case opDel:
			curIdx++
		case opMod:
			if curIdx < v.Len() {
				elem := deepCopyValue(v.Index(curIdx))
				if op.Patch != nil {
					op.Patch.apply(elem)
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

func (p *slicePatch) applyChecked(v reflect.Value) error {
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
			newSlice = reflect.Append(newSlice, op.Val)
		case opDel:
			if curIdx >= v.Len() {
				return fmt.Errorf("slice deletion index %d out of bounds", curIdx)
			}
			curr := v.Index(curIdx)
			if op.Val.IsValid() && !reflect.DeepEqual(curr.Interface(), op.Val.Interface()) {
				return fmt.Errorf("slice deletion mismatch at %d: expected %v, got %v", curIdx, op.Val, curr)
			}
			curIdx++
		case opMod:
			if curIdx >= v.Len() {
				return fmt.Errorf("slice modification index %d out of bounds", curIdx)
			}
			elem := deepCopyValue(v.Index(curIdx))
			if err := op.Patch.applyChecked(elem); err != nil {
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
	return &slicePatch{ops: revOps}
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
