package deep

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/brunoga/deep/internal"
	"github.com/brunoga/deep/patch"
)

// Diff creates a patch that, when applied to src, would transform it into dst.
// Returns a patch containing the operations needed to transform src into dst.
func Diff[T any](src, dst T) (patch.Patch[T], error) {
	p := patch.New[T]()

	// Get reflect values
	srcVal := reflect.ValueOf(src)
	dstVal := reflect.ValueOf(dst)

	// Compare the values and build the patch
	if err := diffValues(src, "", srcVal, dstVal, &p); err != nil {
		return nil, fmt.Errorf("deep: error creating diff: %w", err)
	}

	return p, nil
}

// MustDiff creates a patch that, when applied to src, would transform it into dst.
// Panics if an error occurs while creating the diff.
func MustDiff[T any](src, dst T) patch.Patch[T] {
	p, err := Diff(src, dst)
	if err != nil {
		panic(err)
	}
	return p
}

// diffValues recursively compares two reflect.Values and adds operations to the patch
func diffValues[T any](original T, path string, src, dst reflect.Value, p *patch.Patch[T]) error {
	// Handle nil values
	if !src.IsValid() && !dst.IsValid() {
		return nil // Both nil, no difference
	}

	// Check if we're dealing with pointers
	srcIsNil := isNilValue(src)
	dstIsNil := isNilValue(dst)

	// Nil vs. non-nil pointer case
	if srcIsNil && !dstIsNil {
		// Source is nil, destination is not - add the entire value
		(*p) = (*p).Add(path, dst.Interface())
		return nil
	}

	if !srcIsNil && dstIsNil {
		// Source exists, destination is nil - remove the value
		(*p) = (*p).Remove(path)
		return nil
	}

	// Both nil pointers
	if srcIsNil && dstIsNil {
		return nil
	}

	// Unwrap pointers for comparison but maintain information about the original types
	srcOrig := src
	dstOrig := dst
	srcKind := src.Kind()
	dstKind := dst.Kind()

	src = unwrapValue(src)
	dst = unwrapValue(dst)

	// If one was a pointer and one wasn't, or they were different types of pointers
	if (srcKind == reflect.Ptr) != (dstKind == reflect.Ptr) ||
		(srcKind == reflect.Ptr && dstKind == reflect.Ptr && srcOrig.Type() != dstOrig.Type()) {
		// Different pointer types, replace the entire value
		(*p) = (*p).Replace(path, dstOrig.Interface())
		return nil
	}

	// Check if types are compatible for comparison
	if src.Type() != dst.Type() {
		// Different types, replace the entire value
		(*p) = (*p).Replace(path, dstOrig.Interface())
		return nil
	}

	switch src.Kind() {
	case reflect.Struct:
		return diffStructs(original, path, src, dst, p)

	case reflect.Map:
		return diffMaps(original, path, src, dst, p)

	case reflect.Slice, reflect.Array:
		return diffSlices(original, path, src, dst, p)

	case reflect.Interface:
		if src.IsNil() && dst.IsNil() {
			return nil // Both nil interfaces, no difference
		}
		if src.IsNil() || dst.IsNil() || src.Elem().Type() != dst.Elem().Type() {
			// Replace if one is nil or they contain different types
			(*p) = (*p).Replace(path, dstOrig.Interface())
			return nil
		}
		// Compare the values inside the interface
		return diffValues(original, path, src.Elem(), dst.Elem(), p)

	default:
		// For primitive types and other kinds, just compare values
		if !reflect.DeepEqual(src.Interface(), dst.Interface()) {
			(*p) = (*p).Replace(path, dstOrig.Interface())
		}
		return nil
	}
}

// isNilValue checks if a reflect.Value is nil or invalid
func isNilValue(v reflect.Value) bool {
	if !v.IsValid() {
		return true
	}

	switch v.Kind() {
	case reflect.Ptr, reflect.Map, reflect.Slice, reflect.Interface, reflect.Chan, reflect.Func:
		return v.IsNil()
	default:
		return false
	}
}

// diffStructs compares struct fields and adds operations for differences
func diffStructs[T any](original T, path string, src, dst reflect.Value, p *patch.Patch[T]) error {
	for i := 0; i < src.NumField(); i++ {
		field := src.Type().Field(i)

		// Skip unexported fields
		if !field.IsExported() {
			continue
		}

		fieldPath := joinPath(path, field.Name)
		srcField := src.Field(i)
		dstField := dst.Field(i)

		internal.DisableRO(&srcField)
		internal.DisableRO(&dstField)

		if err := diffValues(original, fieldPath, srcField, dstField, p); err != nil {
			return fmt.Errorf("comparing field %s: %w", field.Name, err)
		}
	}
	return nil
}

// diffMaps compares maps and adds operations for differences
func diffMaps[T any](original T, path string, src, dst reflect.Value, p *patch.Patch[T]) error {
	// Handle nil maps
	if src.IsNil() && dst.IsNil() {
		return nil // Both nil, no difference
	}

	if src.IsNil() {
		// Source is nil, destination is not - add the entire map
		(*p) = (*p).Replace(path, dst.Interface())
		return nil
	}

	if dst.IsNil() {
		// Source exists, destination is nil - remove the map
		(*p) = (*p).Remove(path)
		return nil
	}

	// Process keys that are in src
	for _, key := range src.MapKeys() {
		keyStr := formatMapKey(key)
		keyPath := joinPath(path, keyStr)

		dstValue := dst.MapIndex(key)
		if !dstValue.IsValid() {
			// Key exists in src but not in dst, remove it
			(*p) = (*p).Remove(keyPath)
			continue
		}

		// Key exists in both, compare values
		srcValue := src.MapIndex(key)
		if err := diffValues(original, keyPath, srcValue, dstValue, p); err != nil {
			return fmt.Errorf("comparing map key %s: %w", keyStr, err)
		}
	}

	// Process keys that are in dst but not in src
	for _, key := range dst.MapKeys() {
		srcValue := src.MapIndex(key)
		if !srcValue.IsValid() {
			// Key exists in dst but not in src, add it
			keyStr := formatMapKey(key)
			keyPath := joinPath(path, keyStr)
			dstValue := dst.MapIndex(key)
			(*p) = (*p).Add(keyPath, dstValue.Interface())
		}
	}

	return nil
}

// diffSlices compares slices and adds operations for differences
func diffSlices[T any](original T, path string, src, dst reflect.Value, p *patch.Patch[T]) error {
	// Handle nil slices
	if src.IsNil() && dst.IsNil() {
		return nil // Both nil, no difference
	}

	if src.IsNil() {
		// Source is nil, destination is not - add the entire slice
		(*p) = (*p).Replace(path, dst.Interface())
		return nil
	}

	if dst.IsNil() {
		// Source exists, destination is nil - remove the slice
		(*p) = (*p).Remove(path)
		return nil
	}

	srcLen, dstLen := src.Len(), dst.Len()

	// Handle simple cases for small slices
	if srcLen == 0 && dstLen > 0 {
		// Empty to non-empty: replace the whole slice
		(*p) = (*p).Replace(path, dst.Interface())
		return nil
	}

	// Check for simple append case (when prefix matches exactly)
	if srcLen < dstLen && reflect.DeepEqual(
		src.Interface(),
		dst.Slice(0, srcLen).Interface()) {
		// Elements were only added at the end
		for i := srcLen; i < dstLen; i++ {
			(*p) = (*p).Add(joinPath(path, "-"), dst.Index(i).Interface())
		}
		return nil
	}

	// Compare common elements and generate specific operations
	minLen := srcLen
	if dstLen < minLen {
		minLen = dstLen
	}

	// Compare common elements
	for i := 0; i < minLen; i++ {
		elemPath := joinPath(path, strconv.Itoa(i))
		srcElem := src.Index(i)
		dstElem := dst.Index(i)

		// Skip if they're equal
		if reflect.DeepEqual(srcElem.Interface(), dstElem.Interface()) {
			continue
		}

		// Generate replace operation for this element
		if err := diffValues(original, elemPath, srcElem, dstElem, p); err != nil {
			return fmt.Errorf("comparing slice element %d: %w", i, err)
		}
	}

	// Handle length differences
	if srcLen > dstLen {
		// Remove extra elements, starting from the end to avoid index shifting
		for i := srcLen - 1; i >= dstLen; i-- {
			elemPath := joinPath(path, strconv.Itoa(i))
			(*p) = (*p).Remove(elemPath)
		}
	} else if dstLen > srcLen {
		// Add missing elements
		for i := srcLen; i < dstLen; i++ {
			elemPath := joinPath(path, "-") // Use append notation for new elements
			(*p) = (*p).Add(elemPath, dst.Index(i).Interface())
		}
	}

	return nil
}

// unwrapValue unwraps pointer values to their element type
func unwrapValue(v reflect.Value) reflect.Value {
	for v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return v // Don't dereference nil pointers
		}
		v = v.Elem()
	}
	return v
}

// joinPath joins path segments with proper escaping
func joinPath(base, segment string) string {
	if base == "" {
		return "/" + escapePathSegment(segment)
	}
	return base + "/" + escapePathSegment(segment)
}

// escapePathSegment applies JSON Pointer escaping to a path segment
func escapePathSegment(segment string) string {
	// Replace ~ with ~0 and / with ~1 per RFC 6901
	segment = strings.ReplaceAll(segment, "~", "~0")
	segment = strings.ReplaceAll(segment, "/", "~1")
	return segment
}

// formatMapKey formats a map key as a string for use in a JSON Pointer
func formatMapKey(key reflect.Value) string {
	switch key.Kind() {
	case reflect.String:
		return key.String()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(key.Int(), 10)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(key.Uint(), 10)
	case reflect.Float32, reflect.Float64:
		return strconv.FormatFloat(key.Float(), 'g', -1, 64)
	case reflect.Bool:
		return strconv.FormatBool(key.Bool())
	default:
		// For complex keys, use string representation
		return fmt.Sprintf("%v", key.Interface())
	}
}
