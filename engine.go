package deep

import (
	"fmt"
	"log/slog"
	"reflect"
	"sort"

	"github.com/brunoga/deep/v5/condition"
	"github.com/brunoga/deep/v5/internal/engine"
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
//
// Note: when a Patch has been serialized to JSON and decoded, numeric values in
// Operation.Old and Operation.New will be float64 regardless of the original type.
// This affects strict-mode Old-value checks.
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
		ok, err := condition.EvaluateCondition(v.Elem(), p.Guard)
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
		if err := engine.ApplyOpReflectionValue(v.Elem(), op, cfg.logger); err != nil {
			errors = append(errors, err)
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
// Operations are deduplicated by path. When both patches modify the same path,
// r.Resolve is called if r is non-nil; otherwise other's operation wins over
// base. The output operations are sorted by path for deterministic ordering.
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

	return engine.Equal(a, b)
}

// Clone returns a deep copy of v.
func Clone[T any](v T) T {
	if copyable, ok := any(&v).(interface {
		Clone() *T
	}); ok {
		return *copyable.Clone()
	}

	res, _ := engine.Copy(v)
	return res
}
