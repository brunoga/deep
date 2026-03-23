package deep

import (
	"fmt"
	"log/slog"
	"reflect"
	"regexp"
	"sort"

	"github.com/brunoga/deep/v5/internal/core"
)

// ApplyOption configures the behaviour of [Apply].
type ApplyOption func(*applyConfig)

type applyConfig struct {
	logger *slog.Logger
}

// WithLogger sets the [slog.Logger] used for [OpLog] operations within a
// single [Apply] call. If not provided, [slog.Default] is used.
func WithLogger(l *slog.Logger) ApplyOption {
	return func(c *applyConfig) { c.logger = l }
}

// Apply applies a Patch to a target pointer.
// v5 prioritizes generated Apply methods but falls back to reflection if needed.
func Apply[T any](target *T, p Patch[T], opts ...ApplyOption) error {
	cfg := applyConfig{logger: slog.Default()}
	for _, o := range opts {
		o(&cfg)
	}

	v := reflect.ValueOf(target)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		return fmt.Errorf("target must be a non-nil pointer")
	}

	// Global condition check — prefer generated EvaluateCondition, fall back to reflection.
	if p.Guard != nil {
		type condEvaluator interface {
			EvaluateCondition(Condition) (bool, error)
		}
		var (
			ok  bool
			err error
		)
		if ce, hasGenCond := any(target).(condEvaluator); hasGenCond {
			ok, err = ce.EvaluateCondition(*p.Guard)
		} else {
			ok, err = evaluateCondition(v.Elem(), p.Guard)
		}
		if err != nil {
			return fmt.Errorf("global condition evaluation failed: %w", err)
		}
		if !ok {
			return fmt.Errorf("global condition not met")
		}
	}

	var errors []error

	applier, hasGenerated := any(target).(interface {
		ApplyOperation(Operation, *slog.Logger) (bool, error)
	})

	for _, op := range p.Operations {
		// Stamp strict from the patch onto each operation before dispatch.
		op.Strict = p.Strict

		// Try generated path first.
		if hasGenerated {
			handled, err := applier.ApplyOperation(op, cfg.logger)
			if err != nil {
				errors = append(errors, err)
				continue
			}
			if handled {
				continue
			}
		}

		// Fallback to reflection.
		// Strict check (Old value verification for Replace and Remove).
		if p.Strict && (op.Kind == OpReplace || op.Kind == OpRemove) {
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
			err = core.DeepPath(op.Path).Set(v.Elem(), newVal)
		case OpRemove:
			err = core.DeepPath(op.Path).Delete(v.Elem())
		case OpMove:
			fromPath := op.Old.(string)
			var val reflect.Value
			val, err = core.DeepPath(fromPath).Resolve(v.Elem())
			if err == nil {
				// Copy the resolved value before deleting the source: Resolve
				// returns a reference into the struct, so Delete would zero val
				// in place before it is written to the destination.
				copied := reflect.New(val.Type()).Elem()
				copied.Set(val)
				if err = core.DeepPath(fromPath).Delete(v.Elem()); err == nil {
					err = core.DeepPath(op.Path).Set(v.Elem(), copied)
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
			cfg.logger.Info("deep log", "message", op.New, "path", op.Path)
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
// When both patches touch the same path, r is consulted if non-nil; otherwise
// the operation with the later HLC timestamp wins. If timestamps are equal or
// zero (e.g. manually built patches), other wins over base.
// The output operations are sorted by path for deterministic ordering.
func Merge[T any](base, other Patch[T], r ConflictResolver) Patch[T] {
	latest := make(map[string]Operation, len(base.Operations)+len(other.Operations))

	mergeOps := func(ops []Operation, isOther bool) {
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
			} else if isOther {
				// other wins over base on conflict
				latest[op.Path] = op
			}
		}
	}

	mergeOps(base.Operations, false)
	mergeOps(other.Operations, true)

	res := Patch[T]{}
	res.Operations = make([]Operation, 0, len(latest))
	for _, op := range latest {
		res.Operations = append(res.Operations, op)
	}
	sort.Slice(res.Operations, func(i, j int) bool {
		return res.Operations[i].Path < res.Operations[j].Path
	})
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

// Clone returns a deep copy of v.
func Clone[T any](v T) T {
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
		for _, sub := range c.Sub {
			ok, err := evaluateCondition(root, sub)
			if err != nil || !ok {
				return false, err
			}
		}
		return true, nil
	}
	if c.Op == "or" {
		for _, sub := range c.Sub {
			ok, err := evaluateCondition(root, sub)
			if err == nil && ok {
				return true, nil
			}
		}
		return false, nil
	}
	if c.Op == "not" {
		if len(c.Sub) > 0 {
			ok, err := evaluateCondition(root, c.Sub[0])
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

