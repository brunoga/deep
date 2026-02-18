package unsafe_test

import (
	"reflect"
	"testing"

	"github.com/brunoga/deep/v4/internal/unsafe"
	"github.com/brunoga/deep/v4/internal/unsafe/testdata/foo"
)

func TestDisableRO_CrossPackage(t *testing.T) {
	s := foo.NewS(1, 2)
	rv := reflect.ValueOf(&s).Elem()

	// 'b' is unexported in package 'foo'
	fieldB := rv.FieldByName("b")
	if !fieldB.IsValid() {
		t.Fatal("field b not found")
	}

	// Before DisableRO:
	// - CanInterface() should be false
	// - CanSet() should be false
	if fieldB.CanInterface() {
		t.Error("expected CanInterface() to be false for unexported cross-package field")
	}
	if fieldB.CanSet() {
		t.Error("expected CanSet() to be false for unexported cross-package field")
	}

	// Interface() should panic
	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Error("expected Interface() to panic for unexported field from another package")
			}
		}()
		_ = fieldB.Interface()
	}()

	// ACT
	unsafe.DisableRO(&fieldB)

	// After DisableRO:
	// - CanInterface() should be true
	// - CanSet() should be true (since s is addressable)
	if !fieldB.CanInterface() {
		t.Error("expected CanInterface() to be true after DisableRO")
	}
	if !fieldB.CanSet() {
		t.Error("expected CanSet() to be true after DisableRO")
	}

	// Now it should work for Reading
	val := fieldB.Interface().(int)
	if val != 2 {
		t.Errorf("expected 2, got %d", val)
	}

	// And for Writing
	fieldB.SetInt(100)
	if s.GetB() != 100 {
		t.Errorf("expected 100, got %d", s.GetB())
	}
}
