package deep

import (
	"fmt"
	"reflect"
	"testing"
	"unsafe"
)

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

func TestCopy_Map_Nil(t *testing.T) {
	var m map[int]int
	doCopyAndCheck(t, m, false)
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

func TestCopy_Slice_Nil(t *testing.T) {
	var S []int
	doCopyAndCheck(t, S, false)
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
	var value any
	doCopyAndCheck(t, value, false)
}

func TestCopy_Interface_Struct_Recursive_Nil(t *testing.T) {
	var s struct {
		A any
	}
	doCopyAndCheck(t, s, false)
}

func TestCopy_Interface_Slice_Recursive_Nil(t *testing.T) {
	value := []any{nil}
	doCopyAndCheck(t, value, false)
}

func TestCopy_Interface_Map_Recursive_Nil(t *testing.T) {
	value := map[string]any{"test": nil}
	doCopyAndCheck(t, value, false)
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

func TestTrickyMemberPointer(t *testing.T) {
	type Foo struct {
		N int
	}
	type Bar struct {
		F *Foo
		P *int
	}

	foo := Foo{N: 1}
	bar := Bar{F: &foo, P: &foo.N}

	doCopyAndCheck(t, bar, false)
}

type CustomTypeForCopier struct {
	Value int
	F     func() // Normally unsupported if non-nil.
}

var (
	customTypeCopyCalled    bool
	customTypeCopyErrored   bool
	customPtrTypeCopyCalled bool
)

func (ct CustomTypeForCopier) Copy() (CustomTypeForCopier, error) {
	customTypeCopyCalled = true
	if ct.F != nil && ct.Value == -1 { // Special case to return error
		customTypeCopyErrored = true
		return CustomTypeForCopier{}, fmt.Errorf("custom copy error for F")
	}
	// Example custom logic: double value, share function pointer
	return CustomTypeForCopier{Value: ct.Value * 2, F: ct.F}, nil
}

func TestCopy_CustomCopier_ValueReceiver(t *testing.T) {
	customTypeCopyCalled = false
	customTypeCopyErrored = false
	src := CustomTypeForCopier{Value: 10, F: func() {}}

	dst, err := Copy(src)

	if err != nil {
		t.Fatalf("Copy failed for CustomCopier: %v", err)
	}
	if !customTypeCopyCalled {
		t.Errorf("Custom Copier method was not called")
	}
	if customTypeCopyErrored {
		t.Errorf("Custom Copier method unexpectedly errored")
	}
	if dst.Value != 20 { // As per custom logic
		t.Errorf("Expected dst.Value to be 20, got %d", dst.Value)
	}
	if reflect.ValueOf(dst.F).Pointer() != reflect.ValueOf(src.F).Pointer() {
		t.Errorf("Expected func to be shallow copied (shared) by custom copier")
	}
}

func TestCopy_CustomCopier_ErrorCase(t *testing.T) {
	customTypeCopyCalled = false
	customTypeCopyErrored = false
	// Trigger error condition in custom copier
	src := CustomTypeForCopier{Value: -1, F: func() {}}

	_, err := Copy(src)

	if err == nil {
		t.Fatalf("Expected error from custom copier, got nil")
	}
	if !customTypeCopyCalled {
		t.Errorf("Custom Copier method was not called (for error case)")
	}
	if !customTypeCopyErrored {
		t.Errorf("Custom Copier method did not flag error internally")
	}
	expectedErrorMsg := "custom copy error for F"
	if err.Error() != expectedErrorMsg {
		t.Errorf("Expected error message '%s', got '%s'", expectedErrorMsg, err.Error())
	}
}

type CustomPtrTypeForCopier struct {
	Value int
}

func (cpt *CustomPtrTypeForCopier) Copy() (*CustomPtrTypeForCopier, error) {
	customPtrTypeCopyCalled = true
	if cpt == nil {
		// This case should ideally not be hit if the main Copy function guards against it.
		return nil, fmt.Errorf("custom Copy() called on nil CustomPtrTypeForCopier receiver")
	}
	return &CustomPtrTypeForCopier{Value: cpt.Value * 3}, nil
}

func TestCopy_CustomCopier_PointerReceiver(t *testing.T) {
	customPtrTypeCopyCalled = false
	src := &CustomPtrTypeForCopier{Value: 5} // T is *CustomPtrTypeForCopier

	dst, err := Copy(src)

	if err != nil {
		t.Fatalf("Copy failed for CustomCopier with pointer receiver: %v", err)
	}
	if !customPtrTypeCopyCalled {
		t.Errorf("Custom Copier method (ptr receiver) was not called")
	}
	if dst.Value != 15 {
		t.Errorf("Expected dst.Value to be 15, got %d", dst.Value)
	}
	if dst == src {
		t.Errorf("Expected a new pointer from custom copier, got the same pointer")
	}

	// Test that a nil pointer of a type that implements Copier still results in a nil copy
	// and does not call the custom Copy method.
	customPtrTypeCopyCalled = false
	var nilSrc *CustomPtrTypeForCopier
	dstNil, errNil := Copy(nilSrc)
	if errNil != nil {
		t.Fatalf("Copy failed for nil CustomPtrTypeForCopier: %v", errNil)
	}
	if customPtrTypeCopyCalled {
		t.Errorf("Custom Copier method (ptr receiver) was called for nil input, but should not have been")
	}
	if dstNil != nil {
		t.Errorf("Expected nil for copied nil pointer of custom type, got %v", dstNil)
	}
}
