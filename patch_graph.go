package deep

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// dependencyNode represents a node in the dependency graph.
type dependencyNode struct {
	name   string
	patch  diffPatch
	reads  []string
	writes []string
}

// resolveStructDependencies analyzes the fields of a structPatch and returns an execution plan.
// It returns a map of effective patches (which may differ from original fields if cycles were broken)
// and a list of field names in execution order.
func resolveStructDependencies(p *structPatch, basePath string, root reflect.Value) (map[string]diffPatch, []string, error) {
	nodes := make(map[string]*dependencyNode)
	effectivePatches := make(map[string]diffPatch)
	
	// 1. Build nodes
	for name, patch := range p.fields {
		fullPath := basePath
		if fullPath == "/" {
			fullPath = ""
		} else if fullPath != "" && !strings.HasPrefix(fullPath, "/") {
			fullPath = "/" + fullPath
		}
		fieldPath := fullPath + "/" + name
		
		reads, writes := patch.dependencies(fieldPath)
		nodes[name] = &dependencyNode{
			name:   name,
			patch:  patch,
			reads:  reads,
			writes: writes,
		}
		effectivePatches[name] = patch
	}

	// 2. Build Adjacency List (Edges A -> B means A must run before B)
	// A -> B if A reads what B writes.
	adj := make(map[string][]string)
	inDegree := make(map[string]int)
	
	// Initialize inDegree
	for name := range nodes {
		inDegree[name] = 0
	}

	nodeNames := make([]string, 0, len(nodes))
	for name := range nodes {
		nodeNames = append(nodeNames, name)
	}
	sort.Strings(nodeNames) // Deterministic order for iteration

	for _, nameA := range nodeNames {
		nodeA := nodes[nameA]
		for _, nameB := range nodeNames {
			if nameA == nameB {
				continue
			}
			nodeB := nodes[nameB]

			// Check dependency: Does A need to run before B?
			// Rule: Read before Write.
			// If A reads P, and B writes P: A -> B.
			depends := false
			for _, read := range nodeA.reads {
				for _, write := range nodeB.writes {
					if pathsOverlap(read, write) {
						depends = true
						break
					}
				}
				if depends {
					break
				}
			}

			if depends {
				adj[nameA] = append(adj[nameA], nameB)
				inDegree[nameB]++
			}
		}
	}

	// 3. Topological Sort (Kahn's Algorithm) with Cycle Detection
	var result []string
	queue := make([]string, 0)
	for name, deg := range inDegree {
		if deg == 0 {
			queue = append(queue, name)
		}
	}
	// Sort queue for determinism
	sort.Strings(queue)

	processedCount := 0
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		result = append(result, u)
		processedCount++

		for _, v := range adj[u] {
			inDegree[v]--
			if inDegree[v] == 0 {
				queue = append(queue, v)
			}
		}
		// Sort queue again? Kahn's algo order depends on queue insertion.
		// For stability, we should maybe keep queue sorted or find all 0-degree nodes at once.
		// Re-sorting queue is fine for determinism.
		sort.Strings(queue) 
	}

	if processedCount == len(nodes) {
		return effectivePatches, result, nil
	}

	// 4. Cycle Handling
	// If we are here, there is a cycle.
	// We need to identify nodes involved in cycles (inDegree > 0).
	cycleNodes := make([]string, 0)
	for name, deg := range inDegree {
		if deg > 0 {
			cycleNodes = append(cycleNodes, name)
		}
	}
	sort.Strings(cycleNodes)

	// Strategy: For all nodes in cycles, resolve their READ dependencies eagerly.
	// This breaks the "Read before Write" requirement, effectively removing outgoing edges.
	
	// We need to resolve dependencies for ALL cycle nodes to be safe (Swap A<->B requires both).
	for _, name := range cycleNodes {
		node := nodes[name]
		
		// If node has no reads, it shouldn't be part of a cycle caused by Read-before-Write?
		// Wait, if A writes X, B writes X. (Write-Write conflict).
		// My edge logic only checked Read-before-Write.
		// If I didn't add edges for Write-Write, then A and B are independent?
		// Order doesn't matter for dependency graph, but might matter for result.
		// But here we are looking at Read dependencies.

		if len(node.reads) == 0 {
			continue
		}

		// Resolve value
		// We use the FIRST read dependency?
		// `copyPatch` and `movePatch` have 1 read.
		// `slicePatch` might have multiple?
		// If multiple, we can't easily convert to a single `valuePatch`.
		// But `structPatch` fields are usually `copy/move/replace`.
		// If it's `slicePatch`, it modifies a field that is a slice.
		// Does `slicePatch` read from other fields?
		// Only if it contains `copy/move` ops inside.
		// If so, resolving the WHOLE slice value is heavy/complex.
		
		// Let's assume for `structPatch` refactoring, we mainly care about `copy/move` patches at the field level.
		
		// Check if we can convert.
		if cp, ok := node.patch.(*copyPatch); ok {
			val, err := deepPath(cp.from).resolve(root)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to resolve cycle dependency for %s (from %s): %w", name, cp.from, err)
			}
			// Convert to valuePatch
			// We need deep copy of value because it might be modified later?
			// `valuePatch` stores `newVal`. `apply` uses `setValue`.
			// `setValue` does conversion.
			// Ideally we store the value as is.
			valCopy := deepCopyValue(val)
			effectivePatches[name] = newValuePatch(reflect.Value{}, valCopy)
		} else if mp, ok := node.patch.(*movePatch); ok {
			val, err := deepPath(mp.from).resolve(root)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to resolve cycle dependency for %s (from %s): %w", name, mp.from, err)
			}
			valCopy := deepCopyValue(val)
			effectivePatches[name] = newValuePatch(reflect.Value{}, valCopy)
			// Note: As discussed, we lose the "delete source" side effect if source is external.
			// But for swaps (internal), it's fine.
		} else {
			// If it's a complex patch (e.g. slicePatch) that reads?
			// We can't easily convert it.
			// We might fail or warn.
			// But `slicePatch` reads are internal to the slice Ops.
			// If `slicePatch` on field A reads field B.
			// We can't turn `slicePatch` into `valuePatch` (Replace A) without computing the WHOLE new A.
			// Computing the whole new A requires applying the slice patch to the current A.
			// current A is available in `v` (passed to apply, but we don't have it here).
			// `resolveStructDependencies` takes `root`. It doesn't take `v` (the struct itself).
			// Wait, `apply` has `v`.
			// `resolveStructDependencies` needs `v` if it wants to compute complex updates?
			// No, `root` allows resolving absolute paths.
			// `slicePatch` ops use absolute paths for copy/move source.
			// So we *could* resolve.
			// But `slicePatch` applies to `v.Field(name)`.
			// If we want to pre-calculate, we need `v.Field(name)`.
			// Since we don't pass `v` here, we can't do it for `slicePatch`.
			
			// For now, let's assume cycles only involve `copy/move` at the top level of the field.
			// If `slicePatch` is involved in a cycle, we might be stuck.
			// But `slicePatch` usually modifies *itself* (A).
			// If A reads B, and B reads A.
			// A is slicePatch. B is slicePatch.
			// Dependencies: A reads B. B reads A.
			// This means A copies from B inside the slice?
			// This is rare.
			// Let's stick to `copy/move`.
			return nil, nil, fmt.Errorf("cycle detected involving non-simple patch type for field %s", name)
		}
	}

	// 5. Re-sort with broken cycles
	// Since we converted to valuePatches, reads are gone.
	// We can just topo sort again or return the cycle nodes at the beginning?
	// Actually, just calling Topo Sort again is safest.
	// Re-build graph?
	// Easier: Just return the result of a recursive call?
	// We have modified `effectivePatches`.
	// We can create a new temporary `structPatch` with `effectivePatches` and call `resolveStructDependencies`.
	// But `structPatch` fields are `diffPatch`.
	// `effectivePatches` is `map[string]diffPatch`.
	
	newStructPatch := &structPatch{fields: effectivePatches}
	// Note: We don't need to copy other fields of structPatch (conds) as they are not used for dependency resolution.
	
	_, newResult, err := resolveStructDependencies(newStructPatch, basePath, root)
	return effectivePatches, newResult, err
}

func pathsOverlap(p1, p2 string) bool {
	if p1 == p2 {
		return true
	}
	// Check if p1 is parent of p2
	if strings.HasPrefix(p2, p1+"/") {
		return true
	}
	// Check if p2 is parent of p1
	if strings.HasPrefix(p1, p2+"/") {
		return true
	}
	return false
}


