package deep

import (
	"fmt"

	"github.com/brunoga/deep/v3/internal/core"
)

// applyToBuilder recursively applies an operation to a PatchBuilder.
// This is used by patch.Reverse and patch.Merge to construct new patches.
func applyToBuilder[T any](b *PatchBuilder[T], op OpInfo) error {
	// Apply conditions if present.
	if op.Conditions != nil {
		// Placeholder for condition re-attachment
	}

	switch op.Kind {
	case OpReplace:
		b.Navigate(op.Path).Put(op.Val)

	case OpAdd:
		parentPath, lastPart, err := core.DeepPath(op.Path).ResolveParentPath()
		if err != nil {
			return fmt.Errorf("invalid path for Add %s: %w", op.Path, err)
		}

		node := b.Navigate(string(parentPath))
		if lastPart.IsIndex {
			node.Add(lastPart.Index, op.Val)
		} else {
			node.Add(lastPart.Key, op.Val)
		}

	case OpRemove:
		parentPath, lastPart, err := core.DeepPath(op.Path).ResolveParentPath()
		if err != nil {
			return fmt.Errorf("invalid path for Remove %s: %w", op.Path, err)
		}

		node := b.Navigate(string(parentPath))
		if lastPart.IsIndex {
			node.Delete(lastPart.Index, op.Val)
		} else {
			node.Delete(lastPart.Key, op.Val)
		}

	case OpMove:
		b.Navigate(op.Path).Move(op.From)

	case OpCopy:
		b.Navigate(op.Path).Copy(op.From)

	case OpTest:
		b.Navigate(op.Path).Test(op.Val)

	case OpLog:
		if msg, ok := op.Val.(string); ok {
			b.Navigate(op.Path).Log(msg)
		}
	}

	if b.state.err != nil {
		return b.state.err
	}
	return nil
}

// OpInfo represents a flattened operation from a patch.
type OpInfo struct {
	Kind       OpKind
	Path       string
	From       string // For Move/Copy
	Val        any
	Conditions any // Placeholder
}
