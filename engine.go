package v5

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"

	"github.com/brunoga/deep/v5/crdt/hlc"
	"github.com/brunoga/deep/v5/internal/core"
)

// Apply applies a Patch to a target pointer.
// v5 prioritizes generated Apply methods but falls back to reflection if needed.
func Apply[T any](target *T, p Patch[T]) error {
	// 1. Global Condition check
	if p.Condition != nil {
		ok, err := evaluateCondition(reflect.ValueOf(target).Elem(), p.Condition)
		if err != nil {
			return fmt.Errorf("global condition evaluation failed: %w", err)
		}
		if !ok {
			return fmt.Errorf("global condition not met")
		}
	}

	var errors []error

	applier, hasGenerated := any(target).(interface {
		ApplyOperation(Operation) (bool, error)
	})

	// 2. Fallback to reflection
	v := reflect.ValueOf(target)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		return fmt.Errorf("target must be a non-nil pointer")
	}

	for _, op := range p.Operations {
		// 1. Try generated path
		if hasGenerated {
			handled, err := applier.ApplyOperation(op)
			if err != nil {
				errors = append(errors, err)
				continue
			}
			if handled {
				continue
			}
		}

		// 2. Fallback to reflection
		// Strict check (Old value verification)
		if p.Strict && op.Kind == OpReplace {
			current, err := resolveInternal(v.Elem(), op.Path)
			if err == nil && current.IsValid() {
				if !core.Equal(current.Interface(), op.Old) {
					errors = append(errors, fmt.Errorf("strict check failed at %s: expected %v, got %v", op.Path, op.Old, current.Interface()))
					continue
				}
			}
		}

		// Per-operation conditions
		if op.If != nil {
			ok, err := evaluateCondition(v.Elem(), op.If)
			if err != nil || !ok {
				continue // Skip operation
			}
		}
		if op.Unless != nil {
			ok, err := evaluateCondition(v.Elem(), op.Unless)
			if err == nil && ok {
				continue // Skip operation
			}
		}

		// Struct Tag Enforcement

		if v.Elem().Kind() == reflect.Struct {
			parts := core.ParsePath(op.Path)
			if len(parts) > 0 {
				_, sf, ok := findField(v.Elem(), parts[0].Key)
				if ok {
					tag := core.ParseTag(sf)
					if tag.Ignore {
						continue
					}
					if tag.ReadOnly && op.Kind != OpLog {
						errors = append(errors, fmt.Errorf("field %s is read-only", op.Path))
						continue
					}
				}
			}
		}

		var err error
		switch op.Kind {
		case OpAdd, OpReplace:
			newVal := reflect.ValueOf(op.New)

			// LWW logic
			if op.Timestamp.WallTime != 0 {
				current, err := resolveInternal(v.Elem(), op.Path)
				if err == nil && current.IsValid() {
					if current.Kind() == reflect.Struct {
						tsField := current.FieldByName("Timestamp")
						if tsField.IsValid() {
							if currentTS, ok := tsField.Interface().(hlc.HLC); ok {
								if !op.Timestamp.After(currentTS) {
									continue
								}
							}
						}
					}
				}
			}

			// We use a custom set logic that uses findField internally
			err = setValueInternal(v.Elem(), op.Path, newVal)
		case OpRemove:
			err = deleteValueInternal(v.Elem(), op.Path)
		case OpMove:
			fromPath := op.Old.(string)
			var val reflect.Value
			val, err = resolveInternal(v.Elem(), fromPath)
			if err == nil {
				if err = deleteValueInternal(v.Elem(), fromPath); err == nil {
					err = setValueInternal(v.Elem(), op.Path, val)
				}
			}
		case OpCopy:
			fromPath := op.Old.(string)
			var val reflect.Value
			val, err = resolveInternal(v.Elem(), fromPath)
			if err == nil {
				err = setValueInternal(v.Elem(), op.Path, val)
			}
		case OpLog:
			fmt.Printf("DEEP LOG: %s (at %s)\n", op.New, op.Path)
		}

		if err != nil {
			errors = append(errors, fmt.Errorf("failed to apply %s at %s: %w", op.Kind, op.Path, err))
		}
	}

	if len(errors) > 0 {
		return &ApplyError{Errors: errors}
	}
	return nil
}

// ConflictResolver defines how to resolve merge conflicts.
type ConflictResolver interface {
	Resolve(path string, local, remote any) any
}

// Merge combines two patches into a single patch, resolving conflicts.
func Merge[T any](base, other Patch[T], r ConflictResolver) Patch[T] {
	res := Patch[T]{}
	latest := make(map[string]Operation)

	mergeOps := func(ops []Operation) {
		for _, op := range ops {
			existing, ok := latest[op.Path]
			if !ok {
				latest[op.Path] = op
				continue
			}

			if r != nil {
				resolvedVal := r.Resolve(op.Path, existing.New, op.New)
				op.New = resolvedVal
				latest[op.Path] = op
			} else if op.Timestamp.After(existing.Timestamp) {
				latest[op.Path] = op
			}
		}
	}

	mergeOps(base.Operations)
	mergeOps(other.Operations)

	for _, op := range latest {
		res.Operations = append(res.Operations, op)
	}

	return res
}

// Equal returns true if a and b are deeply equal.
func Equal[T any](a, b T) bool {
	if equallable, ok := any(&a).(interface {
		Equal(*T) bool
	}); ok {
		return equallable.Equal(&b)
	}

	return core.Equal(a, b)
}

// Copy returns a deep copy of v.
func Copy[T any](v T) T {
	if copyable, ok := any(&v).(interface {
		Copy() *T
	}); ok {
		return *copyable.Copy()
	}

	res, _ := core.Copy(v)
	return res
}

func evaluateCondition(root reflect.Value, c *Condition) (bool, error) {
	if c == nil {
		return true, nil
	}

	if c.Op == "and" {
		for _, sub := range c.Apply {
			ok, err := evaluateCondition(root, sub)
			if err != nil || !ok {
				return false, err
			}
		}
		return true, nil
	}
	if c.Op == "or" {
		for _, sub := range c.Apply {
			ok, err := evaluateCondition(root, sub)
			if err == nil && ok {
				return true, nil
			}
		}
		return false, nil
	}
	if c.Op == "not" {
		if len(c.Apply) > 0 {
			ok, err := evaluateCondition(root, c.Apply[0])
			if err != nil {
				return false, err
			}
			return !ok, nil
		}
	}

	val, err := resolveInternal(root, c.Path)
	if err != nil {
		if c.Op == "exists" {
			return false, nil
		}
		return false, err
	}

	if c.Op == "exists" {
		return val.IsValid(), nil
	}

	if c.Op == "log" {
		fmt.Printf("DEEP LOG CONDITION: %s (at %s, value: %v)\n", c.Value, c.Path, val.Interface())
		return true, nil
	}

	if c.Op == "matches" {
		pattern, ok := c.Value.(string)
		if !ok {
			return false, fmt.Errorf("matches requires string pattern")
		}
		matched, err := regexp.MatchString(pattern, fmt.Sprintf("%v", val.Interface()))
		return matched, err
	}

	if c.Op == "type" {
		expectedType, ok := c.Value.(string)
		if !ok {
			return false, fmt.Errorf("type requires string value")
		}
		return checkType(val.Interface(), expectedType), nil
	}

	return core.CompareValues(val, reflect.ValueOf(c.Value), c.Op, false)
}

func checkType(v any, typeName string) bool {
	rv := reflect.ValueOf(v)
	switch typeName {
	case "string":
		return rv.Kind() == reflect.String
	case "number":
		k := rv.Kind()
		return (k >= reflect.Int && k <= reflect.Int64) ||
			(k >= reflect.Uint && k <= reflect.Uintptr) ||
			(k == reflect.Float32 || k == reflect.Float64)
	case "boolean":
		return rv.Kind() == reflect.Bool
	case "object":
		return rv.Kind() == reflect.Struct || rv.Kind() == reflect.Map
	case "array":
		return rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array
	case "null":
		if !rv.IsValid() {
			return true
		}
		return (rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface || rv.Kind() == reflect.Slice || rv.Kind() == reflect.Map) && rv.IsNil()
	}
	return false
}

func findField(v reflect.Value, name string) (reflect.Value, reflect.StructField, bool) {
	typ := v.Type()
	if typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ.Kind() != reflect.Struct {
		return reflect.Value{}, reflect.StructField{}, false
	}

	// 1. Match by name
	f := v.FieldByName(name)
	if f.IsValid() {
		sf, _ := typ.FieldByName(name)
		return f, sf, true
	}

	// 2. Match by JSON tag
	for i := 0; i < typ.NumField(); i++ {
		sf := typ.Field(i)
		tag := sf.Tag.Get("json")
		if tag == "" {
			continue
		}
		tagParts := strings.Split(tag, ",")
		if tagParts[0] == name {
			return v.Field(i), sf, true
		}
	}

	return reflect.Value{}, reflect.StructField{}, false
}

func resolveInternal(root reflect.Value, path string) (reflect.Value, error) {
	parts := core.ParsePath(path)
	current := root
	var err error

	for _, part := range parts {
		current, err = core.Dereference(current)
		if err != nil {
			return reflect.Value{}, err
		}

		if part.IsIndex && (current.Kind() == reflect.Slice || current.Kind() == reflect.Array) {
			if part.Index < 0 || part.Index >= current.Len() {
				return reflect.Value{}, fmt.Errorf("index out of bounds: %d", part.Index)
			}
			current = current.Index(part.Index)
		} else if current.Kind() == reflect.Map {
			// Map logic from core
			keyType := current.Type().Key()
			var keyVal reflect.Value
			key := part.Key
			if key == "" && part.IsIndex {
				key = fmt.Sprintf("%d", part.Index)
			}
			if keyType.Kind() == reflect.String {
				keyVal = reflect.ValueOf(key)
			} else {
				return reflect.Value{}, fmt.Errorf("unsupported map key type")
			}
			val := current.MapIndex(keyVal)
			if !val.IsValid() {
				return reflect.Value{}, nil
			}
			current = val
		} else if current.Kind() == reflect.Struct {
			key := part.Key
			if key == "" && part.IsIndex {
				key = fmt.Sprintf("%d", part.Index)
			}
			f, _, ok := findField(current, key)
			if !ok {
				return reflect.Value{}, fmt.Errorf("field %s not found", key)
			}
			current = f
		} else {
			return reflect.Value{}, fmt.Errorf("cannot access %s on %v", part.Key, current.Type())
		}
	}
	return current, nil
}

func setValueInternal(v reflect.Value, path string, val reflect.Value) error {
	parts := core.ParsePath(path)
	if len(parts) == 0 {
		if !v.CanSet() {
			return fmt.Errorf("cannot set root")
		}
		v.Set(val)
		return nil
	}

	parentPath := ""
	if len(parts) > 1 {
		parentParts := parts[:len(parts)-1]
		var b strings.Builder
		for _, p := range parentParts {
			b.WriteByte('/')
			if p.IsIndex {
				b.WriteString(fmt.Sprintf("%d", p.Index))
			} else {
				b.WriteString(core.EscapeKey(p.Key))
			}
		}
		parentPath = b.String()
	}

	parent, err := resolveInternal(v, parentPath)
	if err != nil {
		return err
	}

	lastPart := parts[len(parts)-1]

	switch parent.Kind() {
	case reflect.Map:
		if parent.IsNil() {
			return fmt.Errorf("cannot set in nil map")
		}
		keyType := parent.Type().Key()
		var keyVal reflect.Value
		key := lastPart.Key
		if key == "" && lastPart.IsIndex {
			key = fmt.Sprintf("%d", lastPart.Index)
		}
		if keyType.Kind() == reflect.String {
			keyVal = reflect.ValueOf(key)
		}
		parent.SetMapIndex(keyVal, core.ConvertValue(val, parent.Type().Elem()))
		return nil
	case reflect.Slice:
		if !parent.CanSet() {
			return fmt.Errorf("cannot set in un-settable slice at %s", path)
		}
		idx := lastPart.Index
		if idx < 0 || idx > parent.Len() {
			return fmt.Errorf("index out of bounds")
		}
		if idx == parent.Len() {
			parent.Set(reflect.Append(parent, core.ConvertValue(val, parent.Type().Elem())))
		} else {
			parent.Index(idx).Set(core.ConvertValue(val, parent.Type().Elem()))
		}
		return nil
	case reflect.Struct:
		key := lastPart.Key
		if key == "" && lastPart.IsIndex {
			key = fmt.Sprintf("%d", lastPart.Index)
		}
		f, _, ok := findField(parent, key)
		if !ok {
			return fmt.Errorf("field %s not found", key)
		}
		if !f.CanSet() {
			return fmt.Errorf("cannot set un-settable field %s", key)
		}
		f.Set(core.ConvertValue(val, f.Type()))
		return nil
	}
	return fmt.Errorf("cannot set value in %v", parent.Kind())
}

func deleteValueInternal(v reflect.Value, path string) error {
	parts := core.ParsePath(path)
	if len(parts) == 0 {
		return fmt.Errorf("cannot delete root")
	}

	parentPath := ""
	if len(parts) > 1 {
		parentParts := parts[:len(parts)-1]
		var b strings.Builder
		for _, p := range parentParts {
			b.WriteByte('/')
			if p.IsIndex {
				b.WriteString(fmt.Sprintf("%d", p.Index))
			} else {
				b.WriteString(core.EscapeKey(p.Key))
			}
		}
		parentPath = b.String()
	}

	parent, err := resolveInternal(v, parentPath)
	if err != nil {
		return err
	}

	lastPart := parts[len(parts)-1]

	switch parent.Kind() {
	case reflect.Map:
		if parent.IsNil() {
			return nil
		}
		keyType := parent.Type().Key()
		var keyVal reflect.Value
		key := lastPart.Key
		if key == "" && lastPart.IsIndex {
			key = fmt.Sprintf("%d", lastPart.Index)
		}
		if keyType.Kind() == reflect.String {
			keyVal = reflect.ValueOf(key)
		}
		parent.SetMapIndex(keyVal, reflect.Value{})
		return nil
	case reflect.Slice:
		if !parent.CanSet() {
			return fmt.Errorf("cannot delete from un-settable slice at %s", path)
		}
		idx := lastPart.Index
		if idx < 0 || idx >= parent.Len() {
			return fmt.Errorf("index out of bounds")
		}
		newSlice := reflect.AppendSlice(parent.Slice(0, idx), parent.Slice(idx+1, parent.Len()))
		parent.Set(newSlice)
		return nil
	case reflect.Struct:
		key := lastPart.Key
		if key == "" && lastPart.IsIndex {
			key = fmt.Sprintf("%d", lastPart.Index)
		}
		f, _, ok := findField(parent, key)
		if !ok {
			return fmt.Errorf("field %s not found", key)
		}
		if !f.CanSet() {
			return fmt.Errorf("cannot delete from un-settable field %s", key)
		}
		f.Set(reflect.Zero(f.Type()))
		return nil
	}
	return fmt.Errorf("cannot delete from %v", parent.Kind())
}

func contains[M ~map[K]V, K comparable, V any](m M, k K) bool {
	_, ok := m[k]
	return ok
}


