package deep

import (
	"fmt"

	"github.com/brunoga/deep/v3/internal/core"
)

// applyToBuilder recursively applies an operation to a PatchBuilder.
// This is used by patch.Reverse and patch.Merge to construct new patches.
func applyToBuilder[T any](b *PatchBuilder[T], op opInfo) error {
	// Apply conditions if present.
	if op.Conditions != nil {
		// Placeholder for condition re-attachment
	}

	switch op.Kind {
	case OpReplace:
		node, err := b.Root().Navigate(op.Path)
		if err != nil {
			return fmt.Errorf("failed to navigate to %s: %w", op.Path, err)
		}
		node.Put(op.Val)

	case OpAdd:
		// Navigate to parent to call Add/AddMapEntry
		parentPath, lastPart, err := core.DeepPath(op.Path).ResolveParentPath()
		if err != nil {
			return fmt.Errorf("invalid path for Add %s: %w", op.Path, err)
		}

		node, err := b.Root().Navigate(string(parentPath))
		if err != nil {
			return fmt.Errorf("failed to navigate to parent %s: %w", parentPath, err)
		}

		if lastPart.IsIndex {
			err = node.Add(lastPart.Index, op.Val)
		} else {
			err = node.AddMapEntry(lastPart.Key, op.Val)
		}
		if err != nil {
			return fmt.Errorf("failed to apply Add at %s: %w", op.Path, err)
		}

	case OpRemove:
		// Navigate to parent to call Delete
		parentPath, lastPart, err := core.DeepPath(op.Path).ResolveParentPath()
		if err != nil {
			return fmt.Errorf("invalid path for Remove %s: %w", op.Path, err)
		}

		node, err := b.Root().Navigate(string(parentPath))
		if err != nil {
			return fmt.Errorf("failed to navigate to parent %s: %w", parentPath, err)
		}

		if lastPart.IsIndex {
			err = node.Delete(lastPart.Index, op.Val)
		} else {
			err = node.Delete(lastPart.Key, op.Val)
		}
		if err != nil {
			return fmt.Errorf("failed to apply Remove at %s: %w", op.Path, err)
		}

	case OpMove:
		node, err := b.Root().Navigate(op.Path)
		if err != nil {
			return fmt.Errorf("failed to navigate to %s: %w", op.Path, err)
		}
		node.Move(op.From)

	case OpCopy:
		node, err := b.Root().Navigate(op.Path)
		if err != nil {
			return fmt.Errorf("failed to navigate to %s: %w", op.Path, err)
		}
		node.Copy(op.From)

	case OpTest:
		node, err := b.Root().Navigate(op.Path)
		if err != nil {
			return fmt.Errorf("failed to navigate to %s: %w", op.Path, err)
		}
		node.Test(op.Val)

	case OpLog:
		node, err := b.Root().Navigate(op.Path)
		if err != nil {
			return fmt.Errorf("failed to navigate to %s: %w", op.Path, err)
		}
		if msg, ok := op.Val.(string); ok {
			node.Log(msg)
		}
	}
	return nil
}

// opInfo represents a flattened operation from a patch.
type opInfo struct {
	Kind       OpKind
	Path       string
	From       string // For Move/Copy
	Val        any
	Conditions any // Placeholder
}
