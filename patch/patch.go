package patch

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/brunoga/deep/internal"
)

// Patch is a slice of Operations that represents a patch.
type Patch[T any] []Operation

// New creates a new empty Patch.
func New[T any]() Patch[T] {
	return Patch[T]{}
}

// validatePath validates if a path is valid for type T. If valueType is not
// nil, it also checks if the type at the path can accept a value of valueType.
// Returns the reflect.Type at the specified path or an error if validation
// fails.
func validatePath[T any](path string, valueType reflect.Type) (reflect.Type, error) {
	rootType := reflect.TypeOf((*T)(nil)).Elem()

	// Handle empty or root path
	if path == "" || path == "/" {
		if valueType != nil && !valueType.AssignableTo(rootType) {
			return nil, fmt.Errorf("type mismatch at root: cannot assign %v to %v", valueType, rootType)
		}
		return rootType, nil
	}

	// Split the path into segments and decode escaping.
	segments := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for i := range segments {
		segments[i] = strings.ReplaceAll(segments[i], "~1", "/")
		segments[i] = strings.ReplaceAll(segments[i], "~0", "~")
	}

	currentType := rootType
	for i, segment := range segments {
		// Handle special "-" segment for arrays/slices (append)
		if segment == "-" {
			if i != len(segments)-1 {
				return nil, fmt.Errorf("'-' can only be used as the last segment in a path")
			}
			if currentType.Kind() != reflect.Array && currentType.Kind() != reflect.Slice {
				return nil, fmt.Errorf("'-' can only be used with arrays or slices, got %v", currentType)
			}
			currentType = currentType.Elem()
			continue
		}

		for currentType.Kind() == reflect.Ptr {
			// Dereference pointers
			currentType = currentType.Elem()
		}

		switch currentType.Kind() {
		case reflect.Struct:
			field, found := currentType.FieldByName(segment)
			if !found {
				return nil, fmt.Errorf("field %q not found in struct %v", segment, currentType)
			}
			currentType = field.Type

		case reflect.Map:
			// Try parsing the key based on the map's key type
			keyType := currentType.Key()
			switch keyType.Kind() {
			case reflect.String:
				// String keys are fine as-is
			case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
				if _, err := strconv.ParseInt(segment, 10, 64); err != nil {
					return nil, fmt.Errorf("invalid map key %q for %v: %v", segment, keyType, err)
				}
			case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
				if _, err := strconv.ParseUint(segment, 10, 64); err != nil {
					return nil, fmt.Errorf("invalid map key %q for %v: %v", segment, keyType, err)
				}
			case reflect.Float32, reflect.Float64:
				if _, err := strconv.ParseFloat(segment, 64); err != nil {
					return nil, fmt.Errorf("invalid map key %q for %v: %v", segment, keyType, err)
				}
			case reflect.Bool:
				if _, err := strconv.ParseBool(segment); err != nil {
					return nil, fmt.Errorf("invalid map key %q for %v: %v", segment, keyType, err)
				}
			default:
				return nil, fmt.Errorf("unsupported map key type %v", keyType)
			}

			// Continue with the map's value type
			currentType = currentType.Elem()

		case reflect.Array, reflect.Slice:
			// Validate index for arrays/slices
			if _, err := strconv.Atoi(segment); err != nil {
				return nil, fmt.Errorf("invalid array/slice index %q: %v", segment, err)
			}
			currentType = currentType.Elem()

		case reflect.Interface:
			// We can't validate beyond an interface statically
			if i < len(segments)-1 {
				return nil, fmt.Errorf("cannot navigate beyond interface type %v", currentType)
			}

		default:
			return nil, fmt.Errorf("cannot navigate into %v using path segment %q", currentType, segment)
		}
	}

	// Check if the provided value type is compatible with the target type
	if valueType != nil && !valueType.AssignableTo(currentType) {
		return nil, fmt.Errorf("type mismatch at %q: cannot assign %v to %v", path, valueType, currentType)
	}

	return currentType, nil
}

// Add creates a new operation to add a value at the specified path.
func (p Patch[T]) Add(path string, value any) Patch[T] {
	valueType := reflect.TypeOf(value)
	if _, err := validatePath[T](path, valueType); err != nil {
		panic(fmt.Sprintf("invalid Add operation: %v", err))
	}

	return append(p, Operation{
		Op:    OperationTypeAdd,
		Path:  path,
		Value: value,
		From:  "",
	})
}

// Remove creates a new operation to remove the value at the specified path.
func (p Patch[T]) Remove(path string) Patch[T] {
	if _, err := validatePath[T](path, nil); err != nil {
		panic(fmt.Sprintf("invalid Remove operation: %v", err))
	}

	return append(p, Operation{
		Op:    OperationTypeRemove,
		Path:  path,
		Value: nil,
		From:  "",
	})
}

// Replace creates a new operation to replace the value at the specified path.
func (p Patch[T]) Replace(path string, value any) Patch[T] {
	valueType := reflect.TypeOf(value)
	if _, err := validatePath[T](path, valueType); err != nil {
		panic(fmt.Sprintf("invalid Replace operation: %v", err))
	}

	return append(p, Operation{
		Op:    OperationTypeReplace,
		Path:  path,
		Value: value,
		From:  "",
	})
}

// Move creates a new operation to move a value from one path to another.
func (p Patch[T]) Move(from, to string) Patch[T] {
	// Validate source path
	fromType, err := validatePath[T](from, nil)
	if err != nil {
		panic(fmt.Sprintf("invalid Move operation source: %v", err))
	}

	// Validate destination path with source type
	if _, err := validatePath[T](to, fromType); err != nil {
		panic(fmt.Sprintf("invalid Move operation destination: %v", err))
	}

	return append(p, Operation{
		Op:    OperationTypeMove,
		Path:  to,
		Value: nil,
		From:  from,
	})
}

// Copy creates a new operation to copy a value from one path to another.
func (p Patch[T]) Copy(from, to string) Patch[T] {
	// Validate source path
	fromType, err := validatePath[T](from, nil)
	if err != nil {
		panic(fmt.Sprintf("invalid Copy operation source: %v", err))
	}

	// Validate destination path with source type
	if _, err := validatePath[T](to, fromType); err != nil {
		panic(fmt.Sprintf("invalid Copy operation destination: %v", err))
	}

	return append(p, Operation{
		Op:    OperationTypeCopy,
		Path:  to,
		Value: nil,
		From:  from,
	})
}

// Test creates a new operation to test the value at the specified path.
func (p Patch[T]) Test(path string, value any) Patch[T] {
	valueType := reflect.TypeOf(value)
	if _, err := validatePath[T](path, valueType); err != nil {
		panic(fmt.Sprintf("invalid Test operation: %v", err))
	}

	return append(p, Operation{
		Op:    OperationTypeTest,
		Path:  path,
		Value: value,
		From:  "",
	})
}

// Apply applies the patch to the given target. It returns an error if the
// operation is not valid or if the target does not match the expected value.
func (p Patch[T]) Apply(target *T) error {
	for i, op := range p {
		if err := applyOperation(target, op); err != nil {
			return fmt.Errorf("operation %d (%s %s): %w", i, op.Op, op.Path, err)
		}
	}
	return nil
}

// applyOperation applies a single patch operation to the target value.
func applyOperation[T any](target *T, op Operation) error {
	targetVal := reflect.ValueOf(target).Elem()

	switch op.Op {
	case OperationTypeAdd:
		return applyAdd(targetVal, op.Path, op.Value)
	case OperationTypeRemove:
		return applyRemove(targetVal, op.Path)
	case OperationTypeReplace:
		return applyReplace(targetVal, op.Path, op.Value)
	case OperationTypeMove:
		return applyMove(targetVal, op.From, op.Path)
	case OperationTypeCopy:
		return applyCopy(targetVal, op.From, op.Path)
	case OperationTypeTest:
		return applyTest(targetVal, op.Path, op.Value)
	default:
		return fmt.Errorf("invalid operation type: %s", op.Op)
	}
}

// getValueAtPath returns the reflect.Value at the specified path.
func getValueAtPath(v reflect.Value, path string) (reflect.Value, error) {
	if path == "" || path == "/" {
		return v, nil
	}

	segments := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for i := range segments {
		segments[i] = strings.ReplaceAll(segments[i], "~1", "/")
		segments[i] = strings.ReplaceAll(segments[i], "~0", "~")
	}

	current := v
	for _, segment := range segments {
		// Handle special "-" segment for arrays/slices (only valid in the last segment for add operation)
		if segment == "-" {
			return reflect.Value{}, fmt.Errorf("'-' can only be used with add operation")
		}

		for current.Kind() == reflect.Ptr {
			if current.IsNil() {
				return reflect.Value{}, fmt.Errorf("nil pointer in path")
			}
			current = current.Elem()
		}

		switch current.Kind() {
		case reflect.Struct:
			field := current.FieldByName(segment)
			if !field.IsValid() {
				return reflect.Value{}, fmt.Errorf("field %q not found in struct %v", segment, current.Type())
			}
			// Make unexported fields accessible
			internal.DisableRO(&field)
			current = field

		case reflect.Map:
			if current.IsNil() {
				return reflect.Value{}, fmt.Errorf("nil map in path")
			}

			keyType := current.Type().Key()
			key := reflect.New(keyType).Elem()

			switch keyType.Kind() {
			case reflect.String:
				key.SetString(segment)
			case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
				i, err := strconv.ParseInt(segment, 10, 64)
				if err != nil {
					return reflect.Value{}, fmt.Errorf("invalid map key %q: %v", segment, err)
				}
				key.SetInt(i)
			case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
				u, err := strconv.ParseUint(segment, 10, 64)
				if err != nil {
					return reflect.Value{}, fmt.Errorf("invalid map key %q: %v", segment, err)
				}
				key.SetUint(u)
			case reflect.Float32, reflect.Float64:
				f, err := strconv.ParseFloat(segment, 64)
				if err != nil {
					return reflect.Value{}, fmt.Errorf("invalid map key %q: %v", segment, err)
				}
				key.SetFloat(f)
			case reflect.Bool:
				b, err := strconv.ParseBool(segment)
				if err != nil {
					return reflect.Value{}, fmt.Errorf("invalid map key %q: %v", segment, err)
				}
				key.SetBool(b)
			default:
				return reflect.Value{}, fmt.Errorf("unsupported map key type: %v", keyType)
			}

			mapVal := current.MapIndex(key)
			if !mapVal.IsValid() {
				return reflect.Value{}, fmt.Errorf("map key %q not found", segment)
			}
			current = mapVal

		case reflect.Slice, reflect.Array:
			idx, err := strconv.Atoi(segment)
			if err != nil {
				return reflect.Value{}, fmt.Errorf("invalid index %q: %v", segment, err)
			}

			if idx < 0 || idx >= current.Len() {
				return reflect.Value{}, fmt.Errorf("index %d out of bounds [0:%d]", idx, current.Len())
			}
			current = current.Index(idx)

		default:
			return reflect.Value{}, fmt.Errorf("cannot traverse into %v", current.Type())
		}
	}

	return current, nil
}

// getParentAndLastSegment returns the parent reflect.Value and the last path segment.
func getParentAndLastSegment(v reflect.Value, path string) (reflect.Value, string, error) {
	if path == "" || path == "/" {
		return reflect.Value{}, "", fmt.Errorf("cannot get parent of root path")
	}

	segments := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for i := range segments {
		segments[i] = strings.ReplaceAll(segments[i], "~1", "/")
		segments[i] = strings.ReplaceAll(segments[i], "~0", "~")
	}

	if len(segments) == 1 {
		return v, segments[0], nil
	}

	// Navigate to parent (all segments except the last)
	current := v
	for _, segment := range segments[:len(segments)-1] {
		// Handle special "-" segment for arrays/slices (only valid in the last segment for add operation)
		if segment == "-" {
			return reflect.Value{}, "", fmt.Errorf("'-' can only be used as the last segment in a path")
		}

		// Dereference any pointers we encounter while traversing
		for current.Kind() == reflect.Ptr {
			if current.IsNil() {
				return reflect.Value{}, "", fmt.Errorf("nil pointer in path")
			}
			current = current.Elem()
		}

		switch current.Kind() {
		case reflect.Struct:
			field := current.FieldByName(segment)
			if !field.IsValid() {
				return reflect.Value{}, "", fmt.Errorf("field %q not found in struct %v", segment, current.Type())
			}
			// Make unexported fields accessible
			internal.DisableRO(&field)
			current = field

		case reflect.Map:
			if current.IsNil() {
				return reflect.Value{}, "", fmt.Errorf("nil map in path")
			}

			keyType := current.Type().Key()
			key := reflect.New(keyType).Elem()

			switch keyType.Kind() {
			case reflect.String:
				key.SetString(segment)
			case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
				i, err := strconv.ParseInt(segment, 10, 64)
				if err != nil {
					return reflect.Value{}, "", fmt.Errorf("invalid map key %q: %v", segment, err)
				}
				key.SetInt(i)
			case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
				u, err := strconv.ParseUint(segment, 10, 64)
				if err != nil {
					return reflect.Value{}, "", fmt.Errorf("invalid map key %q: %v", segment, err)
				}
				key.SetUint(u)
			case reflect.Float32, reflect.Float64:
				f, err := strconv.ParseFloat(segment, 64)
				if err != nil {
					return reflect.Value{}, "", fmt.Errorf("invalid map key %q: %v", segment, err)
				}
				key.SetFloat(f)
			case reflect.Bool:
				b, err := strconv.ParseBool(segment)
				if err != nil {
					return reflect.Value{}, "", fmt.Errorf("invalid map key %q: %v", segment, err)
				}
				key.SetBool(b)
			default:
				return reflect.Value{}, "", fmt.Errorf("unsupported map key type: %v", keyType)
			}

			mapVal := current.MapIndex(key)
			if !mapVal.IsValid() {
				return reflect.Value{}, "", fmt.Errorf("map key %q not found", segment)
			}
			current = mapVal

		case reflect.Slice, reflect.Array:
			idx, err := strconv.Atoi(segment)
			if err != nil {
				return reflect.Value{}, "", fmt.Errorf("invalid index %q: %v", segment, err)
			}

			if idx < 0 || idx >= current.Len() {
				return reflect.Value{}, "", fmt.Errorf("index %d out of bounds [0:%d]", idx, current.Len())
			}
			current = current.Index(idx)

		default:
			return reflect.Value{}, "", fmt.Errorf("cannot traverse into %v", current.Type())
		}
	}

	// Ensure that if the parent is a pointer, we dereference it before returning
	for current.Kind() == reflect.Ptr {
		if current.IsNil() {
			return reflect.Value{}, "", fmt.Errorf("nil pointer in path")
		}
		current = current.Elem()
	}

	return current, segments[len(segments)-1], nil
}

// applyAdd implements the "add" operation.
func applyAdd(target reflect.Value, path string, value interface{}) error {
	// Handle special case for appending to arrays
	if strings.HasSuffix(path, "/-") {
		parentPath := path[:len(path)-2]
		parent, err := getValueAtPath(target, parentPath)
		if err != nil {
			return err
		}

		if parent.Kind() != reflect.Slice {
			return fmt.Errorf("can only append to slice, got %v", parent.Type())
		}

		valueVal := reflect.ValueOf(value)
		if !valueVal.Type().AssignableTo(parent.Type().Elem()) {
			return fmt.Errorf("cannot append %v to %v", valueVal.Type(), parent.Type())
		}

		parent.Set(reflect.Append(parent, valueVal))
		return nil
	}

	// Handle adding to maps and other containers
	parent, lastSegment, err := getParentAndLastSegment(target, path)
	if err != nil {
		return err
	}

	valueVal := reflect.ValueOf(value)

	switch parent.Kind() {
	case reflect.Map:
		keyType := parent.Type().Key()
		elemType := parent.Type().Elem()

		if !valueVal.Type().AssignableTo(elemType) {
			return fmt.Errorf("cannot assign %v to %v", valueVal.Type(), elemType)
		}

		key := reflect.New(keyType).Elem()
		switch keyType.Kind() {
		case reflect.String:
			key.SetString(lastSegment)
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			i, err := strconv.ParseInt(lastSegment, 10, 64)
			if err != nil {
				return fmt.Errorf("invalid map key %q: %v", lastSegment, err)
			}
			key.SetInt(i)
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			u, err := strconv.ParseUint(lastSegment, 10, 64)
			if err != nil {
				return fmt.Errorf("invalid map key %q: %v", lastSegment, err)
			}
			key.SetUint(u)
		case reflect.Float32, reflect.Float64:
			f, err := strconv.ParseFloat(lastSegment, 64)
			if err != nil {
				return fmt.Errorf("invalid map key %q: %v", lastSegment, err)
			}
			key.SetFloat(f)
		case reflect.Bool:
			b, err := strconv.ParseBool(lastSegment)
			if err != nil {
				return fmt.Errorf("invalid map key %q: %v", lastSegment, err)
			}
			key.SetBool(b)
		default:
			return fmt.Errorf("unsupported map key type: %v", keyType)
		}

		parent.SetMapIndex(key, valueVal)

	case reflect.Slice:
		idx, err := strconv.Atoi(lastSegment)
		if err != nil {
			return fmt.Errorf("invalid index %q: %v", lastSegment, err)
		}

		if idx < 0 || idx > parent.Len() {
			return fmt.Errorf("index %d out of bounds [0:%d]", idx, parent.Len())
		}

		// For slices, we need to grow the slice and shift elements
		elemType := parent.Type().Elem()
		if !valueVal.Type().AssignableTo(elemType) {
			return fmt.Errorf("cannot assign %v to %v", valueVal.Type(), elemType)
		}

		newSlice := reflect.MakeSlice(parent.Type(), parent.Len()+1, parent.Cap()+1)

		// Copy elements before insert point
		reflect.Copy(newSlice, parent.Slice(0, idx))

		// Set the new element
		newSlice.Index(idx).Set(valueVal)

		// Copy elements after insert point
		reflect.Copy(newSlice.Slice(idx+1, newSlice.Len()), parent.Slice(idx, parent.Len()))

		parent.Set(newSlice)

	case reflect.Struct:
		field := parent.FieldByName(lastSegment)
		if !field.IsValid() {
			return fmt.Errorf("field %q not found in struct %v", lastSegment, parent.Type())
		}

		internal.DisableRO(&field)

		if !valueVal.Type().AssignableTo(field.Type()) {
			return fmt.Errorf("cannot assign %v to %v", valueVal.Type(), field.Type())
		}

		field.Set(valueVal)

	default:
		return fmt.Errorf("cannot add to %v", parent.Type())
	}

	return nil
}

// applyRemove implements the "remove" operation.
func applyRemove(target reflect.Value, path string) error {
	parent, lastSegment, err := getParentAndLastSegment(target, path)
	if err != nil {
		return err
	}

	switch parent.Kind() {
	case reflect.Map:
		keyType := parent.Type().Key()
		key := reflect.New(keyType).Elem()

		switch keyType.Kind() {
		case reflect.String:
			key.SetString(lastSegment)
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			i, err := strconv.ParseInt(lastSegment, 10, 64)
			if err != nil {
				return fmt.Errorf("invalid map key %q: %v", lastSegment, err)
			}
			key.SetInt(i)
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			u, err := strconv.ParseUint(lastSegment, 10, 64)
			if err != nil {
				return fmt.Errorf("invalid map key %q: %v", lastSegment, err)
			}
			key.SetUint(u)
		case reflect.Float32, reflect.Float64:
			f, err := strconv.ParseFloat(lastSegment, 64)
			if err != nil {
				return fmt.Errorf("invalid map key %q: %v", lastSegment, err)
			}
			key.SetFloat(f)
		case reflect.Bool:
			b, err := strconv.ParseBool(lastSegment)
			if err != nil {
				return fmt.Errorf("invalid map key %q: %v", lastSegment, err)
			}
			key.SetBool(b)
		default:
			return fmt.Errorf("unsupported map key type: %v", keyType)
		}

		if !parent.MapIndex(key).IsValid() {
			return fmt.Errorf("map key %q not found", lastSegment)
		}

		parent.SetMapIndex(key, reflect.Value{}) // Delete the key

	case reflect.Slice:
		idx, err := strconv.Atoi(lastSegment)
		if err != nil {
			return fmt.Errorf("invalid index %q: %v", lastSegment, err)
		}

		if idx < 0 || idx >= parent.Len() {
			return fmt.Errorf("index %d out of bounds [0:%d]", idx, parent.Len())
		}

		// Create a new slice with the element removed
		newLen := parent.Len() - 1
		newSlice := reflect.MakeSlice(parent.Type(), newLen, parent.Cap())

		// Copy elements before the removed element
		reflect.Copy(newSlice, parent.Slice(0, idx))

		// Copy elements after the removed element
		if idx < newLen {
			reflect.Copy(newSlice.Slice(idx, newLen), parent.Slice(idx+1, parent.Len()))
		}

		parent.Set(newSlice)

	case reflect.Struct:
		field := parent.FieldByName(lastSegment)
		if !field.IsValid() {
			return fmt.Errorf("field %q not found in struct %v", lastSegment, parent.Type())
		}

		internal.DisableRO(&field)

		// Set the field to its zero value
		field.Set(reflect.Zero(field.Type()))

	default:
		return fmt.Errorf("cannot remove from %v", parent.Type())
	}

	return nil
}

// applyReplace implements the "replace" operation.
func applyReplace(target reflect.Value, path string, value interface{}) error {
	// Special case for root path
	if path == "" || path == "/" {
		valueVal := reflect.ValueOf(value)
		if !valueVal.Type().AssignableTo(target.Type()) {
			return fmt.Errorf("cannot assign %v to %v", valueVal.Type(), target.Type())
		}
		target.Set(valueVal)
		return nil
	}

	// Get the parent and last segment
	parent, lastSegment, err := getParentAndLastSegment(target, path)
	if err != nil {
		return err
	}

	valueVal := reflect.ValueOf(value)

	// Handle different container types
	switch parent.Kind() {
	case reflect.Map:
		keyType := parent.Type().Key()
		elemType := parent.Type().Elem()

		if !valueVal.Type().AssignableTo(elemType) {
			return fmt.Errorf("cannot assign %v to %v", valueVal.Type(), elemType)
		}

		key := reflect.New(keyType).Elem()
		switch keyType.Kind() {
		case reflect.String:
			key.SetString(lastSegment)
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			i, err := strconv.ParseInt(lastSegment, 10, 64)
			if err != nil {
				return fmt.Errorf("invalid map key %q: %v", lastSegment, err)
			}
			key.SetInt(i)
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			u, err := strconv.ParseUint(lastSegment, 10, 64)
			if err != nil {
				return fmt.Errorf("invalid map key %q: %v", lastSegment, err)
			}
			key.SetUint(u)
		case reflect.Float32, reflect.Float64:
			f, err := strconv.ParseFloat(lastSegment, 64)
			if err != nil {
				return fmt.Errorf("invalid map key %q: %v", lastSegment, err)
			}
			key.SetFloat(f)
		case reflect.Bool:
			b, err := strconv.ParseBool(lastSegment)
			if err != nil {
				return fmt.Errorf("invalid map key %q: %v", lastSegment, err)
			}
			key.SetBool(b)
		default:
			return fmt.Errorf("unsupported map key type: %v", keyType)
		}

		parent.SetMapIndex(key, valueVal)

	case reflect.Slice, reflect.Array:
		idx, err := strconv.Atoi(lastSegment)
		if err != nil {
			return fmt.Errorf("invalid index %q: %v", lastSegment, err)
		}

		if idx < 0 || idx >= parent.Len() {
			return fmt.Errorf("index %d out of bounds [0:%d]", idx, parent.Len())
		}

		elem := parent.Index(idx)
		if !valueVal.Type().AssignableTo(elem.Type()) {
			return fmt.Errorf("cannot assign %v to %v", valueVal.Type(), elem.Type())
		}

		elem.Set(valueVal)

	case reflect.Struct:
		field := parent.FieldByName(lastSegment)
		if !field.IsValid() {
			return fmt.Errorf("field %q not found in struct %v", lastSegment, parent.Type())
		}

		internal.DisableRO(&field)

		if !valueVal.Type().AssignableTo(field.Type()) {
			return fmt.Errorf("cannot assign %v to %v", valueVal.Type(), field.Type())
		}

		field.Set(valueVal)

	default:
		return fmt.Errorf("cannot replace in %v", parent.Type())
	}

	return nil
}

// applyMove implements the "move" operation.
func applyMove(target reflect.Value, from, to string) error {
	// Get the value at the "from" path
	fromVal, err := getValueAtPath(target, from)
	if err != nil {
		return fmt.Errorf("source path error: %w", err)
	}

	// Store a copy of the value
	valueCopy := reflect.New(fromVal.Type()).Elem()
	valueCopy.Set(fromVal)

	// Remove the value from the "from" path
	if err := applyRemove(target, from); err != nil {
		return fmt.Errorf("removing from source: %w", err)
	}

	// Add the value to the "to" path
	if err := applyAdd(target, to, valueCopy.Interface()); err != nil {
		return fmt.Errorf("adding to destination: %w", err)
	}

	return nil
}

// applyCopy implements the "copy" operation.
func applyCopy(target reflect.Value, from, to string) error {
	// Get the value at the "from" path
	fromVal, err := getValueAtPath(target, from)
	if err != nil {
		return fmt.Errorf("source path error: %w", err)
	}

	// Add the value to the "to" path
	if err := applyAdd(target, to, fromVal.Interface()); err != nil {
		return fmt.Errorf("adding to destination: %w", err)
	}

	return nil
}

// applyTest implements the "test" operation.
func applyTest(target reflect.Value, path string, value interface{}) error {
	valueVal := reflect.ValueOf(value)

	// Special case for root path
	if path == "" || path == "/" {
		if !valueVal.Type().AssignableTo(target.Type()) {
			return fmt.Errorf("cannot compare %v with %v", valueVal.Type(), target.Type())
		}

		if !reflect.DeepEqual(target.Interface(), value) {
			return fmt.Errorf("test failed: values are not equal")
		}
		return nil
	}

	// Get the parent and last segment
	parent, lastSegment, err := getParentAndLastSegment(target, path)
	if err != nil {
		return err
	}

	var current reflect.Value

	// Handle different container types
	switch parent.Kind() {
	case reflect.Map:
		keyType := parent.Type().Key()
		key := reflect.New(keyType).Elem()

		switch keyType.Kind() {
		case reflect.String:
			key.SetString(lastSegment)
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			i, err := strconv.ParseInt(lastSegment, 10, 64)
			if err != nil {
				return fmt.Errorf("invalid map key %q: %v", lastSegment, err)
			}
			key.SetInt(i)
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			u, err := strconv.ParseUint(lastSegment, 10, 64)
			if err != nil {
				return fmt.Errorf("invalid map key %q: %v", lastSegment, err)
			}
			key.SetUint(u)
		case reflect.Float32, reflect.Float64:
			f, err := strconv.ParseFloat(lastSegment, 64)
			if err != nil {
				return fmt.Errorf("invalid map key %q: %v", lastSegment, err)
			}
			key.SetFloat(f)
		case reflect.Bool:
			b, err := strconv.ParseBool(lastSegment)
			if err != nil {
				return fmt.Errorf("invalid map key %q: %v", lastSegment, err)
			}
			key.SetBool(b)
		default:
			return fmt.Errorf("unsupported map key type: %v", keyType)
		}

		current = parent.MapIndex(key)
		if !current.IsValid() {
			return fmt.Errorf("map key %q not found", lastSegment)
		}

	case reflect.Slice, reflect.Array:
		idx, err := strconv.Atoi(lastSegment)
		if err != nil {
			return fmt.Errorf("invalid index %q: %v", lastSegment, err)
		}

		if idx < 0 || idx >= parent.Len() {
			return fmt.Errorf("index %d out of bounds [0:%d]", idx, parent.Len())
		}

		current = parent.Index(idx)

	case reflect.Struct:
		field := parent.FieldByName(lastSegment)
		if !field.IsValid() {
			return fmt.Errorf("field %q not found in struct %v", lastSegment, parent.Type())
		}

		internal.DisableRO(&field)
		current = field

	default:
		return fmt.Errorf("cannot test within %v", parent.Type())
	}

	// Type check
	if !valueVal.Type().AssignableTo(current.Type()) {
		return fmt.Errorf("cannot compare %v with %v", valueVal.Type(), current.Type())
	}

	// Value check
	if !reflect.DeepEqual(current.Interface(), value) {
		return fmt.Errorf("test failed: values are not equal")
	}

	return nil
}
