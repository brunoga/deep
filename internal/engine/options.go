package engine

import (
	"reflect"

	icore "github.com/brunoga/deep/v5/internal/core"
)

// DiffOption allows configuring the behavior of the Diff function.
type DiffOption interface {
	applyDiffOption()
}

// CopyOption allows configuring the behavior of the Copy function.
type CopyOption interface {
	asCoreCopyOption() icore.CopyOption
}

// EqualOption allows configuring the behavior of the Equal function.
type EqualOption interface {
	asCoreEqualOption() icore.EqualOption
}

type unifiedOption string

func (u unifiedOption) asCoreCopyOption() icore.CopyOption {
	return icore.CopyIgnorePath(string(u))
}

func (u unifiedOption) asCoreEqualOption() icore.EqualOption {
	return icore.EqualIgnorePath(string(u))
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
	opt icore.CopyOption
}

func (s simpleCopyOption) asCoreCopyOption() icore.CopyOption { return s.opt }

// SkipUnsupported returns an option that tells Copy to skip unsupported types.
func SkipUnsupported() CopyOption {
	return simpleCopyOption{icore.SkipUnsupported()}
}

// RegisterCustomCopy registers a custom copy function for a specific type.
func RegisterCustomCopy[T any](fn func(T) (T, error)) {
	var t T
	typ := reflect.TypeOf(t)
	icore.RegisterCustomCopy(typ, reflect.ValueOf(fn))
}

// RegisterCustomEqual registers a custom equality function for a specific type.
func RegisterCustomEqual[T any](fn func(T, T) bool) {
	var t T
	typ := reflect.TypeOf(t)
	icore.RegisterCustomEqual(typ, reflect.ValueOf(fn))
}
