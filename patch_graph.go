package deep

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/brunoga/deep/v3/internal/core"
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
		sort.Strings(queue) 
	}

	if processedCount == len(nodes) {
		return effectivePatches, result, nil
	}

	// 4. Cycle Handling
	cycleNodes := make([]string, 0)
	for name, deg := range inDegree {
		if deg > 0 {
			cycleNodes = append(cycleNodes, name)
		}
	}
	sort.Strings(cycleNodes)

	for _, name := range cycleNodes {
		node := nodes[name]
		
		if len(node.reads) == 0 {
			continue
		}

		if cp, ok := node.patch.(*copyPatch); ok {
			val, err := core.DeepPath(cp.from).Resolve(root)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to resolve cycle dependency for %s (from %s): %w", name, cp.from, err)
			}
			valCopy := core.DeepCopyValue(val)
			effectivePatches[name] = newValuePatch(reflect.Value{}, valCopy)
		} else if mp, ok := node.patch.(*movePatch); ok {
			val, err := core.DeepPath(mp.from).Resolve(root)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to resolve cycle dependency for %s (from %s): %w", name, mp.from, err)
			}
			valCopy := core.DeepCopyValue(val)
			effectivePatches[name] = newValuePatch(reflect.Value{}, valCopy)
		} else {
			return nil, nil, fmt.Errorf("cycle detected involving non-simple patch type for field %s", name)
		}
	}

	// 5. Re-sort with broken cycles
	newStructPatch := &structPatch{fields: effectivePatches}
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
