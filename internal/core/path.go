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

		if part.IsIndex && (current.Kind() == reflect.Slice || current.Kind() == reflect.Array) {
			if part.Index < 0 || part.Index >= current.Len() {
				return reflect.Value{}, PathPart{}, fmt.Errorf("index out of bounds: %d", part.Index)
			}
			current = current.Index(part.Index)
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
	parent, lastPart, err := p.ResolveParent(v)
	if err != nil {
		if string(p) == "" || string(p) == "/" {
			if !v.CanSet() {
				return fmt.Errorf("cannot set root value")
			}
			v.Set(val)
			return nil
		}
		return err
	}

	switch parent.Kind() {
	case reflect.Map:
		keyType := parent.Type().Key()
		var keyVal reflect.Value
		key := lastPart.Key
		if key == "" && lastPart.IsIndex {
			key = strconv.Itoa(lastPart.Index)
		}
		if keyType.Kind() == reflect.String {
			keyVal = reflect.ValueOf(key)
		} else if keyType.Kind() == reflect.Int {
			i, err := strconv.Atoi(key)
			if err != nil {
				return fmt.Errorf("invalid int key: %s", key)
			}
			keyVal = reflect.ValueOf(i)
		}
		parent.SetMapIndex(keyVal, ConvertValue(val, parent.Type().Elem()))
		return nil
	case reflect.Slice:
		idx := lastPart.Index
		if !lastPart.IsIndex {
			var err error
			idx, err = strconv.Atoi(lastPart.Key)
			if err != nil {
				return fmt.Errorf("invalid slice index: %s", lastPart.Key)
			}
		}
		if idx < 0 || idx > parent.Len() {
			return fmt.Errorf("index out of bounds: %d", idx)
		}
		if idx == parent.Len() {
			parent.Set(reflect.Append(parent, ConvertValue(val, parent.Type().Elem())))
		} else {
			parent.Index(idx).Set(ConvertValue(val, parent.Type().Elem()))
		}
		return nil
	case reflect.Struct:
		key := lastPart.Key
		if key == "" && lastPart.IsIndex {
			key = strconv.Itoa(lastPart.Index)
		}
		info := GetTypeInfo(parent.Type())
		var fieldIdx = -1
		for _, fInfo := range info.Fields {
			if fInfo.Name == key || (fInfo.JSONTag != "" && fInfo.JSONTag == key) {
				fieldIdx = fInfo.Index
				break
			}
		}
		if fieldIdx == -1 {
			return fmt.Errorf("field %s not found", key)
		}
		f := parent.Field(fieldIdx)
		if !f.CanSet() {
			unsafe.DisableRO(&f)
		}
		f.Set(ConvertValue(val, f.Type()))
		return nil
	default:
		return fmt.Errorf("cannot set value in %v", parent.Kind())
	}
}

func (p DeepPath) Delete(v reflect.Value) error {
	parent, lastPart, err := p.ResolveParent(v)
	if err != nil {
		return err
	}

	switch parent.Kind() {
	case reflect.Map:
		keyType := parent.Type().Key()
		var keyVal reflect.Value
		key := lastPart.Key
		if key == "" && lastPart.IsIndex {
			key = strconv.Itoa(lastPart.Index)
		}
		if keyType.Kind() == reflect.String {
			keyVal = reflect.ValueOf(key)
		} else if keyType.Kind() == reflect.Int {
			i, err := strconv.Atoi(key)
			if err != nil {
				return fmt.Errorf("invalid int key: %s", key)
			}
			keyVal = reflect.ValueOf(i)
		}
		parent.SetMapIndex(keyVal, reflect.Value{})
		return nil
	case reflect.Slice:
		idx := lastPart.Index
		if !lastPart.IsIndex {
			var err error
			idx, err = strconv.Atoi(lastPart.Key)
			if err != nil {
				return fmt.Errorf("invalid slice index: %s", lastPart.Key)
			}
		}
		if idx < 0 || idx >= parent.Len() {
			return fmt.Errorf("index out of bounds: %d", idx)
		}
		newSlice := reflect.AppendSlice(parent.Slice(0, idx), parent.Slice(idx+1, parent.Len()))
		parent.Set(newSlice)
		return nil
	case reflect.Struct:
		key := lastPart.Key
		if key == "" && lastPart.IsIndex {
			key = strconv.Itoa(lastPart.Index)
		}
		info := GetTypeInfo(parent.Type())
		var fieldIdx = -1
		for _, fInfo := range info.Fields {
			if fInfo.Name == key || (fInfo.JSONTag != "" && fInfo.JSONTag == key) {
				fieldIdx = fInfo.Index
				break
			}
		}
		if fieldIdx == -1 {
			return fmt.Errorf("field %s not found", key)
		}
		f := parent.Field(fieldIdx)
		if !f.CanSet() {
			unsafe.DisableRO(&f)
		}
		f.Set(reflect.Zero(f.Type()))
		return nil
	default:
		return fmt.Errorf("cannot delete from %v", parent.Kind())
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

func (p PathPart) Equals(other PathPart) bool {
	if p.IsIndex != other.IsIndex {
		return false
	}
	if p.IsIndex {
		return p.Index == other.Index
	}
	return p.Key == other.Key
}

func (p DeepPath) StripParts(prefix []PathPart) DeepPath {
	parts := ParsePath(string(p))
	if len(parts) < len(prefix) {
		return p
	}
	for i := range prefix {
		if !parts[i].Equals(prefix[i]) {
			return p
		}
	}
	remaining := parts[len(prefix):]
	if len(remaining) == 0 {
		return ""
	}
	var res strings.Builder
	for _, part := range remaining {
		res.WriteByte('/')
		if part.IsIndex {
			res.WriteString(strconv.Itoa(part.Index))
		} else {
			res.WriteString(EscapeKey(part.Key))
		}
	}
	return DeepPath(res.String())
}

// ParsePath parses a JSON Pointer path.
// It assumes the path starts with "/" or is empty.
func ParsePath(path string) []PathPart {
	if path == "" {
		return nil
	}
	if !strings.HasPrefix(path, "/") {
		return ParseJSONPointer(path)
	}
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

func ToReflectValue(v any) reflect.Value {
	if rv, ok := v.(reflect.Value); ok {
		return rv
	}
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Pointer {
		rv = rv.Elem()
	}
	return rv
}
