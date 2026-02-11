package deep

import (
	"reflect"
	"strings"
)

type structTag struct {
	ignore   bool
	readOnly bool
	atomic   bool
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
		}
	}

	return st
}
