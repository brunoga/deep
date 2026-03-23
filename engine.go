package deep

import (
	"fmt"
	"log/slog"
	"reflect"
	"sort"

	"github.com/brunoga/deep/v5/core"
)

type applyConfig struct {
	logger *slog.Logger
}

func newApplyConfig(opts ...ApplyOption) applyConfig {
	cfg := applyConfig{logger: slog.Default()}
	for _, o := range opts {
		o(&cfg)
	}
	return cfg
}

// ApplyOption configures the behaviour of [Apply].
type ApplyOption func(*applyConfig)

// WithLogger sets the [slog.Logger] used for [OpLog] operations within a
// single [Apply] call. If not provided, [slog.Default] is used.
func WithLogger(l *slog.Logger) ApplyOption {
	return func(c *applyConfig) { c.logger = l }
}

// Apply applies a Patch to a target pointer.
// v5 prioritizes the generated Patch method but falls back to reflection if needed.
func Apply[T any](target *T, p Patch[T], opts ...ApplyOption) error {
	v := reflect.ValueOf(target)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		return fmt.Errorf("target must be a non-nil pointer")
	}

	cfg := newApplyConfig(opts...)

	// Dispatch to generated Patch method if available.
	if patcher, ok := any(target).(interface {
		Patch(Patch[T], *slog.Logger) error
	}); ok {
		return patcher.Patch(p, cfg.logger)
	}

	// Reflection fallback.

	if p.Guard != nil {
		ok, err := core.EvaluateCondition(v.Elem(), p.Guard)
		if err != nil {
			return fmt.Errorf("global condition evaluation failed: %w", err)
		}
		if !ok {
			return fmt.Errorf("global condition not met")
		}
	}

	var errors []error
	for _, op := range p.Operations {
		op.Strict = p.Strict
		if err := applyOpReflection(v.Elem(), op, cfg.logger); err != nil {
			errors = append(errors, err)
		}
	}

	if len(errors) > 0 {
		return &ApplyError{Errors: errors}
	}
	return nil
}

// ApplyOpReflection applies a single operation to target via reflection.
// This is called by generated Patch methods for operations the generated fast-path does not handle.
func ApplyOpReflection[T any](target *T, op Operation, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}
	return applyOpReflection(reflect.ValueOf(target).Elem(), op, logger)
}

func applyOpReflection(v reflect.Value, op Operation, logger *slog.Logger) error {
	// Strict check.
	if op.Strict && (op.Kind == OpReplace || op.Kind == OpRemove) {
		current, err := core.DeepPath(op.Path).Resolve(v)
		if err == nil && current.IsValid() {
			if !core.Equal(current.Interface(), op.Old) {
				return fmt.Errorf("strict check failed at %s: expected %v, got %v", op.Path, op.Old, current.Interface())
			}
		}
	}

	// Per-operation conditions.
	if op.If != nil {
		ok, err := core.EvaluateCondition(v, op.If)
		if err != nil || !ok {
			return nil
		}
	}
	if op.Unless != nil {
		ok, err := core.EvaluateCondition(v, op.Unless)
		if err != nil || ok {
			return nil
		}
	}

	// Struct tag enforcement.
	if v.Kind() == reflect.Struct {
		parts := core.ParsePath(op.Path)
		if len(parts) > 0 {
			info := core.GetTypeInfo(v.Type())
			for _, fInfo := range info.Fields {
				if fInfo.Name == parts[0].Key || (fInfo.JSONTag != "" && fInfo.JSONTag == parts[0].Key) {
					if fInfo.Tag.Ignore {
						return nil
					}
					if fInfo.Tag.ReadOnly && op.Kind != OpLog {
						return fmt.Errorf("field %s is read-only", op.Path)
					}
					break
				}
			}
		}
	}

	var err error
	switch op.Kind {
	case OpAdd, OpReplace:
		err = core.DeepPath(op.Path).Set(v, reflect.ValueOf(op.New))
	case OpRemove:
		err = core.DeepPath(op.Path).Delete(v)
	case OpMove:
		fromPath := op.Old.(string)
		var val reflect.Value
		val, err = core.DeepPath(fromPath).Resolve(v)
		if err == nil {
			copied := reflect.New(val.Type()).Elem()
			copied.Set(val)
			if err = core.DeepPath(fromPath).Delete(v); err == nil {
				err = core.DeepPath(op.Path).Set(v, copied)
			}
		}
	case OpCopy:
		fromPath := op.Old.(string)
		var val reflect.Value
		val, err = core.DeepPath(fromPath).Resolve(v)
		if err == nil {
			err = core.DeepPath(op.Path).Set(v, val)
		}
	case OpLog:
		logger.Info("deep log", "message", op.New, "path", op.Path)
	}
	if err != nil {
		return fmt.Errorf("failed to apply %s at %s: %w", op.Kind, op.Path, err)
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
		Clone() *T
	}); ok {
		return *copyable.Clone()
	}

	res, _ := core.Copy(v)
	return res
}
