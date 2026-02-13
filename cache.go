package deep

import (
	"reflect"
	"sync"
)

type fieldInfo struct {
	index int
	name  string
	tag   structTag
}

type typeInfo struct {
	fields        []fieldInfo
	keyFieldIndex int
}

var (
	typeCache sync.Map // map[reflect.Type]*typeInfo
)

func getTypeInfo(typ reflect.Type) *typeInfo {
	if info, ok := typeCache.Load(typ); ok {
		return info.(*typeInfo)
	}

	info := &typeInfo{
		keyFieldIndex: -1,
	}
	if typ.Kind() == reflect.Struct {
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			tag := parseTag(field)
			info.fields = append(info.fields, fieldInfo{
				index: i,
				name:  field.Name,
				tag:   tag,
			})
			if tag.key {
				info.keyFieldIndex = i
			}
		}
	}

	typeCache.Store(typ, info)
	return info
}
