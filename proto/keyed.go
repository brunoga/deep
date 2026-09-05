package deepproto

import (
	"fmt"
	"sync"

	deep "github.com/brunoga/deep/v6"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// Keyed repeated fields: the proto counterpart of the core library's
// `deep:"key"` tag.
//
// An unkeyed repeated field diffs positionally — reordering is a change, and a
// removal from the middle replaces the whole list. For a repeated message
// field whose elements have an identity, registering that identity makes the
// diff order-insensitive and element-addressed: elements are matched by key,
// paths name elements by key rather than index, and an element that moved is
// no change at all.
//
// Registration is by the field's full protobuf name, which pins it to one
// field of one message rather than to every list that happens to hold that
// element type:
//
//	deepproto.RegisterListKey("shop.Catalog.items", "id")

var (
	listKeyMu sync.RWMutex
	listKeys  = map[protoreflect.FullName]string{}
)

// RegisterListKey declares that the repeated message field fieldFullName —
// "package.Message.field" — is keyed by the element field keyField, named by
// its protojson name. Register during initialisation, alongside [Register].
func RegisterListKey(fieldFullName, keyField string) {
	listKeyMu.Lock()
	defer listKeyMu.Unlock()
	listKeys[protoreflect.FullName(fieldFullName)] = keyField
}

// listKeyFor returns the key field descriptor for a keyed repeated field, if
// one is registered and names a real field of the element.
func listKeyFor(fd protoreflect.FieldDescriptor) (protoreflect.FieldDescriptor, bool) {
	if fd.Message() == nil {
		return nil, false
	}
	listKeyMu.RLock()
	name, ok := listKeys[fd.FullName()]
	listKeyMu.RUnlock()
	if !ok {
		return nil, false
	}
	kd := fieldByName(fd.Message(), name)
	return kd, kd != nil
}

// elementKey renders an element's key the way it appears in a path.
func elementKey(kd protoreflect.FieldDescriptor, elem protoreflect.Message) string {
	return elem.Get(kd).String()
}

// diffKeyedList matches elements by key: a key on one side only is an add or
// a remove of that element, a key on both sides diffs field by field under
// the key's path, and order carries no meaning at all.
func diffKeyedList(path string, fd, kd protoreflect.FieldDescriptor, la, lb protoreflect.List, ops *[]deep.Operation) error {
	inB := make(map[string]int, lb.Len())
	for i := 0; i < lb.Len(); i++ {
		inB[elementKey(kd, lb.Get(i).Message())] = i
	}

	inA := make(map[string]int, la.Len())
	for i := 0; i < la.Len(); i++ {
		key := elementKey(kd, la.Get(i).Message())
		inA[key] = i
		if _, ok := inB[key]; !ok {
			*ops = append(*ops, deep.Operation{
				Kind: deep.OpRemove,
				Path: path + "/" + deep.EscapePathKey(key),
				Old:  la.Get(i).Message().Interface(),
			})
		}
	}

	for i := 0; i < lb.Len(); i++ {
		eb := lb.Get(i).Message()
		key := elementKey(kd, eb)
		kpath := path + "/" + deep.EscapePathKey(key)
		ai, ok := inA[key]
		if !ok {
			*ops = append(*ops, deep.Operation{Kind: deep.OpAdd, Path: kpath, New: eb.Interface()})
			continue
		}
		if err := diffFields(kpath, la.Get(ai).Message(), eb, ops); err != nil {
			return err
		}
	}
	return nil
}

// applyKeyedList performs an operation addressed by key. An add for a key not
// present appends; a remove for one that is present takes it out wherever it
// sits; a deeper path descends into the matched element.
func applyKeyedList(m protoreflect.Message, fd, kd protoreflect.FieldDescriptor, rest []string, op deep.Operation) error {
	l := m.Mutable(fd).List()
	key := rest[0]
	idx := -1
	for i := 0; i < l.Len(); i++ {
		if elementKey(kd, l.Get(i).Message()) == key {
			idx = i
			break
		}
	}

	if len(rest) == 1 {
		if op.Strict && op.Old != nil {
			if idx < 0 {
				return fmt.Errorf("deepproto: strict check failed at key %q: absent", key)
			}
			want, err := coerceElem(l.NewElement, fd, op.Old)
			if err != nil {
				return fmt.Errorf("deepproto: strict check at key %q: %w", key, err)
			}
			if !scalarEqual(fd, l.Get(idx), want) {
				return fmt.Errorf("deepproto: strict check failed at key %q", key)
			}
		}
		switch op.Kind {
		case deep.OpRemove:
			if idx < 0 {
				return fmt.Errorf("deepproto: no element with key %q to remove", key)
			}
			for i := idx; i < l.Len()-1; i++ {
				l.Set(i, l.Get(i+1))
			}
			l.Truncate(l.Len() - 1)
			return nil
		case deep.OpAdd, deep.OpReplace:
			v, err := coerceElem(l.NewElement, fd, op.New)
			if err != nil {
				return err
			}
			if idx < 0 {
				l.Append(v)
			} else {
				l.Set(idx, v)
			}
			return nil
		default:
			return fmt.Errorf("deepproto: unsupported keyed-list operation %s", op.Kind)
		}
	}

	if idx < 0 {
		return fmt.Errorf("deepproto: no element with key %q", key)
	}
	return applySegments(l.Get(idx).Message(), rest[1:], op)
}

// resolveKeyedList reads by key, for condition paths.
func resolveKeyedList(fd, kd protoreflect.FieldDescriptor, l protoreflect.List, rest []string) (any, error) {
	key := rest[0]
	for i := 0; i < l.Len(); i++ {
		if elementKey(kd, l.Get(i).Message()) == key {
			return resolveElem(fd, l.Get(i), rest[1:])
		}
	}
	return nil, fmt.Errorf("deepproto: no element with key %q", key)
}
