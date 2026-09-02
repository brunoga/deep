package engine

import (
	"fmt"
	"log/slog"
	"reflect"

	"github.com/brunoga/deep/v5/condition"
	icore "github.com/brunoga/deep/v5/internal/core"
)

// ApplyOpReflection applies a single operation to target using reflection.
// It is called by generated Patch methods for operations the generated fast-path does not handle
// (e.g. slice index or map key paths). Direct use is not intended.
func ApplyOpReflection[T any](target *T, op Operation, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}
	return ApplyOpReflectionValue(reflect.ValueOf(target).Elem(), op, logger)
}

// ApplyOpReflectionValue applies op to the already-reflected value v.
func ApplyOpReflectionValue(v reflect.Value, op Operation, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}
	// Strict check. The comparison coerces, because a patch that travelled as
	// JSON carries every number as float64 whatever the field's type; a check
	// that demanded identical types would fail against state it matches.
	// A nil Old is no expectation at all — a hand-built operation that did not
	// record one — so there is nothing to check. Diff always records it.
	if op.Strict && op.Old != nil && (op.Kind == OpReplace || op.Kind == OpRemove) {
		// ResolveMember rather than Resolve: Resolve dereferences, so a
		// pointer field came back as the struct it points at and was compared
		// against an Old that is a pointer, which never matched.
		current, err := icore.DeepPath(op.Path).ResolveMember(v)
		switch {
		case err != nil || !current.IsValid():
			// The path holds nothing, but the patch said what it expected to
			// find. Silently skipping the check, as this used to, left a strict
			// operation unchecked exactly when the target had drifted furthest.
			return fmt.Errorf("strict check failed at %s: expected %v, found nothing", op.Path, op.Old)
		case !icore.EqualCoerced(current, op.Old):
			return fmt.Errorf("strict check failed at %s: expected %v, got %v", op.Path, op.Old, current.Interface())
		}
	}

	// Per-operation conditions. A condition that evaluates to false skips the
	// operation; a condition that cannot be evaluated is an error, matching
	// how the patch-level Guard behaves.
	if op.If != nil {
		ok, err := condition.Evaluate(v, op.If)
		if err != nil {
			return fmt.Errorf("condition evaluation failed at %s: %w", op.Path, err)
		}
		if !ok {
			return nil
		}
	}
	if op.Unless != nil {
		ok, err := condition.Evaluate(v, op.Unless)
		if err != nil {
			return fmt.Errorf("condition evaluation failed at %s: %w", op.Path, err)
		}
		if ok {
			return nil
		}
	}

	// Struct tag enforcement.
	if v.Kind() == reflect.Struct {
		parts := icore.ParsePath(op.Path)
		if len(parts) > 0 {
			info := icore.GetTypeInfo(v.Type())
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
		err = icore.DeepPath(op.Path).Set(v, reflect.ValueOf(op.New))
	case OpRemove:
		err = icore.DeepPath(op.Path).Delete(v)
	case OpMove:
		if op.From == "" {
			return fmt.Errorf("move at %s: missing From source path", op.Path)
		}
		var val reflect.Value
		val, err = icore.DeepPath(op.From).Resolve(v)
		if err == nil {
			copied := reflect.New(val.Type()).Elem()
			copied.Set(val)
			if err = icore.DeepPath(op.From).Delete(v); err == nil {
				err = icore.DeepPath(op.Path).Set(v, copied)
			}
		}
	case OpCopy:
		if op.From == "" {
			return fmt.Errorf("copy at %s: missing From source path", op.Path)
		}
		var val reflect.Value
		val, err = icore.DeepPath(op.From).Resolve(v)
		if err == nil {
			err = icore.DeepPath(op.Path).Set(v, icore.DeepCopyValue(val))
		}
	case OpAlias:
		// Unlike OpCopy, the resolved value is installed as-is: for a pointer
		// that shares the object, which is the point — the destination must end
		// up holding the same object the source path holds.
		if op.From == "" {
			return fmt.Errorf("alias at %s: missing From source path", op.Path)
		}
		var val reflect.Value
		val, err = icore.DeepPath(op.From).ResolveMember(v)
		if err == nil {
			err = icore.DeepPath(op.Path).Set(v, val)
		}
	case OpLog:
		logger.Info("deep log", "message", op.New, "path", op.Path)
	}
	if err != nil {
		return fmt.Errorf("failed to apply %s at %s: %w", op.Kind, op.Path, err)
	}
	return nil
}
