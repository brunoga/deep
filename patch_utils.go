package deep

import (
	"reflect"
	"strconv"

	"github.com/brunoga/deep/v3/internal/unsafe"
)

type opInfo struct {
	path string
	op   OpKind
	old  any
	new  any
}

func opPriority(kind OpKind) int {
	switch kind {
	case OpTest:
		return 0
	case OpCopy, OpMove:
		return 1
	case OpAdd:
		return 2
	case OpReplace:
		return 3
	case OpRemove:
		return 4
	default:
		return 5
	}
}

func applyToBuilder[T any](b *Builder[T], op opInfo) error {
	parts := parsePath(op.path)
	if len(parts) > 0 {
		parentParts := parts[:len(parts)-1]
		lastPart := parts[len(parts)-1]

		parentNode, err := b.Root().NavigateParts(parentParts)
		if err == nil && parentNode.typ != nil {
			kind := parentNode.typ.Kind()
			if kind == reflect.Map {
				var key any
				if lastPart.isIndex {
					// Fallback if index was used for map key
					key = strconv.Itoa(lastPart.index)
				} else {
					key = lastPart.key
				}

				// If it's a map using Keyer, we might need the actual key.
				// However, Builder.AddMapEntry/Delete accept the canonical key string
				// if the map is being constructed.

				switch op.op {
				case OpAdd:
					return parentNode.AddMapEntry(key, op.new)
				case OpRemove:
					return parentNode.Delete(key, op.old)
				case OpCopy, OpMove:
					// Ensure the key exists as an addition before calling Copy/Move.
					// This prevents ApplyChecked from trying to modify a missing key.
					if err := parentNode.AddMapEntry(key, nil); err != nil {
						return err
					}
					node, err := parentNode.MapKey(key)
					if err != nil {
						return err
					}
					if op.op == OpCopy {
						node.Copy(op.old.(string))
					} else {
						node.Move(op.old.(string))
					}
					return nil
				}
			} else if kind == reflect.Slice {
				if lastPart.isIndex {
					switch op.op {
					case OpAdd:
						return parentNode.Add(lastPart.index, op.new)
					case OpRemove:
						return parentNode.Delete(lastPart.index, op.old)
					case OpCopy, OpMove:
						// Ensure the index exists as an addition before calling Copy/Move.
						if err := parentNode.Add(lastPart.index, nil); err != nil {
							return err
						}
						node, err := parentNode.Index(lastPart.index)
						if err != nil {
							return err
						}
						if op.op == OpCopy {
							node.Copy(op.old.(string))
						} else {
							node.Move(op.old.(string))
						}
						return nil
					}
				}
			}
		}
	}

	// 2. Default navigation and application for other types (Structs, etc.)
	node, err := b.Root().Navigate(op.path)
	if err != nil {
		return err
	}

	switch op.op {
	case OpAdd, OpReplace:
		if op.old != nil {
			node.Set(op.old, op.new)
		} else {
			node.Put(op.new)
		}
	case OpRemove:
		return node.Remove(op.old)
	case OpMove:
		if from, ok := op.old.(string); ok {
			node.Move(from)
		}
	case OpCopy:
		if from, ok := op.old.(string); ok {
			node.Copy(from)
		}
	case OpTest:
		node.Test(op.new)
	case OpLog:
		if msg, ok := op.new.(string); ok {
			node.Log(msg)
		}
	}
	return nil
}

func convertValue(v reflect.Value, targetType reflect.Type) reflect.Value {
	if !v.IsValid() {
		return reflect.Zero(targetType)
	}

	if v.Type().AssignableTo(targetType) {
		return v
	}

	if v.Type().ConvertibleTo(targetType) {
		return v.Convert(targetType)
	}

	// Handle pointer wrapping
	if targetType.Kind() == reflect.Pointer && v.Type().AssignableTo(targetType.Elem()) {
		ptr := reflect.New(targetType.Elem())
		ptr.Elem().Set(v)
		return ptr
	}

	// Handle JSON/Gob numbers
	if v.Kind() == reflect.Float64 {
		switch targetType.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			return reflect.ValueOf(int64(v.Float())).Convert(targetType)
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			return reflect.ValueOf(uint64(v.Float())).Convert(targetType)
		case reflect.Float32, reflect.Float64:
			return reflect.ValueOf(v.Float()).Convert(targetType)
		}
	}

	return v
}

func setValue(v, newVal reflect.Value) {
	if !newVal.IsValid() {
		if v.CanSet() {
			v.Set(reflect.Zero(v.Type()))
		}
		return
	}

	// Navigate through pointers if needed
	target := v
	for target.Kind() == reflect.Pointer && target.Type() != newVal.Type() {
		if target.IsNil() {
			target.Set(reflect.New(target.Type().Elem()))
		}
		target = target.Elem()
	}

	converted := convertValue(newVal, target.Type())
	target.Set(converted)
}

func valueToInterface(v reflect.Value) any {
	if !v.IsValid() {
		return nil
	}
	if !v.CanInterface() {
		unsafe.DisableRO(&v)
	}
	return v.Interface()
}

func interfaceToValue(i any) reflect.Value {
	if i == nil {
		return reflect.Value{}
	}
	return reflect.ValueOf(i)
}
