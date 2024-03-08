package deep

import (
	"reflect"
	"testing"
	"unsafe"
	/*
		"github.com/barkimedes/go-deepcopy"
		"github.com/mitchellh/copystructure"
	*/)

func TestCopy_Bool(t *testing.T) {
	doCopyAndCheck(t, true, false)
}

func TestCopy_Int(t *testing.T) {
	doCopyAndCheck(t, 42, false)
}

func TestCopy_Int8(t *testing.T) {
	doCopyAndCheck(t, int8(42), false)
}

func TestCopy_Int16(t *testing.T) {
	doCopyAndCheck(t, int16(42), false)
}

func TestCopy_Int32(t *testing.T) {
	doCopyAndCheck(t, int32(42), false)
}

func TestCopy_Int64(t *testing.T) {
	doCopyAndCheck(t, int64(42), false)
}

func TestCopy_Uint(t *testing.T) {
	doCopyAndCheck(t, uint(42), false)
}

func TestCopy_Uint8(t *testing.T) {
	doCopyAndCheck(t, uint8(42), false)
}

func TestCopy_Uint16(t *testing.T) {
	doCopyAndCheck(t, uint16(42), false)
}

func TestCopy_Uint32(t *testing.T) {
	doCopyAndCheck(t, uint32(42), false)
}

func TestCopy_Uint64(t *testing.T) {
	doCopyAndCheck(t, uint64(42), false)
}

func TestCopy_Uintptr(t *testing.T) {
	doCopyAndCheck(t, uintptr(42), false)
}

func TestCopy_Float32(t *testing.T) {
	doCopyAndCheck(t, float32(42), false)
}

func TestCopy_Float64(t *testing.T) {
	doCopyAndCheck(t, float64(42), false)
}

func TestCopy_Complex64(t *testing.T) {
	doCopyAndCheck(t, complex64(42), false)
}

func TestCopy_Complex128(t *testing.T) {
	doCopyAndCheck(t, complex128(42), false)
}

func TestCopy_String(t *testing.T) {
	doCopyAndCheck(t, "42", false)
}

func TestCopy_Array(t *testing.T) {
	doCopyAndCheck(t, [4]int{42, 43, 44, 45}, false)
}

func TestCopy_Array_Error(t *testing.T) {
	doCopyAndCheck(t, [1]func(){func() {}}, true)
}

func TestCopy_Map(t *testing.T) {
	doCopyAndCheck(t, map[int]string{42: "42", 43: "43", 44: "44", 45: "45"}, false)
}

func TestCopy_Map_Error(t *testing.T) {
	doCopyAndCheck(t, map[int]func(){0: func() {}}, true)
}

func TestCopy_Ptr(t *testing.T) {
	value := 42
	doCopyAndCheck(t, &value, false)
}

func TestCopyPtr_Error(t *testing.T) {
	value := func() {}
	doCopyAndCheck(t, &value, true)
}

func TestCopy_Ptr_Nil(t *testing.T) {
	value := (*int)(nil)
	doCopyAndCheck(t, value, false)
}

func TestCopy_Slice(t *testing.T) {
	doCopyAndCheck(t, []int{42, 43, 44, 45}, false)
}

func TestCopy_Slice_Error(t *testing.T) {
	doCopyAndCheck(t, []func(){func() {}}, true)
}

func TestCopy_Any_MapStringAny(t *testing.T) {
	doCopyAndCheck(t, any(map[string]any{"key": 123}), false)
}

func TestCopy_Struct(t *testing.T) {
	type S struct {
		A int
		B string
	}
	doCopyAndCheck(t, S{42, "42"}, false)
}

func TestCopy_Struct_Loop(t *testing.T) {
	type S struct {
		A int
		B *S
	}

	// Create a loop.
	src := S{A: 1}
	src.B = &src

	doCopyAndCheck(t, src, false)
}

func TestCopy_Struct_Unexported(t *testing.T) {
	type S struct {
		a int
		b string
	}

	doCopyAndCheck(t, S{42, "42"}, false)
}

func TestCopy_Struct_Error(t *testing.T) {
	type S struct {
		A func()
	}

	doCopyAndCheck(t, S{A: func() {}}, true)
}

func TestCopy_Func_Nil(t *testing.T) {
	var f func()
	doCopyAndCheck(t, f, false)
}

func TestCopy_Func_Error(t *testing.T) {
	doCopyAndCheck(t, func() {}, true)
}

func TestCopy_Chan_Nil(t *testing.T) {
	var c chan struct{}
	doCopyAndCheck(t, c, false)
}

func TestCopy_Chan_Error(t *testing.T) {
	doCopyAndCheck(t, make(chan struct{}), true)
}

func TestCopy_UnsafePointer_Nil(t *testing.T) {
	var p unsafe.Pointer
	doCopyAndCheck(t, p, false)
}

func TestCopy_UnsafePointer_Error(t *testing.T) {
	doCopyAndCheck(t, unsafe.Pointer(t), true)
}

func TestCopy_Interface_Nil(t *testing.T) {
	type S any

	type A struct {
		Ptr S
	}

	doCopyAndCheck(t, A{}, false)
}

func TestCopy_DerivedType(t *testing.T) {
	type S int
	doCopyAndCheck(t, S(42), false)
}

func TestCopy_Struct_With_Any_Field(t *testing.T) {
	type S struct {
		A any
	}

	src := S{A: map[string]interface{}{"key1": "value1", "key2": 12345}}
	doCopyAndCheck(t, src, false)
}

func TestCopySkipUnsupported(t *testing.T) {
	type S struct {
		A int
		B func()
		C int
	}

	src := S{A: 42, B: func() {}, C: 43}
	dst, err := CopySkipUnsupported(src)
	if err != nil {
		t.Errorf("CopySkipUnsupported failed: %v", err)
	}

	if src.A != dst.A {
		t.Errorf("CopySkipUnsupported failed: expected %v, got %v", src.A, dst.A)
	}

	if src.C != dst.C {
		t.Errorf("CopySkipUnsupported failed: expected %v, got %v", src.C, dst.C)
	}

	if dst.B != nil {
		t.Errorf("CopySkipUnsupported failed: expected nil, got non-nil")
	}
}

func TestMustCopy(t *testing.T) {
	src := 42
	dst := MustCopy(src)
	if src != dst {
		t.Errorf("MustCopy failed: expected %v, got %v", src, dst)
	}
}

func TestMustCopy_Error(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("MustCopy did not panic")
		}
	}()

	MustCopy(func() {})
}

func doCopyAndCheck[T any](t *testing.T, src T, expectError bool) {
	t.Helper()

	dst, err := Copy(src)
	if err != nil {
		if !expectError {
			t.Errorf("Copy failed: %v", err)
		}
		return
	}
	if !reflect.DeepEqual(dst, src) {
		t.Errorf("Copy failed: expected %v, got %v", src, dst)
	}
}

func BenchmarkCopy_Deep(b *testing.B) {
	type InnerStruct struct {
		Description string
		ID          int
		Points      *InnerStruct
	}

	type NestedStruct struct {
		Title     string
		InnerData InnerStruct
		MoreData  *NestedStruct
	}

	type ComplexStruct struct {
		Name        string
		Age         int
		Data        map[string]interface{}
		Nested      NestedStruct
		Pointers    []*InnerStruct
		IsAvailable bool
		// Both below can be copied if they are nil.
		F func()
		C chan struct{}
	}

	src := ComplexStruct{
		Name:        "Complex Example",
		Age:         42,
		Data:        map[string]interface{}{"key1": "value1", "key2": 12345},
		IsAvailable: true,
	}

	innerInstance := InnerStruct{
		Description: "Inner struct instance",
		ID:          1,
	}

	innerInstance.Points = &innerInstance // Cyclic reference

	nestedInstance := NestedStruct{
		Title:     "Nested Instance",
		InnerData: innerInstance,
	}

	nestedInstance.MoreData = &nestedInstance // Cyclic reference

	src.Nested = nestedInstance
	src.Pointers = append(src.Pointers, &innerInstance)

	for i := 0; i < b.N; i++ {
		MustCopy(src)
	}
}

/*
func BenchmarkCopy_DeepCopy(b *testing.B) {
	type InnerStruct struct {
		Description string
		ID          int
		Points      *InnerStruct
	}

	type NestedStruct struct {
		Title     string
		InnerData InnerStruct
		MoreData  *NestedStruct
	}

	type ComplexStruct struct {
		Name        string
		Age         int
		Data        map[string]interface{}
		Nested      NestedStruct
		Pointers    []*InnerStruct
		IsAvailable bool
	}

	src := ComplexStruct{
		Name:        "Complex Example",
		Age:         42,
		Data:        map[string]interface{}{"key1": "value1", "key2": 12345},
		IsAvailable: true,
	}

	innerInstance := InnerStruct{
		Description: "Inner struct instance",
		ID:          1,
	}

	innerInstance.Points = &innerInstance // Cyclic reference

	nestedInstance := NestedStruct{
		Title:     "Nested Instance",
		InnerData: innerInstance,
	}

	nestedInstance.MoreData = &nestedInstance // Cyclic reference

	src.Nested = nestedInstance
	src.Pointers = append(src.Pointers, &innerInstance)

	for i := 0; i < b.N; i++ {
		deepcopy.MustAnything(src)
	}
}

func BenchmarkCopy_CopyStructure(b *testing.B) {
	type InnerStruct struct {
		Description string
		ID          int
		Points      *InnerStruct
	}

	type NestedStruct struct {
		Title     string
		InnerData InnerStruct
		MoreData  *NestedStruct
	}

	type ComplexStruct struct {
		Name        string
		Age         int
		Data        map[string]interface{}
		Nested      NestedStruct
		Pointers    []*InnerStruct
		IsAvailable bool
		// Both below can be copied if they are nil.
		F func()
		C chan struct{}
	}

	src := ComplexStruct{
		Name:        "Complex Example",
		Age:         42,
		Data:        map[string]interface{}{"key1": "value1", "key2": 12345},
		IsAvailable: true,
	}

	innerInstance := InnerStruct{
		Description: "Inner struct instance",
		ID:          1,
	}

	// The copystructure library does not support cyclic references.
	innerInstance.Points = nil

	nestedInstance := NestedStruct{
		Title:     "Nested Instance",
		InnerData: innerInstance,
	}

	// The copystructure library does not support cyclic references.
	nestedInstance.MoreData = nil

	src.Nested = nestedInstance
	src.Pointers = append(src.Pointers, &innerInstance)

	for i := 0; i < b.N; i++ {
		copystructure.Copy(src)
	}
}
*/
