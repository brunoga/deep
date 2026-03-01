package deep

import (
	"fmt"
	"reflect"
	"regexp"

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
			current, err := core.DeepPath(op.Path).Resolve(v.Elem())
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
				info := core.GetTypeInfo(v.Elem().Type())
				var tag core.StructTag
				found := false
				for _, fInfo := range info.Fields {
					if fInfo.Name == parts[0].Key || (fInfo.JSONTag != "" && fInfo.JSONTag == parts[0].Key) {
						tag = fInfo.Tag
						found = true
						break
					}
				}
				
				if found {
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
				current, err := core.DeepPath(op.Path).Resolve(v.Elem())
				if err == nil && current.IsValid() {
					if current.Kind() == reflect.Struct {
						info := core.GetTypeInfo(current.Type())
						var tsField reflect.Value
						for _, fInfo := range info.Fields {
							if fInfo.Name == "Timestamp" {
								tsField = current.Field(fInfo.Index)
								break
							}
						}
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

			// We use core.DeepPath for set logic
			err = core.DeepPath(op.Path).Set(v.Elem(), newVal)
		case OpRemove:
			err = core.DeepPath(op.Path).Delete(v.Elem())
		case OpMove:
			fromPath := op.Old.(string)
			var val reflect.Value
			val, err = core.DeepPath(fromPath).Resolve(v.Elem())
			if err == nil {
				if err = core.DeepPath(fromPath).Delete(v.Elem()); err == nil {
					err = core.DeepPath(op.Path).Set(v.Elem(), val)
				}
			}
		case OpCopy:
			fromPath := op.Old.(string)
			var val reflect.Value
			val, err = core.DeepPath(fromPath).Resolve(v.Elem())
			if err == nil {
				err = core.DeepPath(op.Path).Set(v.Elem(), val)
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

	val, err := core.DeepPath(c.Path).Resolve(root)
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

