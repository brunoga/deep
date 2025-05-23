package deep

import (
	"fmt"

	"github.com/brunoga/deep/patch"
)

// Patch applies a patch to a deep copy of src and returns the result.
// If an error occurs during copying or patching, it returns the zero value and the error.
func Patch[T any](src T, p patch.Patch[T]) (T, error) {
	// First make a deep copy of the source
	copy, err := Copy(src)
	if err != nil {
		var zero T
		return zero, fmt.Errorf("deep: error creating copy for patching: %w", err)
	}

	// Apply the patch to the copy
	if err = p.Apply(&copy); err != nil {
		var zero T
		return zero, fmt.Errorf("deep: error applying patch: %w", err)
	}

	return copy, nil
}

// MustPatch applies a patch to a deep copy of src and returns the result.
// It panics if any error occurs during copying or patching.
func MustPatch[T any](src T, p patch.Patch[T]) T {
	result, err := Patch(src, p)
	if err != nil {
		panic(err)
	}
	return result
}
