package deep

import (
	"github.com/brunoga/deep/v3/internal/core"
)

// Equal performs a deep equality check between a and b.
// It supports cyclic references and unexported fields.
// You can customize behavior using EqualOption (e.g., IgnorePath).
func Equal[T any](a, b T, opts ...EqualOption) bool {
	coreOpts := make([]core.EqualOption, len(opts))
	for i, opt := range opts {
		coreOpts[i] = opt.asCoreEqualOption()
	}
	return core.Equal(a, b, coreOpts...)
}
