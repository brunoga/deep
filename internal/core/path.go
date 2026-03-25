package core

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/brunoga/deep/v5/internal/unsafe"
)

// DeepPath represents a path to a field or element within a structure.
// It supports JSON Pointers (RFC 6901) syntax like "/Field/SubField".
type DeepPath string

// Resolve traverses v using the path and returns the reflect.Value found.
func (p DeepPath) Resolve(v reflect.Value) (reflect.Value, error) {
	parts := ParsePath(string(p))
	val, _, err := p.Navigate(v, parts)
	return val, err
}

// ResolveParentPath splits the path into parent path and the last part.
func (p DeepPath) ResolveParentPath() (DeepPath, PathPart, error) {
	parts := ParsePath(string(p))
	if len(parts) == 0 {
		return "", PathPart{}, fmt.Errorf("path is empty")
	}
	last := parts[len(parts)-1]

	if len(parts) == 1 {
		return "", last, nil
	}

	parentParts := parts[:len(parts)-1]
	var b strings.Builder
	for _, part := range parentParts {
		b.WriteByte('/')
		if part.IsIndex {
			b.WriteString(strconv.Itoa(part.Index))
		} else {
			b.WriteString(EscapeKey(part.Key))
		}
	}
	return DeepPath(b.String()), last, nil
}

func (p DeepPath) ResolveParent(v reflect.Value) (reflect.Value, PathPart, error) {
	parts := ParsePath(string(p))
	if len(parts) == 0 {
		return reflect.Value{}, PathPart{}, fmt.Errorf("path is empty")
	}
	parent, _, err := p.Navigate(v, parts[:len(parts)-1])
	if err != nil {
		return reflect.Value{}, PathPart{}, err
	}
	return parent, parts[len(parts)-1], nil
}

func (p DeepPath) Navigate(v reflect.Value, parts []PathPart) (reflect.Value, PathPart, error) {
	current, err := Dereference(v)
	if err != nil {
		return reflect.Value{}, PathPart{}, err
	}

	for _, part := range parts {
		if !current.IsValid() {
			return reflect.Value{}, PathPart{}, fmt.Errorf("path traversal failed: nil value at intermediate step")
		}

		if current.Kind() == reflect.Slice || current.Kind() == reflect.Array {
			if current.Kind() == reflect.Slice {
				// Check for keyed-collection tag first, regardless of whether the
				// path segment is numeric. Keys like "todo" or "in-progress" are
				// non-numeric but still valid keyed-slice selectors.
				if keyIdx, found := sliceKeyField(current.Type()); found {
					keyStr := part.Key
					if keyStr == "" && part.IsIndex {
						keyStr = strconv.Itoa(part.Index)
					}
					elem, ok := findSliceElemByKey(current, keyIdx, keyStr)
					if !ok {
						return reflect.Value{}, PathPart{}, fmt.Errorf("element with key %s not found", keyStr)
					}
					current = elem
				} else if part.IsIndex {
					if part.Index < 0 || part.Index >= current.Len() {
						return reflect.Value{}, PathPart{}, fmt.Errorf("index out of bounds: %d", part.Index)
					}
					current = current.Index(part.Index)
				} else {
					return reflect.Value{}, PathPart{}, fmt.Errorf("non-numeric index %q for non-keyed slice", part.Key)
				}
			} else {
				// Array: always numeric.
				if !part.IsIndex {
					return reflect.Value{}, PathPart{}, fmt.Errorf("non-numeric index %q for array", part.Key)
				}
				if part.Index < 0 || part.Index >= current.Len() {
					return reflect.Value{}, PathPart{}, fmt.Errorf("index out of bounds: %d", part.Index)
				}
				current = current.Index(part.Index)
			}
		} else if current.Kind() == reflect.Map {
			keyType := current.Type().Key()
			var keyVal reflect.Value
			key := part.Key
			if key == "" && part.IsIndex {
				key = strconv.Itoa(part.Index)
			}
			if keyType.Kind() == reflect.String {
				keyVal = reflect.ValueOf(key)
			} else if keyType.Kind() == reflect.Int {
				i, err := strconv.Atoi(key)
				if err != nil {
					return reflect.Value{}, PathPart{}, fmt.Errorf("invalid int key: %s", key)
				}
				keyVal = reflect.ValueOf(i)
			} else {
				return reflect.Value{}, PathPart{}, fmt.Errorf("unsupported map key type for path: %v", keyType)
			}

			val := current.MapIndex(keyVal)
			if !val.IsValid() {
				return reflect.Value{}, PathPart{}, nil
			}
			current = val
		} else {
			if current.Kind() != reflect.Struct {
				return reflect.Value{}, PathPart{}, fmt.Errorf("cannot access field %s on %v", part.Key, current.Type())
			}

			key := part.Key
			if key == "" && part.IsIndex {
				key = strconv.Itoa(part.Index)
			}

			info := GetTypeInfo(current.Type())
			var fieldIdx = -1
			for _, fInfo := range info.Fields {
				if fInfo.Name == key || (fInfo.JSONTag != "" && fInfo.JSONTag == key) {
					fieldIdx = fInfo.Index
					break
				}
			}

			if fieldIdx == -1 {
				return reflect.Value{}, PathPart{}, fmt.Errorf("field %s not found", key)
			}
			f := current.Field(fieldIdx)

			if !f.CanInterface() {
				unsafe.DisableRO(&f)
			}
			current = f
		}

		current, err = Dereference(current)
		if err != nil {
			return reflect.Value{}, PathPart{}, err
		}
	}
	return current, PathPart{}, nil
}

func (p DeepPath) Set(v reflect.Value, val reflect.Value) error {
	if string(p) == "" || string(p) == "/" {
		if !v.CanSet() {
			return fmt.Errorf("cannot set root value")
		}
		SetValue(v, val)
		return nil
	}
	return setAtPath(v, ParsePath(string(p)), val)
}

// setAtPath recursively walks parts and sets val at the target location.
// It handles map boundaries with copy-modify-put-back so that values nested
// inside maps remain correct even though map elements are not addressable.
func setAtPath(v reflect.Value, parts []PathPart, val reflect.Value) error {
	v, err := Dereference(v)
	if err != nil {
		return err
	}

	if len(parts) == 0 {
		if !v.CanSet() {
			unsafe.DisableRO(&v)
		}
		SetValue(v, val)
		return nil
	}

	part := parts[0]
	rest := parts[1:]

	switch v.Kind() {
	case reflect.Map:
		keyVal, err := makeMapKey(v.Type().Key(), part)
		if err != nil {
			return err
		}
		if len(rest) == 0 {
			v.SetMapIndex(keyVal, ConvertValue(val, v.Type().Elem()))
			return nil
		}
		// Deeper path: copy the map element, recurse, put it back.
		elem := v.MapIndex(keyVal)
		if !elem.IsValid() {
			return fmt.Errorf("map key %v not found", part.Key)
		}
		newElem := reflect.New(elem.Type()).Elem()
		newElem.Set(elem)
		if err := setAtPath(newElem, rest, val); err != nil {
			return err
		}
		v.SetMapIndex(keyVal, newElem)
		return nil

	case reflect.Slice:
		if keyIdx, found := sliceKeyField(v.Type()); found {
			// Keyed slice: key lookup works for both numeric and non-numeric keys.
			keyStr := part.Key
			if keyStr == "" && part.IsIndex {
				keyStr = strconv.Itoa(part.Index)
			}
			converted := ConvertValue(val, v.Type().Elem())
			if len(rest) == 0 {
				for i := 0; i < v.Len(); i++ {
					if keyFieldStr(v.Index(i), keyIdx) == keyStr {
						v.Index(i).Set(converted)
						return nil
					}
				}
				// Key not found: append.
				if !v.CanSet() {
					return fmt.Errorf("cannot append to non-settable keyed slice at key %s", keyStr)
				}
				v.Set(reflect.Append(v, converted))
				return nil
			}
			// Deeper: recurse into the keyed element (slice elements are addressable).
			for i := 0; i < v.Len(); i++ {
				if keyFieldStr(v.Index(i), keyIdx) == keyStr {
					return setAtPath(v.Index(i), rest, val)
				}
			}
			return fmt.Errorf("element with key %s not found", keyStr)
		}
		// Plain slice: positional index.
		idx := part.Index
		if !part.IsIndex {
			idx, err = strconv.Atoi(part.Key)
			if err != nil {
				return fmt.Errorf("invalid slice index: %s", part.Key)
			}
		}
		if idx < 0 || idx > v.Len() {
			return fmt.Errorf("index out of bounds: %d", idx)
		}
		if len(rest) == 0 {
			if idx == v.Len() {
				if !v.CanSet() {
					return fmt.Errorf("cannot append to non-settable slice at index %d", idx)
				}
				v.Set(reflect.Append(v, ConvertValue(val, v.Type().Elem())))
			} else {
				v.Index(idx).Set(ConvertValue(val, v.Type().Elem()))
			}
			return nil
		}
		if idx >= v.Len() {
			return fmt.Errorf("index out of bounds: %d", idx)
		}
		return setAtPath(v.Index(idx), rest, val)

	case reflect.Struct:
		key := part.Key
		if key == "" && part.IsIndex {
			key = strconv.Itoa(part.Index)
		}
		info := GetTypeInfo(v.Type())
		for _, fInfo := range info.Fields {
			if fInfo.Name == key || (fInfo.JSONTag != "" && fInfo.JSONTag == key) {
				f := v.Field(fInfo.Index)
				if !f.CanInterface() {
					unsafe.DisableRO(&f)
				}
				if len(rest) == 0 {
					if !f.CanSet() {
						unsafe.DisableRO(&f)
					}
					f.Set(ConvertValue(val, f.Type()))
					return nil
				}
				return setAtPath(f, rest, val)
			}
		}
		return fmt.Errorf("field %s not found", key)

	default:
		return fmt.Errorf("cannot navigate into %v", v.Kind())
	}
}

// makeMapKey converts a PathPart into a reflect.Value suitable as a map key.
func makeMapKey(keyType reflect.Type, part PathPart) (reflect.Value, error) {
	key := part.Key
	if key == "" && part.IsIndex {
		key = strconv.Itoa(part.Index)
	}
	switch keyType.Kind() {
	case reflect.String:
		return reflect.ValueOf(key).Convert(keyType), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		i, err := strconv.ParseInt(key, 10, 64)
		if err != nil {
			return reflect.Value{}, fmt.Errorf("invalid int key: %s", key)
		}
		return reflect.ValueOf(i).Convert(keyType), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		u, err := strconv.ParseUint(key, 10, 64)
		if err != nil {
			return reflect.Value{}, fmt.Errorf("invalid uint key: %s", key)
		}
		return reflect.ValueOf(u).Convert(keyType), nil
	default:
		return reflect.Value{}, fmt.Errorf("unsupported map key type for path: %v", keyType)
	}
}

func (p DeepPath) Delete(v reflect.Value) error {
	return deleteAtPath(v, ParsePath(string(p)))
}

// deleteAtPath recursively walks parts and removes the value at the target location.
// Like setAtPath it uses copy-modify-put-back at map boundaries so that values
// nested inside maps can be deleted without hitting addressability panics.
func deleteAtPath(v reflect.Value, parts []PathPart) error {
	v, err := Dereference(v)
	if err != nil {
		return err
	}

	if len(parts) == 0 {
		return fmt.Errorf("cannot delete: empty path")
	}

	part := parts[0]
	rest := parts[1:]

	switch v.Kind() {
	case reflect.Map:
		keyVal, err := makeMapKey(v.Type().Key(), part)
		if err != nil {
			return err
		}
		if len(rest) == 0 {
			v.SetMapIndex(keyVal, reflect.Value{})
			return nil
		}
		// Deeper: copy-modify-put-back.
		elem := v.MapIndex(keyVal)
		if !elem.IsValid() {
			return fmt.Errorf("map key %v not found", part.Key)
		}
		newElem := reflect.New(elem.Type()).Elem()
		newElem.Set(elem)
		if err := deleteAtPath(newElem, rest); err != nil {
			return err
		}
		v.SetMapIndex(keyVal, newElem)
		return nil

	case reflect.Slice:
		if keyIdx, found := sliceKeyField(v.Type()); found {
			// Keyed slice.
			keyStr := part.Key
			if keyStr == "" && part.IsIndex {
				keyStr = strconv.Itoa(part.Index)
			}
			if len(rest) == 0 {
				for i := 0; i < v.Len(); i++ {
					if keyFieldStr(v.Index(i), keyIdx) == keyStr {
						newSlice := reflect.AppendSlice(v.Slice(0, i), v.Slice(i+1, v.Len()))
						if !v.CanSet() {
							return fmt.Errorf("cannot delete from non-settable keyed slice at key %s", keyStr)
						}
						v.Set(newSlice)
						return nil
					}
				}
				return fmt.Errorf("element with key %s not found", keyStr)
			}
			// Deeper: recurse into element (slice elements are addressable).
			for i := 0; i < v.Len(); i++ {
				if keyFieldStr(v.Index(i), keyIdx) == keyStr {
					return deleteAtPath(v.Index(i), rest)
				}
			}
			return fmt.Errorf("element with key %s not found", keyStr)
		}
		// Plain slice: positional index.
		idx := part.Index
		if !part.IsIndex {
			idx, err = strconv.Atoi(part.Key)
			if err != nil {
				return fmt.Errorf("invalid slice index: %s", part.Key)
			}
		}
		if idx < 0 || idx >= v.Len() {
			return fmt.Errorf("index out of bounds: %d", idx)
		}
		if len(rest) == 0 {
			newSlice := reflect.AppendSlice(v.Slice(0, idx), v.Slice(idx+1, v.Len()))
			if !v.CanSet() {
				return fmt.Errorf("cannot delete from non-settable slice at index %d", idx)
			}
			v.Set(newSlice)
			return nil
		}
		return deleteAtPath(v.Index(idx), rest)

	case reflect.Struct:
		key := part.Key
		if key == "" && part.IsIndex {
			key = strconv.Itoa(part.Index)
		}
		info := GetTypeInfo(v.Type())
		for _, fInfo := range info.Fields {
			if fInfo.Name == key || (fInfo.JSONTag != "" && fInfo.JSONTag == key) {
				f := v.Field(fInfo.Index)
				if !f.CanInterface() {
					unsafe.DisableRO(&f)
				}
				if len(rest) == 0 {
					if !f.CanSet() {
						unsafe.DisableRO(&f)
					}
					f.Set(reflect.Zero(f.Type()))
					return nil
				}
				return deleteAtPath(f, rest)
			}
		}
		return fmt.Errorf("field %s not found", key)

	default:
		return fmt.Errorf("cannot delete from %v", v.Kind())
	}
}

func Dereference(v reflect.Value) (reflect.Value, error) {
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return reflect.Value{}, fmt.Errorf("path traversal failed: nil pointer/interface")
		}
		v = v.Elem()
	}
	return v, nil
}

func CompareValues(v1, v2 reflect.Value, op string, ignoreCase bool) (bool, error) {
	if !v1.IsValid() || !v2.IsValid() {
		switch op {
		case "==":
			return !v1.IsValid() && !v2.IsValid(), nil
		case "!=":
			return v1.IsValid() != v2.IsValid(), nil
		default:
			return false, nil
		}
	}

	v2 = ConvertValue(v2, v1.Type())

	if op == "==" {
		if ignoreCase && v1.Kind() == reflect.String && v2.Kind() == reflect.String {
			return strings.EqualFold(v1.String(), v2.String()), nil
		}
		return ValueEqual(v1, v2, nil), nil
	}
	if op == "!=" {
		if ignoreCase && v1.Kind() == reflect.String && v2.Kind() == reflect.String {
			return !strings.EqualFold(v1.String(), v2.String()), nil
		}
		return !ValueEqual(v1, v2, nil), nil
	}

	if v1.Kind() != v2.Kind() {
		return false, fmt.Errorf("type mismatch: %v and %v", v1.Type(), v2.Type())
	}

	switch v1.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return compareOrdered(v1.Int(), v2.Int(), op)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return compareOrdered(v1.Uint(), v2.Uint(), op)
	case reflect.Float32, reflect.Float64:
		return compareOrdered(v1.Float(), v2.Float(), op)
	case reflect.String:
		return compareOrdered(v1.String(), v2.String(), op)
	}
	return false, fmt.Errorf("unsupported comparison %s for kind %v", op, v1.Kind())
}

func compareOrdered[T int64 | uint64 | float64 | string](a, b T, op string) (bool, error) {
	switch op {
	case ">":
		return a > b, nil
	case "<":
		return a < b, nil
	case ">=":
		return a >= b, nil
	case "<=":
		return a <= b, nil
	default:
		return false, fmt.Errorf("unsupported operator: %s", op)
	}
}

type PathPart struct {
	Key     string
	Index   int
	IsIndex bool
}

// ParsePath parses a JSON Pointer path (RFC 6901).
func ParsePath(path string) []PathPart {
	return ParseJSONPointer(path)
}

func ParseJSONPointer(path string) []PathPart {
	if path == "" || path == "/" {
		return nil
	}

	// Handle paths not starting with / (treat as relative/simple key)
	var tokens []string
	if strings.HasPrefix(path, "/") {
		tokens = strings.Split(path, "/")[1:]
	} else {
		tokens = strings.Split(path, "/")
	}

	parts := make([]PathPart, len(tokens))
	for i, token := range tokens {
		token = strings.ReplaceAll(token, "~1", "/")
		token = strings.ReplaceAll(token, "~0", "~")
		if idx, err := strconv.Atoi(token); err == nil && idx >= 0 {
			parts[i] = PathPart{Key: token, Index: idx, IsIndex: true}
		} else {
			parts[i] = PathPart{Key: token}
		}
	}
	return parts
}

// NormalizePath converts a dot-notation or JSON Pointer path to a standard JSON Pointer.
func NormalizePath(path string) string {
	if path == "" || path == "/" {
		return "/"
	}
	parts := ParsePath(path)
	var b strings.Builder
	for _, p := range parts {
		b.WriteByte('/')
		if p.IsIndex {
			b.WriteString(strconv.Itoa(p.Index))
		} else {
			b.WriteString(EscapeKey(p.Key))
		}
	}
	return b.String()
}

func EscapeKey(key string) string {
	key = strings.ReplaceAll(key, "~", "~0")
	key = strings.ReplaceAll(key, "/", "~1")
	return key
}

// JoinPath joins two JSON Pointer paths with a slash.
func JoinPath(parent, child string) string {
	if parent == "" || parent == "/" {
		if child == "" || child == "/" {
			return "/"
		}
		if child[0] == '/' {
			return child
		}
		return "/" + child
	}
	if child == "" || child == "/" {
		return parent
	}
	res := parent
	if !strings.HasSuffix(res, "/") {
		res += "/"
	}
	if child[0] == '/' {
		res += child[1:]
	} else {
		res += child
	}
	return res
}

// sliceKeyField returns the index of the deep:"key" field on the element type of
// a slice type, together with a found flag. Returns -1, false for non-keyed slices.
func sliceKeyField(sliceType reflect.Type) (int, bool) {
	elemType := sliceType.Elem()
	for elemType.Kind() == reflect.Pointer {
		elemType = elemType.Elem()
	}
	return GetKeyField(elemType)
}

// keyFieldStr returns the string representation of the key field at fieldIdx in elem.
func keyFieldStr(elem reflect.Value, fieldIdx int) string {
	for elem.Kind() == reflect.Pointer {
		if elem.IsNil() {
			return ""
		}
		elem = elem.Elem()
	}
	f := elem.Field(fieldIdx)
	return fmt.Sprintf("%v", f.Interface())
}

// findSliceElemByKey searches s for the element whose key field equals keyStr,
// returning the element value and true on success.
func findSliceElemByKey(s reflect.Value, keyIdx int, keyStr string) (reflect.Value, bool) {
	for i := 0; i < s.Len(); i++ {
		if keyFieldStr(s.Index(i), keyIdx) == keyStr {
			return s.Index(i), true
		}
	}
	return reflect.Value{}, false
}
