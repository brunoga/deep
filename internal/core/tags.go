package core

import (
	"reflect"
	"strings"
)

type StructTag struct {
	Ignore   bool
	ReadOnly bool
	Atomic   bool
	Key      bool
}

func ParseTag(field reflect.StructField) StructTag {
	st := StructTag{}

	// A field kept out of the JSON document is kept out of deep as well: it
	// stays out of diffs, out of equality and out of clones. The rule lives
	// here so that every reader of a field's tags sees it — diffing, cloning
	// and applying each ask separately, and a field that is invisible to one
	// of them but not the others is worse than no rule at all.
	if name, _, _ := strings.Cut(field.Tag.Get("json"), ","); name == "-" {
		st.Ignore = true
	}

	tag := field.Tag.Get("deep")
	if tag == "" {
		return st
	}

	parts := strings.Split(tag, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		switch part {
		case "-":
			st.Ignore = true
		case "readonly":
			st.ReadOnly = true
		case "atomic":
			st.Atomic = true
		case "key":
			st.Key = true
		}
	}

	return st
}

func GetKeyField(typ reflect.Type) (int, bool) {
	if typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ.Kind() != reflect.Struct {
		return -1, false
	}

	info := GetTypeInfo(typ)
	if info.KeyFieldIndex != -1 {
		return info.KeyFieldIndex, true
	}

	return -1, false
}
