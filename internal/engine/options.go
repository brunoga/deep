package engine

import (
	"reflect"

	"github.com/brunoga/deep/v5/internal/core"
)

// DiffOption allows configuring the behavior of the Diff function.
type DiffOption interface {
	applyDiffOption()
}

// CopyOption allows configuring the behavior of the Copy function.
type CopyOption interface {
	asCoreCopyOption() core.CopyOption
}

// EqualOption allows configuring the behavior of the Equal function.
type EqualOption interface {
	asCoreEqualOption() core.EqualOption
}

type unifiedOption string

func (u unifiedOption) asCoreCopyOption() core.CopyOption {
	return core.CopyIgnorePath(string(u))
}

func (u unifiedOption) asCoreEqualOption() core.EqualOption {
	return core.EqualIgnorePath(string(u))
}

func (u unifiedOption) applyDiffOption() {}

// IgnorePath returns an option that tells Diff, Copy, and Equal to ignore
// changes at the specified path.
// The path should use JSON Pointer notation (e.g., "/Field/SubField", "/Map/Key", "/Slice/0").
func IgnorePath(path string) interface {
	DiffOption
	CopyOption
	EqualOption
} {
	return unifiedOption(path)
}

type simpleCopyOption struct {
	opt core.CopyOption
}

func (s simpleCopyOption) asCoreCopyOption() core.CopyOption { return s.opt }

// SkipUnsupported returns an option that tells Copy to skip unsupported types.
func SkipUnsupported() CopyOption {
	return simpleCopyOption{core.SkipUnsupported()}
}

// RegisterCustomCopy registers a custom copy function for a specific type.
func RegisterCustomCopy[T any](fn func(T) (T, error)) {
	var t T
	typ := reflect.TypeOf(t)
	core.RegisterCustomCopy(typ, reflect.ValueOf(fn))
}

// RegisterCustomEqual registers a custom equality function for a specific type.
func RegisterCustomEqual[T any](fn func(T, T) bool) {
	var t T
	typ := reflect.TypeOf(t)
	core.RegisterCustomEqual(typ, reflect.ValueOf(fn))
}
