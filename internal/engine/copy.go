package engine

import (
	icore "github.com/brunoga/deep/v5/internal/core"
)

// Copier is an interface that types can implement to provide their own
// custom deep copy logic.
type Copier[T any] interface {
	Copy() (T, error)
}

// Copy creates a deep copy of src. It returns the copy and a nil error in case
// of success and the zero value for the type and a non-nil error on failure.
//
// It correctly handles cyclic references and unexported fields.
func Copy[T any](src T, opts ...CopyOption) (T, error) {
	coreOpts := make([]icore.CopyOption, len(opts))
	for i, opt := range opts {
		coreOpts[i] = opt.asCoreCopyOption()
	}
	return icore.Copy(src, coreOpts...)
}

// MustCopy creates a deep copy of src. It returns the copy on success or panics
// in case of any failure.
//
// It correctly handles cyclic references and unexported fields.
func MustCopy[T any](src T, opts ...CopyOption) T {
	coreOpts := make([]icore.CopyOption, len(opts))
	for i, opt := range opts {
		coreOpts[i] = opt.asCoreCopyOption()
	}
	return icore.MustCopy(src, coreOpts...)
}
