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
	tag := field.Tag.Get("deep")
	if tag == "" {
		return StructTag{}
	}

	st := StructTag{}
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
