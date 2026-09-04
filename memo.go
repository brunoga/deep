package deep

import (
	"log/slog"

	"github.com/brunoga/deep/v5/gen"
	"github.com/brunoga/deep/v5/internal/engine"
)

// The bookkeeping that generated code threads through its methods lives in
// [github.com/brunoga/deep/v5/gen]. It is aliased here because generated code
// written before that package existed refers to it by these names, and those
// files are checked into your repository rather than regenerated on every
// build.
//
// Nothing below is meant to be called by hand. Regenerating picks up the new
// names; until then, these keep working.

// CloneMemo is an alias for [gen.CloneMemo].
//
// Deprecated: use [gen.CloneMemo]. Regenerating with deep-gen updates the
// generated code that refers to this.
type CloneMemo = gen.CloneMemo

// VisitSet is an alias for [gen.VisitSet].
//
// Deprecated: use [gen.VisitSet]. Regenerating with deep-gen updates the
// generated code that refers to this.
type VisitSet = gen.VisitSet

// DiffMemo is an alias for [gen.DiffMemo].
//
// Deprecated: use [gen.DiffMemo]. Regenerating with deep-gen updates the
// generated code that refers to this.
type DiffMemo = gen.DiffMemo

// NewCloneMemo is an alias for [gen.NewCloneMemo].
//
// Deprecated: use [gen.NewCloneMemo].
func NewCloneMemo() *gen.CloneMemo { return gen.NewCloneMemo() }

// NewVisitSet is an alias for [gen.NewVisitSet].
//
// Deprecated: use [gen.NewVisitSet].
func NewVisitSet() *gen.VisitSet { return gen.NewVisitSet() }

// NewDiffMemo is an alias for [gen.NewDiffMemo].
//
// Deprecated: use [gen.NewDiffMemo].
func NewDiffMemo() *gen.DiffMemo { return gen.NewDiffMemo() }

// CloneShared is an alias for [gen.CloneShared].
//
// Deprecated: use [gen.CloneShared].
func CloneShared[T any](v T, memo *gen.CloneMemo) T { return gen.CloneShared(v, memo) }

// ApplyOpReflection applies a single Operation to target using reflection.
//
// Deprecated: use [gen.ApplyOpReflection]. This was never part of the
// user-facing API; it is what a generated Patch method calls for operations
// its fast path does not model. Prefer [Apply].
func ApplyOpReflection[T any](target *T, op Operation, logger *slog.Logger) error {
	return engine.ApplyOpReflection(target, op, logger)
}
