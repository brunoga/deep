package core

import (
	"reflect"
	"strings"
	"sync"
)

type FieldInfo struct {
	Index   int
	Name    string
	JSONTag string
	Tag     StructTag
}

// FieldPathName is [FieldInfo.PathName] for a field the caller holds
// directly, without going through the type cache.
func FieldPathName(field reflect.StructField) string {
	full := field.Tag.Get("json")
	if full == "-" {
		return field.Name
	}
	if name, _, _ := strings.Cut(full, ","); name != "" {
		return name
	}
	return field.Name
}

// PathName is the name this field takes in a patch path: its JSON name where
// it has one, and its Go name otherwise.
//
// Paths are how a patch survives leaving the process, so they are named the
// way the document is named — which for anything crossing a wire, a log or a
// language boundary is the JSON name. Generated code and the type-safe
// selectors have always done this; the reflection engine now agrees with
// them, so a patch describes the same field the same way whichever engine
// produced it.
func (f FieldInfo) PathName() string {
	if f.JSONTag != "" {
		return f.JSONTag
	}
	return f.Name
}

type TypeInfo struct {
	Fields        []FieldInfo
	KeyFieldIndex int
}

var (
	typeCache sync.Map // map[reflect.Type]*TypeInfo
)

func GetTypeInfo(typ reflect.Type) *TypeInfo {
	if info, ok := typeCache.Load(typ); ok {
		return info.(*TypeInfo)
	}

	info := &TypeInfo{
		KeyFieldIndex: -1,
	}
	if typ.Kind() == reflect.Struct {
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			tag := ParseTag(field)
			// `json:"-"` names no field at all, so it contributes no path
			// name; `json:"-,"` names a field that really is called "-",
			// which is the one case where the two spellings differ.
			jsonTag := ""
			if full := field.Tag.Get("json"); full != "-" {
				jsonTag, _, _ = strings.Cut(full, ",")
			}
			info.Fields = append(info.Fields, FieldInfo{
				Index:   i,
				Name:    field.Name,
				JSONTag: jsonTag,
				Tag:     tag,
			})
			if tag.Key {
				info.KeyFieldIndex = i
			}
		}
	}

	typeCache.Store(typ, info)
	return info
}
