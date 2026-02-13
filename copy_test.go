package deep

import (
	"fmt"
	"reflect"
	"testing"
	"unsafe"
)

func TestCopy_Bool(t *testing.T) {
	src := true
	dst, err := Copy(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if dst != src {
		t.Errorf("expected %v, got %v", src, dst)
	}
}

func TestCopy_Int(t *testing.T) {
	src := 42
	dst, err := Copy(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if dst != src {
		t.Errorf("expected %v, got %v", src, dst)
	}
}

func TestCopy_Int8(t *testing.T) {
	src := int8(42)
	dst, err := Copy(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if dst != src {
		t.Errorf("expected %v, got %v", src, dst)
	}
}

func TestCopy_Int16(t *testing.T) {
	src := int16(42)
	dst, err := Copy(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if dst != src {
		t.Errorf("expected %v, got %v", src, dst)
	}
}

func TestCopy_Int32(t *testing.T) {
	src := int32(42)
	dst, err := Copy(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if dst != src {
		t.Errorf("expected %v, got %v", src, dst)
	}
}

func TestCopy_Int64(t *testing.T) {
	src := int64(42)
	dst, err := Copy(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if dst != src {
		t.Errorf("expected %v, got %v", src, dst)
	}
}

func TestCopy_Uint(t *testing.T) {
	src := uint(42)
	dst, err := Copy(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if dst != src {
		t.Errorf("expected %v, got %v", src, dst)
	}
}

func TestCopy_Uint8(t *testing.T) {
	src := uint8(42)
	dst, err := Copy(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if dst != src {
		t.Errorf("expected %v, got %v", src, dst)
	}
}

func TestCopy_Uint16(t *testing.T) {
	src := uint16(42)
	dst, err := Copy(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if dst != src {
		t.Errorf("expected %v, got %v", src, dst)
	}
}

func TestCopy_Uint32(t *testing.T) {
	src := uint32(42)
	dst, err := Copy(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if dst != src {
		t.Errorf("expected %v, got %v", src, dst)
	}
}

func TestCopy_Uint64(t *testing.T) {
	src := uint64(42)
	dst, err := Copy(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if dst != src {
		t.Errorf("expected %v, got %v", src, dst)
	}
}

func TestCopy_Uintptr(t *testing.T) {
	src := uintptr(42)
	dst, err := Copy(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if dst != src {
		t.Errorf("expected %v, got %v", src, dst)
	}
}

func TestCopy_Float32(t *testing.T) {
	src := float32(42.42)
	dst, err := Copy(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if dst != src {
		t.Errorf("expected %v, got %v", src, dst)
	}
}

func TestCopy_Float64(t *testing.T) {
	src := 42.42
	dst, err := Copy(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if dst != src {
		t.Errorf("expected %v, got %v", src, dst)
	}
}

func TestCopy_Complex64(t *testing.T) {
	src := complex(float32(42.42), float32(42.42))
	dst, err := Copy(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if dst != src {
		t.Errorf("expected %v, got %v", src, dst)
	}
}

func TestCopy_Complex128(t *testing.T) {
	src := complex(42.42, 42.42)
	dst, err := Copy(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if dst != src {
		t.Errorf("expected %v, got %v", src, dst)
	}
}

func TestCopy_String(t *testing.T) {
	src := "foo"
	dst, err := Copy(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if dst != src {
		t.Errorf("expected %v, got %v", src, dst)
	}
}

func TestCopy_Array(t *testing.T) {
	src := [3]int{1, 2, 3}
	dst, err := Copy(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if dst != src {
		t.Errorf("expected %v, got %v", src, dst)
	}
}

func TestCopy_Array_Error(t *testing.T) {
	type unsupported struct {
		f func()
	}

	src := [1]unsupported{
		{
			f: func() {},
		},
	}

	_, err := Copy(src)
	if err == nil {
		t.Errorf("expected error, got nil")
	}
}

func TestCopy_Map(t *testing.T) {
	src := map[string]int{
		"foo": 1,
		"bar": 2,
	}
	dst, err := Copy(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !reflect.DeepEqual(dst, src) {
		t.Errorf("expected %v, got %v", src, dst)
	}

	if reflect.ValueOf(dst).Pointer() == reflect.ValueOf(src).Pointer() {
		t.Errorf("expected different pointers, got same")
	}
}

func TestCopy_Map_Error(t *testing.T) {
	type unsupported struct {
		f func()
	}

	src := map[string]unsupported{
		"foo": {
			f: func() {},
		},
	}

	_, err := Copy(src)
	if err == nil {
		t.Errorf("expected error, got nil")
	}
}

func TestCopy_Map_Nil(t *testing.T) {
	var src map[string]int
	dst, err := Copy(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if dst != nil {
		t.Errorf("expected nil, got %v", dst)
	}
}

func TestCopy_Ptr(t *testing.T) {
	v := 42
	src := &v
	dst, err := Copy(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if dst == src {
		t.Errorf("expected different pointers, got same")
	}

	if *dst != *src {
		t.Errorf("expected %v, got %v", *src, *dst)
	}
}

func TestCopyPtr_Error(t *testing.T) {
	src := func() {}
	_, err := Copy(&src)
	if err == nil {
		t.Errorf("expected error, got nil")
	}
}

func TestCopy_Ptr_Nil(t *testing.T) {
	var src *int
	dst, err := Copy(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if dst != nil {
		t.Errorf("expected nil, got %v", dst)
	}
}

func TestCopy_Slice(t *testing.T) {
	src := []int{1, 2, 3}
	dst, err := Copy(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !reflect.DeepEqual(dst, src) {
		t.Errorf("expected %v, got %v", src, dst)
	}

	if reflect.ValueOf(dst).Pointer() == reflect.ValueOf(src).Pointer() {
		t.Errorf("expected different pointers, got same")
	}
}

func TestCopy_Slice_Nil(t *testing.T) {
	var src []int
	dst, err := Copy(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if dst != nil {
		t.Errorf("expected nil, got %v", dst)
	}
}

func TestCopy_Slice_Error(t *testing.T) {
	type unsupported struct {
		f func()
	}

	src := []unsupported{
		{
			f: func() {},
		},
	}

	_, err := Copy(src)
	if err == nil {
		t.Errorf("expected error, got nil")
	}
}

func TestCopy_Any_MapStringAny(t *testing.T) {
	src := any(map[string]any{
		"foo": 1,
		"bar": "baz",
	})
	dst, err := Copy(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !reflect.DeepEqual(dst, src) {
		t.Errorf("expected %v, got %v", src, dst)
	}

	if reflect.ValueOf(dst).Pointer() == reflect.ValueOf(src).Pointer() {
		t.Errorf("expected different pointers, got same")
	}
}

func TestCopy_Struct(t *testing.T) {
	type s struct {
		A int
		B string
	}

	src := s{
		A: 42,
		B: "foo",
	}
	dst, err := Copy(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if dst != src {
		t.Errorf("expected %v, got %v", src, dst)
	}
}

func TestCopy_Struct_Loop(t *testing.T) {
	type s struct {
		S *s
	}

	src := &s{}
	src.S = src

	dst, err := Copy(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if dst.S != dst {
		t.Errorf("expected loop, got %p vs %p", dst.S, dst)
	}
}

func TestCopy_Struct_Unexported(t *testing.T) {
	type s struct {
		a int
		B string
	}

	src := s{
		a: 42,
		B: "foo",
	}
	dst, err := Copy(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if dst != src {
		t.Errorf("expected %v, got %v", src, dst)
	}
}

func TestCopy_Struct_Error(t *testing.T) {
	type unsupported struct {
		f func()
	}

	type s struct {
		U unsupported
	}

	src := s{
		U: unsupported{
			f: func() {},
		},
	}

	_, err := Copy(src)
	if err == nil {
		t.Errorf("expected error, got nil")
	}
}

func TestCopy_Func_Nil(t *testing.T) {
	var src func()
	dst, err := Copy(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if dst != nil {
		t.Errorf("expected nil")
	}
}

func TestCopy_Func_Error(t *testing.T) {
	src := func() {}
	_, err := Copy(src)
	if err == nil {
		t.Errorf("expected error, got nil")
	}
}

func TestCopy_Chan_Nil(t *testing.T) {
	var src chan int
	dst, err := Copy(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if dst != nil {
		t.Errorf("expected nil, got %v", dst)
	}
}

func TestCopy_Chan_Error(t *testing.T) {
	src := make(chan int)
	_, err := Copy(src)
	if err == nil {
		t.Errorf("expected error, got nil")
	}
}

func TestCopy_UnsafePointer_Nil(t *testing.T) {
	var src unsafe.Pointer
	dst, err := Copy(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if dst != nil {
		t.Errorf("expected nil, got %v", dst)
	}
}

func TestCopy_UnsafePointer_Error(t *testing.T) {
	v := 42
	src := unsafe.Pointer(&v)
	_, err := Copy(src)
	if err == nil {
		t.Errorf("expected error, got nil")
	}
}

func TestCopy_Interface_Nil(t *testing.T) {
	var src any
	dst, err := Copy(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if dst != nil {
		t.Errorf("expected nil, got %v", dst)
	}
}

func TestCopy_Interface_Struct_Recursive_Nil(t *testing.T) {
	type s struct {
		A any
	}

	var src s
	dst, err := Copy(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if dst.A != nil {
		t.Errorf("expected nil, got %v", dst.A)
	}
}

func TestCopy_Interface_Slice_Recursive_Nil(t *testing.T) {
	src := []any{nil}
	dst, err := Copy(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if dst[0] != nil {
		t.Errorf("expected nil, got %v", dst[0])
	}
}

func TestCopy_Interface_Map_Recursive_Nil(t *testing.T) {
	src := map[string]any{
		"foo": nil,
	}
	dst, err := Copy(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if dst["foo"] != nil {
		t.Errorf("expected nil, got %v", dst["foo"])
	}
}

func TestCopy_DerivedType(t *testing.T) {
	type MyInt int
	src := MyInt(42)
	dst, err := Copy(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if dst != src {
		t.Errorf("expected %v, got %v", src, dst)
	}
}

func TestCopy_Struct_With_Any_Field(t *testing.T) {
	type s struct {
		A any
	}

	src := s{
		A: 42,
	}
	dst, err := Copy(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if dst != src {
		t.Errorf("expected %v, got %v", src, dst)
	}
}

func TestCopySkipUnsupported(t *testing.T) {
	src := func() {}
	dst, err := Copy(src, SkipUnsupported())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if dst != nil {
		t.Errorf("expected nil")
	}
}

func TestMustCopy(t *testing.T) {
	src := 42
	dst := MustCopy(src)
	if dst != src {
		t.Errorf("expected %v, got %v", src, dst)
	}
}

func TestMustCopy_Error(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("expected panic, got nil")
		}
	}()

	src := func() {}
	MustCopy(src)
}

func TestTrickyMemberPointer(t *testing.T) {
	type s struct {
		A *int
		B *int
	}

	v := 42
	src := s{
		A: &v,
		B: &v,
	}

	dst, err := Copy(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if dst.A != dst.B {
		t.Errorf("expected same pointer, got different")
	}

	if dst.A == src.A {
		t.Errorf("expected different pointers, got same")
	}

	if *dst.A != 42 {
		t.Errorf("expected 42, got %d", *dst.A)
	}
}

type CustomTypeForCopier struct {
	Value int
}

func (c CustomTypeForCopier) Copy() (CustomTypeForCopier, error) {
	return CustomTypeForCopier{Value: c.Value + 1}, nil
}

func TestCopy_CustomCopier_ValueReceiver(t *testing.T) {
	src := CustomTypeForCopier{Value: 10}
	dst, err := Copy(src)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if dst.Value != 11 {
		t.Errorf("expected 11, got %d", dst.Value)
	}
}

type CustomErrorCopier struct{}

func (c CustomErrorCopier) Copy() (CustomErrorCopier, error) {
	return CustomErrorCopier{}, fmt.Errorf("custom error")
}

func TestCopy_CustomCopier_ErrorCase(t *testing.T) {
	src := CustomErrorCopier{}
	_, err := Copy(src)
	if err == nil || err.Error() != "custom error" {
		t.Errorf("expected 'custom error', got %v", err)
	}
}

type CustomPtrTypeForCopier struct {
	Value int
}

var customPtrTypeCopyCalled bool

func (c *CustomPtrTypeForCopier) Copy() (*CustomPtrTypeForCopier, error) {
	customPtrTypeCopyCalled = true
	return &CustomPtrTypeForCopier{Value: c.Value + 5}, nil
}

func TestCopy_CustomCopier_PointerReceiver(t *testing.T) {
	customPtrTypeCopyCalled = false
	src := &CustomPtrTypeForCopier{Value: 10}
	dst, err := Copy(src)
	if err != nil {
		t.Fatalf("Copy failed: %v", err)
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
		t.Errorf("Expected nil copy for nil input, got %v", dstNil)
	}
}

func TestCopy_Options(t *testing.T) {
	type S struct {
		A int
		B string
	}
	s := S{A: 1, B: "secret"}

	t.Run("IgnorePath", func(t *testing.T) {
		got, err := Copy(s, CopyIgnorePath("B"))
		if err != nil {
			t.Fatalf("Copy failed: %v", err)
		}
		if got.A != 1 || got.B != "" {
			t.Errorf("CopyIgnorePath failed: %+v", got)
		}
	})

	t.Run("SkipUnsupported", func(t *testing.T) {
		type Unsupported struct {
			F func()
		}
		u := Unsupported{F: func() {}}
		_, err := Copy(u)
		if err == nil {
			t.Error("Expected error for unsupported type")
		}

		got, err := Copy(u, SkipUnsupported())
		if err != nil {
			t.Fatalf("Copy with SkipUnsupported failed: %v", err)
		}
		if got.F != nil {
			t.Error("Expected nil function in copy")
		}
	})
}

func TestMustCopy_Options(t *testing.T) {
	type S struct {
		A int
		B string
	}
	s := S{A: 1, B: "secret"}
	got := MustCopy(s, CopyIgnorePath("B"))
	if got.A != 1 || got.B != "" {
		t.Errorf("MustCopy with options failed: %+v", got)
	}
}
