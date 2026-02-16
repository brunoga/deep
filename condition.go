package deep

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/brunoga/deep/v3/internal/unsafe"
)

// Condition represents a logical check against a value of type T.
type Condition[T any] interface {
	// Evaluate evaluates the condition against the given value.
	Evaluate(v *T) (bool, error)

	// MarshalJSON returns the JSON representation of the condition.
	MarshalJSON() ([]byte, error)

	internalCondition
}

// internalCondition is an internal interface for efficient evaluation without reflection.
type internalCondition interface {
	evaluateAny(v any) (bool, error)
}

// path represents a path to a field or element within a structure.
// Syntax: "Field", "Field.SubField", "Slice[0]", "Map.Key", "Ptr.Field".
// It also supports JSON Pointers (RFC 6901) like "/Field/SubField".
type deepPath string

// resolve traverses v using the path and returns the reflect.Value found.
func (p deepPath) resolve(v reflect.Value) (reflect.Value, error) {
	parts := parsePath(string(p))
	val, _, err := p.Navigate(v, parts)
	return val, err
}

func (p deepPath) resolveParent(v reflect.Value) (reflect.Value, pathPart, error) {
	parts := parsePath(string(p))
	if len(parts) == 0 {
		return reflect.Value{}, pathPart{}, fmt.Errorf("path is empty")
	}
	parent, _, err := p.Navigate(v, parts[:len(parts)-1])
	if err != nil {
		return reflect.Value{}, pathPart{}, err
	}
	return parent, parts[len(parts)-1], nil
}

func (p deepPath) Navigate(v reflect.Value, parts []pathPart) (reflect.Value, pathPart, error) {
	current, err := dereference(v)
	if err != nil {
		return reflect.Value{}, pathPart{}, err
	}

	for _, part := range parts {
		if !current.IsValid() {
			return reflect.Value{}, pathPart{}, fmt.Errorf("path traversal failed: nil value at intermediate step")
		}

		if part.isIndex && (current.Kind() == reflect.Slice || current.Kind() == reflect.Array) {
			if part.index < 0 || part.index >= current.Len() {
				return reflect.Value{}, pathPart{}, fmt.Errorf("index out of bounds: %d", part.index)
			}
			current = current.Index(part.index)
		} else if current.Kind() == reflect.Map {
			keyType := current.Type().Key()
			var keyVal reflect.Value
			key := part.key
			if key == "" && part.isIndex {
				key = strconv.Itoa(part.index)
			}
			if keyType.Kind() == reflect.String {
				keyVal = reflect.ValueOf(key)
			} else if keyType.Kind() == reflect.Int {
				i, err := strconv.Atoi(key)
				if err != nil {
					return reflect.Value{}, pathPart{}, fmt.Errorf("invalid int key: %s", key)
				}
				keyVal = reflect.ValueOf(i)
			} else {
				return reflect.Value{}, pathPart{}, fmt.Errorf("unsupported map key type for path: %v", keyType)
			}

			val := current.MapIndex(keyVal)
			if !val.IsValid() {
				return reflect.Value{}, pathPart{}, nil
			}
			current = val
		} else {
			if current.Kind() != reflect.Struct {
				return reflect.Value{}, pathPart{}, fmt.Errorf("cannot access field %s on %v", part.key, current.Type())
			}

			key := part.key
			if key == "" && part.isIndex {
				key = strconv.Itoa(part.index)
			}

			f := current.FieldByName(key)
			if !f.IsValid() {
				return reflect.Value{}, pathPart{}, fmt.Errorf("field %s not found", key)
			}
			if !f.CanInterface() {
				unsafe.DisableRO(&f)
			}
			current = f
		}

		current, err = dereference(current)
		if err != nil {
			return reflect.Value{}, pathPart{}, err
		}
		if len(parts) > 0 && part == parts[len(parts)-1] {
			return current, part, nil
		}
	}
	return current, pathPart{}, nil
}

func (p deepPath) set(v reflect.Value, val reflect.Value) error {
	parent, lastPart, err := p.resolveParent(v)
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
		key := lastPart.key
		if key == "" && lastPart.isIndex {
			key = strconv.Itoa(lastPart.index)
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
		parent.SetMapIndex(keyVal, convertValue(val, parent.Type().Elem()))
		return nil
	case reflect.Slice:
		idx := lastPart.index
		if !lastPart.isIndex {
			var err error
			idx, err = strconv.Atoi(lastPart.key)
			if err != nil {
				return fmt.Errorf("invalid slice index: %s", lastPart.key)
			}
		}
		if idx < 0 || idx > parent.Len() {
			return fmt.Errorf("index out of bounds: %d", idx)
		}
		if idx == parent.Len() {
			parent.Set(reflect.Append(parent, convertValue(val, parent.Type().Elem())))
		} else {
			parent.Index(idx).Set(convertValue(val, parent.Type().Elem()))
		}
		return nil
	case reflect.Struct:
		key := lastPart.key
		if key == "" && lastPart.isIndex {
			key = strconv.Itoa(lastPart.index)
		}
		f := parent.FieldByName(key)
		if !f.IsValid() {
			return fmt.Errorf("field %s not found", key)
		}
		if !f.CanSet() {
			unsafe.DisableRO(&f)
		}
		f.Set(convertValue(val, f.Type()))
		return nil
	default:
		return fmt.Errorf("cannot set value in %v", parent.Kind())
	}
}

func (p deepPath) delete(v reflect.Value) error {
	parent, lastPart, err := p.resolveParent(v)
	if err != nil {
		return err
	}

	switch parent.Kind() {
	case reflect.Map:
		keyType := parent.Type().Key()
		var keyVal reflect.Value
		key := lastPart.key
		if key == "" && lastPart.isIndex {
			key = strconv.Itoa(lastPart.index)
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
		idx := lastPart.index
		if !lastPart.isIndex {
			var err error
			idx, err = strconv.Atoi(lastPart.key)
			if err != nil {
				return fmt.Errorf("invalid slice index: %s", lastPart.key)
			}
		}
		if idx < 0 || idx >= parent.Len() {
			return fmt.Errorf("index out of bounds: %d", idx)
		}
		newSlice := reflect.AppendSlice(parent.Slice(0, idx), parent.Slice(idx+1, parent.Len()))
		parent.Set(newSlice)
		return nil
	case reflect.Struct:
		key := lastPart.key
		if key == "" && lastPart.isIndex {
			key = strconv.Itoa(lastPart.index)
		}
		f := parent.FieldByName(key)
		if !f.IsValid() {
			return fmt.Errorf("field %s not found", key)
		}
		if !f.CanSet() {
			unsafe.DisableRO(&f)
		}
		f.Set(reflect.Zero(f.Type()))
		return nil
	default:
		return fmt.Errorf("cannot delete from %v", parent.Kind())
	}
}

func dereference(v reflect.Value) (reflect.Value, error) {
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return reflect.Value{}, fmt.Errorf("path traversal failed: nil pointer/interface")
		}
		v = v.Elem()
	}
	return v, nil
}

func compareValues(v1, v2 reflect.Value, op string, ignoreCase bool) (bool, error) {
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

	v2 = convertValue(v2, v1.Type())

	if op == "==" {
		if ignoreCase && v1.Kind() == reflect.String && v2.Kind() == reflect.String {
			return strings.EqualFold(v1.String(), v2.String()), nil
		}
		return Equal(v1.Interface(), v2.Interface()), nil
	}
	if op == "!=" {
		if ignoreCase && v1.Kind() == reflect.String && v2.Kind() == reflect.String {
			return !strings.EqualFold(v1.String(), v2.String()), nil
		}
		return !Equal(v1.Interface(), v2.Interface()), nil
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

type pathPart struct {
	key     string
	index   int
	isIndex bool
}

func (p pathPart) equals(other pathPart) bool {
	if p.isIndex != other.isIndex {
		return false
	}
	if p.isIndex {
		return p.index == other.index
	}
	return p.key == other.key
}

func (p deepPath) stripParts(prefix []pathPart) deepPath {
	parts := parsePath(string(p))
	if len(parts) < len(prefix) {
		return p
	}
	for i := range prefix {
		if !parts[i].equals(prefix[i]) {
			return p
		}
	}
	remaining := parts[len(prefix):]
	if len(remaining) == 0 {
		return ""
	}
	var res strings.Builder
	for i, part := range remaining {
		if part.isIndex {
			res.WriteByte('[')
			res.WriteString(strconv.Itoa(part.index))
			res.WriteByte(']')
		} else {
			if i > 0 {
				res.WriteByte('.')
			}
			res.WriteString(part.key)
		}
	}
	return deepPath(res.String())
}

func parsePath(path string) []pathPart {
	if strings.HasPrefix(path, "/") {
		return parseJSONPointer(path)
	}
	var parts []pathPart
	var buf strings.Builder
	flush := func() {
		if buf.Len() > 0 {
			parts = append(parts, pathPart{key: buf.String()})
			buf.Reset()
		}
	}
	for i := 0; i < len(path); i++ {
		c := path[i]
		switch c {
		case '.':
			flush()
		case '[':
			flush()
			start := i + 1
			for i < len(path) && path[i] != ']' {
				i++
			}
			if i < len(path) {
				content := path[start:i]
				idx, err := strconv.Atoi(content)
				if err == nil {
					parts = append(parts, pathPart{index: idx, isIndex: true})
				} else {
					parts = append(parts, pathPart{key: content})
				}
			}
		default:
			buf.WriteByte(c)
		}
	}
	flush()
	return parts
}

func parseJSONPointer(path string) []pathPart {
	if path == "/" {
		return nil
	}
	tokens := strings.Split(path, "/")[1:]
	parts := make([]pathPart, len(tokens))
	for i, token := range tokens {
		token = strings.ReplaceAll(token, "~1", "/")
		token = strings.ReplaceAll(token, "~0", "~")
		if idx, err := strconv.Atoi(token); err == nil && idx >= 0 {
			parts[i] = pathPart{key: token, index: idx, isIndex: true}
		} else {
			parts[i] = pathPart{key: token}
		}
	}
	return parts
}

// normalizePath converts a dot-notation or JSON Pointer path to a standard JSON Pointer.
func normalizePath(path string) string {
	if path == "" || path == "/" {
		return "/"
	}
	parts := parsePath(path)
	var b strings.Builder
	for _, p := range parts {
		b.WriteByte('/')
		if p.isIndex {
			b.WriteString(strconv.Itoa(p.index))
		} else {
			// Escape JSON Pointer tokens
			key := strings.ReplaceAll(p.key, "~", "~0")
			key = strings.ReplaceAll(key, "/", "~1")
			b.WriteString(key)
		}
	}
	return b.String()
}

// joinPath joins two JSON Pointer paths with a slash.
func joinPath(parent, child string) string {
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

func toReflectValue(v any) reflect.Value {
	if rv, ok := v.(reflect.Value); ok {
		return rv
	}
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Pointer {
		rv = rv.Elem()
	}
	return rv
}
