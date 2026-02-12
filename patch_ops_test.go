package deep

import (
	"reflect"
	"testing"
)

func TestPatch_ReverseFormat_Exhaustive(t *testing.T) {
	// valuePatch
	t.Run("valuePatch", func(t *testing.T) {
		p := &valuePatch{oldVal: reflect.ValueOf(1), newVal: reflect.ValueOf(2)}
		p.reverse()
		p.format(0)
		p.toJSONPatch("/p")
		p.toJSONPatch("") // root

		pRem := &valuePatch{oldVal: reflect.ValueOf(1)}
		pRem.toJSONPatch("/p")
	})
	// ptrPatch
	t.Run("ptrPatch", func(t *testing.T) {
		p := &ptrPatch{elemPatch: &valuePatch{}}
		p.reverse()
		p.format(0)
		p.toJSONPatch("/p")
	})
	// interfacePatch
	t.Run("interfacePatch", func(t *testing.T) {
		p := &interfacePatch{elemPatch: &valuePatch{}}
		p.reverse()
		p.format(0)
		p.toJSONPatch("/p")
	})
	// structPatch
	t.Run("structPatch", func(t *testing.T) {
		p := &structPatch{fields: map[string]diffPatch{"A": &valuePatch{}}}
		p.reverse()
		p.format(0)
		p.toJSONPatch("/p")
	})
	// arrayPatch
	t.Run("arrayPatch", func(t *testing.T) {
		p := &arrayPatch{indices: map[int]diffPatch{0: &valuePatch{}}}
		p.reverse()
		p.format(0)
		p.toJSONPatch("/p")
	})
	// mapPatch
	t.Run("mapPatch", func(t *testing.T) {
		p := &mapPatch{
			added:    map[any]reflect.Value{"a": reflect.ValueOf(1)},
			removed:  map[any]reflect.Value{"b": reflect.ValueOf(2)},
			modified: map[any]diffPatch{"c": &valuePatch{}},
		}
		p.reverse()
		p.format(0)
		p.toJSONPatch("/p")
	})
	// slicePatch
	t.Run("slicePatch", func(t *testing.T) {
		p := &slicePatch{
			ops: []sliceOp{
				{Kind: OpAdd, Index: 0, Val: reflect.ValueOf(1)},
				{Kind: OpRemove, Index: 1, Val: reflect.ValueOf(2)},
				{Kind: OpReplace, Index: 2, Patch: &valuePatch{}},
			},
		}
		p.reverse()
		p.format(0)
		p.toJSONPatch("/p")
	})
	// testPatch
	t.Run("testPatch", func(t *testing.T) {
		p := &testPatch{expected: reflect.ValueOf(1)}
		p.reverse()
		p.format(0)
		p.toJSONPatch("/p")
	})
	// copyPatch
	t.Run("copyPatch", func(t *testing.T) {
		p := &copyPatch{from: "/a", path: "/b"}
		p.reverse()
		p.format(0)
		p.toJSONPatch("/p")
	})
	// movePatch
	t.Run("movePatch", func(t *testing.T) {
		p := &movePatch{from: "/a", path: "/b"}
		p.reverse()
		p.format(0)
		p.toJSONPatch("/p")
	})
	// logPatch
	t.Run("logPatch", func(t *testing.T) {
		p := &logPatch{message: "test"}
		p.reverse()
		p.format(0)
		p.toJSONPatch("/p")
	})
}

func TestPatch_MiscCoverage(t *testing.T) {
	// valuePatch reverse/format/toJSONPatch
	t.Run("valuePatch", func(t *testing.T) {
		p := &valuePatch{oldVal: reflect.ValueOf(1), newVal: reflect.ValueOf(2)}
		p.reverse()
		p.format(0)
		p.toJSONPatch("/path")
		p.toJSONPatch("") // root

		pRem := &valuePatch{oldVal: reflect.ValueOf(1)}
		pRem.toJSONPatch("/path")
	})

	// ptrPatch reverse/format/toJSONPatch
	t.Run("ptrPatch", func(t *testing.T) {
		p := &ptrPatch{elemPatch: &valuePatch{oldVal: reflect.ValueOf(1), newVal: reflect.ValueOf(2)}}
		p.reverse()
		p.format(0)
		p.toJSONPatch("/path")
	})

	// interfacePatch reverse/format/toJSONPatch
	t.Run("interfacePatch", func(t *testing.T) {
		p := &interfacePatch{elemPatch: &valuePatch{oldVal: reflect.ValueOf(1), newVal: reflect.ValueOf(2)}}
		p.reverse()
		p.format(0)
		p.toJSONPatch("/path")
	})

	// structPatch format/toJSONPatch
	t.Run("structPatch", func(t *testing.T) {
		p := &structPatch{fields: map[string]diffPatch{"A": &valuePatch{newVal: reflect.ValueOf(1)}}}
		p.format(0)
		p.toJSONPatch("/path")
	})

	// arrayPatch format/toJSONPatch
	t.Run("arrayPatch", func(t *testing.T) {
		p := &arrayPatch{indices: map[int]diffPatch{0: &valuePatch{newVal: reflect.ValueOf(1)}}}
		p.format(0)
		p.toJSONPatch("/path")
	})

	// mapPatch format/toJSONPatch
	t.Run("mapPatch", func(t *testing.T) {
		p := &mapPatch{
			added:    map[any]reflect.Value{"a": reflect.ValueOf(1)},
			removed:  map[any]reflect.Value{"b": reflect.ValueOf(2)},
			modified: map[any]diffPatch{"c": &valuePatch{newVal: reflect.ValueOf(3)}},
		}
		p.format(0)
		p.toJSONPatch("/path")
	})

	// slicePatch format
	t.Run("slicePatch", func(t *testing.T) {
		p := &slicePatch{
			ops: []sliceOp{
				{Kind: OpAdd, Index: 0, Val: reflect.ValueOf(1)},
				{Kind: OpRemove, Index: 1, Val: reflect.ValueOf(2)},
				{Kind: OpReplace, Index: 2, Patch: &valuePatch{newVal: reflect.ValueOf(3)}},
			},
		}
		p.format(0)
	})
}
