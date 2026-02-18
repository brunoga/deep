package deep

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/brunoga/deep/v3/internal/core"
)

// Conflict represents a merge conflict where two patches modify the same path
// with different values or cause structural inconsistencies (tree conflicts).
type Conflict struct {
	Path  string
	OpA   OpInfo
	OpB   OpInfo
	Base  any
}

func (c Conflict) String() string {
	return fmt.Sprintf("conflict at %s: %v vs %v", c.Path, c.OpA.Val, c.OpB.Val)
}

// Merge combines multiple patches into a single patch.
// It detects conflicts and overlaps, including tree conflicts.
func Merge[T any](patches ...Patch[T]) (Patch[T], []Conflict, error) {
	if len(patches) == 0 {
		return nil, nil, nil
	}

	// 1. Flatten
	opsByPath := make(map[string]OpInfo)
	var conflicts []Conflict
	var orderedPaths []string

	// Track which paths are removed or moved from, to detect tree conflicts.
	removedPaths := make(map[string]int)

	for i, p := range patches {
		err := p.Walk(func(path string, kind OpKind, oldVal, newVal any) error {
			op := OpInfo{
				Kind: kind,
				Path: path,
				Val:  newVal,
			}
			if kind == OpMove || kind == OpCopy {
				op.From = oldVal.(string)
			}

			// Tree Conflict Detection 1: New op is under an already removed path
			for removed := range removedPaths {
				if path == removed {
					continue
				}
				if strings.HasPrefix(path, removed+"/") {
					conflicts = append(conflicts, Conflict{
						Path: path,
						OpA:  OpInfo{Kind: OpRemove, Path: removed},
						OpB:  op,
					})
				}
			}

			// Direct Conflict Detection
			if existing, ok := opsByPath[path]; ok {
				conflict := false
				if existing.Kind != op.Kind {
					conflict = true
				} else {
					if kind == OpMove || kind == OpCopy {
						if existing.From != op.From {
							conflict = true
						}
					} else {
						if !core.Equal(existing.Val, op.Val) {
							conflict = true
						}
					}
				}

				if conflict {
					conflicts = append(conflicts, Conflict{
						Path: path,
						OpA:  existing,
						OpB:  op,
					})
				}
				// Last wins: overwrite existing op
				opsByPath[path] = op
			} else {
				opsByPath[path] = op
				orderedPaths = append(orderedPaths, path)
			}
			
			// Track removals
			isRemoval := false
			if kind == OpRemove {
				isRemoval = true
			} else if kind == OpMove {
				removedPaths[op.From] = i
			} else if kind == OpReplace {
				if newVal == nil {
					isRemoval = true
				} else {
					v := reflect.ValueOf(newVal)
					if !v.IsValid() || ((v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface || v.Kind() == reflect.Map || v.Kind() == reflect.Slice) && v.IsNil()) {
						isRemoval = true
					}
				}
			}
			
			if isRemoval {
				removedPaths[path] = i
				
				// Tree Conflict Detection 2: This removal invalidates existing ops under it
				for existingPath, existingOp := range opsByPath {
					if existingPath == path {
						continue
					}
					if strings.HasPrefix(existingPath, path+"/") {
						conflicts = append(conflicts, Conflict{
							Path: existingPath,
							OpA:  op, // The removal
							OpB:  existingOp, // The existing modification
						})
					}
				}
			}
			
			return nil
		})
		if err != nil {
			return nil, nil, fmt.Errorf("error walking patch %d: %w", i, err)
		}
	}

	// 3. Rebuild
	builder := NewPatchBuilder[T]()
	
	// Filter out orderedPaths that were overwritten (duplicates in list? no, orderedPaths is append-only)
	// orderedPaths might contain duplicates if we added `path` multiple times?
	// Logic: `if existing ... else append`. So no duplicates in orderedPaths.
	// But `opsByPath` stores the *last* op.
	
	for _, path := range orderedPaths {
		if op, ok := opsByPath[path]; ok {
			if err := applyToBuilder(builder, op); err != nil {
				return nil, conflicts, err
			}
		}
	}

	p, err := builder.Build()
	return p, conflicts, err
}
