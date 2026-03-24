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
	// Strict check.
	if op.Strict && (op.Kind == OpReplace || op.Kind == OpRemove) {
		current, err := icore.DeepPath(op.Path).Resolve(v)
		if err == nil && current.IsValid() {
			if !icore.Equal(current.Interface(), op.Old) {
				return fmt.Errorf("strict check failed at %s: expected %v, got %v", op.Path, op.Old, current.Interface())
			}
		}
	}

	// Per-operation conditions.
	if op.If != nil {
		ok, err := condition.EvaluateCondition(v, op.If)
		if err != nil || !ok {
			return nil
		}
	}
	if op.Unless != nil {
		ok, err := condition.EvaluateCondition(v, op.Unless)
		if err != nil || ok {
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
		fromPath := op.Old.(string)
		var val reflect.Value
		val, err = icore.DeepPath(fromPath).Resolve(v)
		if err == nil {
			copied := reflect.New(val.Type()).Elem()
			copied.Set(val)
			if err = icore.DeepPath(fromPath).Delete(v); err == nil {
				err = icore.DeepPath(op.Path).Set(v, copied)
			}
		}
	case OpCopy:
		fromPath := op.Old.(string)
		var val reflect.Value
		val, err = icore.DeepPath(fromPath).Resolve(v)
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
