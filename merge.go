package deep

import (
	"fmt"
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
	err := patchA.Walk(func(path string, op OpKind, old, new any) error {
		opsA = append(opsA, opInfo{path, op, old, new})
		return nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("walk A failed: %w", err)
	}

	var opsB []opInfo
	err = patchB.Walk(func(path string, op OpKind, old, new any) error {
		opsB = append(opsB, opInfo{path, op, old, new})
		return nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("walk B failed: %w", err)
	}

	// Detect conflicts and nested modifications
	mapA := make(map[string]opInfo)
	for _, op := range opsA {
		mapA[op.path] = op
	}

	for _, b := range opsB {
		if a, ok := mapA[b.path]; ok {
			if a.op != b.op || !Equal(a.new, b.new) {
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
	mergedOps := make(map[string]opInfo)
	for _, a := range opsA {
		mergedOps[a.path] = a
	}
	for _, b := range opsB {
		if _, ok := mergedOps[b.path]; !ok {
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

	// Sort by kind and path to ensure parents are created before children,
	// and that Copy/Move happen before Remove.
	sort.Slice(finalOps, func(i, j int) bool {
		ki := opPriority(finalOps[i].op)
		kj := opPriority(finalOps[j].op)
		if ki != kj {
			return ki < kj
		}
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
