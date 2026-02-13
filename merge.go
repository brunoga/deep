package deep

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// Conflict represents a conflict between two patches during a Merge operation.
type Conflict struct {
	Path    string
	OpA     OpKind
	ValA    any
	OpB     OpKind
	ValB    any
	Message string
}

func (c Conflict) String() string {
	return fmt.Sprintf("conflict at %s: %s (A: %v %v, B: %v %v)", c.Path, c.Message, c.OpA, c.ValA, c.OpB, c.ValB)
}

type opInfo struct {
	path string
	op   OpKind
	old  any
	new  any
}

// Merge combines two patches into a single patch.
// If both patches modify the same field to different values, a conflict is recorded.
// patchA and patchB are assumed to be derived from the same base state.
func Merge[T any](patchA, patchB Patch[T]) (Patch[T], []Conflict, error) {
	if patchA == nil {
		return patchB, nil, nil
	}
	if patchB == nil {
		return patchA, nil, nil
	}

	builder := NewBuilder[T]()
	var conflicts []Conflict

	var opsA []opInfo
	patchA.Walk(func(path string, op OpKind, old, new any) error {
		opsA = append(opsA, opInfo{path, op, old, new})
		return nil
	})

	var opsB []opInfo
	patchB.Walk(func(path string, op OpKind, old, new any) error {
		opsB = append(opsB, opInfo{path, op, old, new})
		return nil
	})

	// Detect conflicts and nested modifications
	mapA := make(map[string]opInfo)
	for _, op := range opsA {
		mapA[op.path] = op
	}

	mapB := make(map[string]opInfo)
	for _, op := range opsB {
		mapB[op.path] = op
	}

	for _, b := range opsB {
		if a, ok := mapA[b.path]; ok {
			if a.op != b.op || !reflect.DeepEqual(a.new, b.new) {
				conflicts = append(conflicts, Conflict{
					Path:    b.path,
					OpA:     a.op,
					ValA:    a.new,
					OpB:     b.op,
					ValB:    b.new,
					Message: "concurrent modification",
				})
			}
		}

		// Check if B modifies something that A removed or replaced
		for _, a := range opsA {
			if (a.op == OpRemove || a.op == OpReplace) && isSubPath(a.path, b.path) {
				conflicts = append(conflicts, Conflict{
					Path:    b.path,
					OpA:     a.op,
					ValA:    a.new,
					OpB:     b.op,
					ValB:    b.new,
					Message: "modification of a removed or replaced parent",
				})
			}
		}
	}

	// Vice-versa: Check if A modifies something that B removed or replaced
	for _, a := range opsA {
		for _, b := range opsB {
			if (b.op == OpRemove || b.op == OpReplace) && isSubPath(b.path, a.path) {
				conflicts = append(conflicts, Conflict{
					Path:    a.path,
					OpA:     a.op,
					ValA:    a.new,
					OpB:     b.op,
					ValB:    b.new,
					Message: "modification of a removed or replaced parent",
				})
			}
		}
	}

	// Combine and sort all non-conflicting ops
	// For conflicts, we'll prefer A's version by default
	mergedOps := make(map[string]opInfo)
	for _, a := range opsA {
		mergedOps[a.path] = a
	}
	for _, b := range opsB {
		if _, ok := mergedOps[b.path]; !ok {
			// Only add if no conflict with A's path or any of A's parent removals/replacements
			hasConflict := false
			for _, a := range opsA {
				if (a.op == OpRemove || a.op == OpReplace) && isSubPath(a.path, b.path) {
					hasConflict = true
					break
				}
			}
			if !hasConflict {
				mergedOps[b.path] = b
			}
		}
	}

	var finalOps []opInfo
	for _, op := range mergedOps {
		finalOps = append(finalOps, op)
	}

	// Sort by path to ensure parents are created before children
	// and slice indices are applied in order.
	sort.Slice(finalOps, func(i, j int) bool {
		return finalOps[i].path < finalOps[j].path
	})

	for _, op := range finalOps {
		if err := applyToBuilder(builder, op); err != nil {
			return nil, nil, err
		}
	}

	merged, err := builder.Build()
	return merged, conflicts, err
}

func isSubPath(parent, child string) bool {
	if parent == child {
		return false
	}
	if parent == "/" {
		return true
	}
	prefix := parent
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	return strings.HasPrefix(child, prefix)
}

func applyToBuilder[T any](b *Builder[T], op opInfo) error {
	// Navigate to parent if it's an addition or removal in a map/slice
	parts := parsePath(op.path)
	if len(parts) > 0 {
		parentParts := parts[:len(parts)-1]
		lastPart := parts[len(parts)-1]

		parentNode, err := b.Root().NavigateParts(parentParts)
		if err == nil && parentNode.typ != nil {
			kind := parentNode.typ.Kind()
			if kind == reflect.Map {
				var key any
				if lastPart.isIndex {
					// Map key could be an int
					key = lastPart.index
				} else {
					key = lastPart.key
				}

				if op.op == OpAdd {
					return parentNode.AddMapEntry(key, op.new)
				}
				if op.op == OpRemove {
					return parentNode.Delete(key, op.old)
				}
			} else if kind == reflect.Slice {
				if lastPart.isIndex {
					if op.op == OpAdd {
						return parentNode.Add(lastPart.index, op.new)
					}
					if op.op == OpRemove {
						return parentNode.Delete(lastPart.index, op.old)
					}
				}
			}
		}
	}

	node, err := b.Root().Navigate(op.path)
	if err != nil {
		return err
	}

	switch op.op {
	case OpAdd, OpReplace:
		if op.old != nil {
			node.Set(op.old, op.new)
		} else {
			node.Put(op.new)
		}
	case OpRemove:
		return node.Remove(op.old)
	case OpMove:
		if from, ok := op.old.(string); ok {
			node.Move(from)
		}
	case OpCopy:
		if from, ok := op.old.(string); ok {
			node.Copy(from)
		}
	case OpTest:
		node.Test(op.new)
	case OpLog:
		if msg, ok := op.new.(string); ok {
			node.Log(msg)
		}
	}
	return nil
}
