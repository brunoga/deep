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
			jsonTag := field.Tag.Get("json")
			if jsonTag != "" {
				jsonTag = strings.Split(jsonTag, ",")[0]
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

