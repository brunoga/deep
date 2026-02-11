package deep

import (
	"reflect"
	"strings"
)

type structTag struct {
	ignore   bool
	readOnly bool
	atomic   bool
	key      bool
}

func parseTag(field reflect.StructField) structTag {
	tag := field.Tag.Get("deep")
	if tag == "" {
		return structTag{}
	}

	st := structTag{}
	parts := strings.Split(tag, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		switch part {
		case "-":
			st.ignore = true
		case "readonly":
			st.readOnly = true
		case "atomic":
			st.atomic = true
		case "key":
			st.key = true
		}
	}

	return st
}

func getKeyField(typ reflect.Type) (int, bool) {
	if typ.Kind() == reflect.Ptr {
		typ = typ.Elem()
	}
	if typ.Kind() != reflect.Struct {
		return -1, false
	}

	for i := 0; i < typ.NumField(); i++ {
		tag := parseTag(typ.Field(i))
		if tag.key {
			return i, true
		}
	}

	return -1, false
}
