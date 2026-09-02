// Package testmodels_test checks that the two implementations of every
// operation agree. deep dispatches to a type's generated methods when it has
// them and to the reflection engine when it does not, so the two are only
// interchangeable if they produce the same result for the same input — the same
// copy, the same verdict, and the same value after a diff is applied.
//
// The models cover the shapes that make the two paths easy to disagree on:
// embedded fields, keyed slices, nested and pointer-valued maps, atomic fields,
// values that reach themselves, and values that reach the same node twice.
package testmodels_test

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	deep "github.com/brunoga/deep/v5"
	"github.com/brunoga/deep/v5/internal/engine"
	"github.com/brunoga/deep/v5/internal/testmodels"
	"github.com/brunoga/deep/v5/internal/testmodels/external"
	"github.com/brunoga/deep/v5/internal/testmodels/graph"
	"github.com/brunoga/deep/v5/internal/testmodels/shapes"
	iunsafe "github.com/brunoga/deep/v5/internal/unsafe"
)

// generated is the set of methods deep-gen emits that deep dispatches to. A
// type satisfying it has a fast path, so both implementations are available for
// it and can be compared.
type generated[T any] interface {
	*T
	Clone() *T
	Equal(*T) bool
	Diff(*T) deep.Patch[T]
}

// ── structural fingerprint ───────────────────────────────────────────────────

type ptrKey struct {
	ptr uintptr
	typ reflect.Type
}

// shape renders v as a string capturing its structure, its values, and which
// parts of it are shared. The first time a pointer is reached it is written out
// in full behind an id; every later reference to the same pointer is written as
// that id alone.
//
// That is what makes this stronger than equality for the question at hand. Two
// values compare equal whether a node reached twice was copied once or twice,
// and equality cannot be asked at all of a value that references itself. Their
// shapes differ in the first case and are finite in the second.
//
// Ids are numbered in first-visit order within one call, so a shape says
// nothing about identity between two separate values — only about sharing
// inside each of them, which is what has to match.
func shape(v any) string {
	var b strings.Builder
	writeShape(&b, reflect.ValueOf(v), make(map[ptrKey]int))
	return b.String()
}

func writeShape(b *strings.Builder, v reflect.Value, ids map[ptrKey]int) {
	if !v.IsValid() {
		b.WriteString("invalid")
		return
	}

	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			b.WriteString("nil")
			return
		}
		key := ptrKey{v.Pointer(), v.Type()}
		if id, ok := ids[key]; ok {
			// Reached before: a cycle, or a node the value shares.
			fmt.Fprintf(b, "@%d", id)
			return
		}
		id := len(ids) + 1
		ids[key] = id
		fmt.Fprintf(b, "*%d(", id)
		writeShape(b, v.Elem(), ids)
		b.WriteString(")")

	case reflect.Interface:
		if v.IsNil() {
			b.WriteString("nil")
			return
		}
		writeShape(b, v.Elem(), ids)

	case reflect.Struct:
		b.WriteString("{")
		for i := 0; i < v.NumField(); i++ {
			if i > 0 {
				b.WriteString(" ")
			}
			f := v.Field(i)
			if !f.CanInterface() {
				iunsafe.DisableRO(&f)
			}
			fmt.Fprintf(b, "%s:", v.Type().Field(i).Name)
			writeShape(b, f, ids)
		}
		b.WriteString("}")

	case reflect.Slice:
		if v.IsNil() {
			b.WriteString("nil")
			return
		}
		fallthrough
	case reflect.Array:
		b.WriteString("[")
		for i := 0; i < v.Len(); i++ {
			if i > 0 {
				b.WriteString(" ")
			}
			writeShape(b, v.Index(i), ids)
		}
		b.WriteString("]")

	case reflect.Map:
		if v.IsNil() {
			b.WriteString("nil")
			return
		}
		keys := v.MapKeys()
		rendered := make([]string, len(keys))
		order := make([]int, len(keys))
		for i, k := range keys {
			rendered[i] = fmt.Sprintf("%v", k)
			order[i] = i
		}
		sort.Slice(order, func(i, j int) bool { return rendered[order[i]] < rendered[order[j]] })
		b.WriteString("map[")
		for n, i := range order {
			if n > 0 {
				b.WriteString(" ")
			}
			fmt.Fprintf(b, "%s:", rendered[i])
			writeShape(b, v.MapIndex(keys[i]), ids)
		}
		b.WriteString("]")

	case reflect.Chan, reflect.Func, reflect.UnsafePointer:
		if v.IsNil() {
			b.WriteString("nil")
		} else {
			b.WriteString("<non-nil " + v.Kind().String() + ">")
		}

	default:
		fmt.Fprintf(b, "%v", v)
	}
}

// ── parity checks ────────────────────────────────────────────────────────────

// checkParity runs every operation both ways over a and b and reports any
// disagreement.
func checkParity[T any, PT generated[T]](t *testing.T, name string, a, b T) {
	t.Helper()

	t.Run(name+"/clone", func(t *testing.T) {
		for _, v := range []*T{&a, &b} {
			gen := PT(v).Clone()
			ref, err := engine.Copy(v)
			if err != nil {
				t.Fatalf("reflection copy: %v", err)
			}
			if got, want := shape(gen), shape(ref); got != want {
				t.Errorf("clone differs\ngenerated:  %s\nreflection: %s", got, want)
			}
			// A copy that shares nothing with its source is the point of a
			// deep copy, so check the source is unchanged by comparing it to
			// the copy it should still match.
			if got, want := shape(v), shape(gen); got != want {
				t.Errorf("clone is not a faithful copy of its source\nsource: %s\nclone:  %s", got, want)
			}
		}
	})

	t.Run(name+"/equal", func(t *testing.T) {
		pairs := []struct {
			name string
			x, y *T
		}{
			{"a,a", &a, &a},
			{"a,b", &a, &b},
			{"b,a", &b, &a},
			{"b,b", &b, &b},
		}
		for _, p := range pairs {
			gen := PT(p.x).Equal(p.y)
			ref := engine.Equal(*p.x, *p.y)
			if gen != ref {
				t.Errorf("%s: generated Equal = %v, reflection = %v", p.name, gen, ref)
			}
		}
		// A clone must compare equal to its source under both.
		clone := PT(&a).Clone()
		if !PT(&a).Equal(clone) {
			t.Error("generated Equal says a clone differs from its source")
		}
		if !engine.Equal(a, *clone) {
			t.Error("reflection Equal says a clone differs from its source")
		}
	})

	t.Run(name+"/diff-apply", func(t *testing.T) {
		genPatch := PT(&a).Diff(&b)
		refPatch, err := engine.Diff(a, b)
		if err != nil {
			t.Fatalf("reflection diff: %v", err)
		}

		// Both patches are applied to their own copy of a. What has to match is
		// where they land, not how they get there: the two engines are free to
		// describe the same change with different operations.
		genTarget := *PT(&a).Clone()
		if err := deep.Apply(&genTarget, genPatch); err != nil {
			t.Fatalf("applying generated patch: %v", err)
		}

		refTarget := *PT(&a).Clone()
		refPatch.Apply(&refTarget)

		// Equality, not shape, is the contract here. A patch addresses values
		// by path and has no way to say that two paths must end up pointing at
		// one object, so applying one to a fresh target cannot always rebuild
		// the sharing the target value had. What it must rebuild is the value.
		if !engine.Equal(genTarget, refTarget) {
			t.Errorf("diff+apply lands somewhere different\ngenerated:  %s\nreflection: %s",
				shape(&genTarget), shape(&refTarget))
		}
		if !engine.Equal(genTarget, b) {
			t.Errorf("generated diff+apply did not reach b\ngot:  %s\nwant: %s", shape(&genTarget), shape(&b))
		}
		if !engine.Equal(refTarget, b) {
			t.Errorf("reflection diff+apply did not reach b\ngot:  %s\nwant: %s", shape(&refTarget), shape(&b))
		}
	})
}

// ── models ───────────────────────────────────────────────────────────────────

func TestParityFlatStruct(t *testing.T) {
	a := testmodels.User{
		ID:    1,
		Name:  "ann",
		Info:  testmodels.Detail{Age: 30, Address: "here"},
		Roles: []string{"admin", "dev"},
		Score: map[string]int{"a": 1},
	}
	b := testmodels.User{
		ID:    2,
		Name:  "bob",
		Info:  testmodels.Detail{Age: 31, Address: "there"},
		Roles: []string{"admin", "ops"},
		Score: map[string]int{"a": 2, "b": 3},
	}
	checkParity[testmodels.User](t, "User", a, b)
}

func TestParityStructuralShapes(t *testing.T) {
	mk := func(version int, name string) shapes.Doc {
		return shapes.Doc{
			Meta:    shapes.Meta{Version: version, Author: "ann"},
			Entries: []shapes.Entry{{ID: "a", N: 1}, {ID: "b", N: 2}},
			Nested:  map[string]map[string]int{"outer": {"inner": version}},
			Stages:  map[string]*shapes.Meta{"one": {Version: version, Author: "zed"}},
			Side:    &shapes.Meta{Version: version, Author: "side"},
			Blob:    shapes.Payload{Bytes: []byte{1, 2}, Label: "blob"},
			Scores:  map[string]int{"s": version},
			Name:    name,
		}
	}
	checkParity[shapes.Doc](t, "Doc", mk(1, "first"), mk(2, "second"))

	// Empty and nil collections next to populated ones: the two paths have to
	// agree on nil versus empty, not just on contents.
	checkParity[shapes.Doc](t, "Doc/empty-to-full", shapes.Doc{Name: "bare"}, mk(3, "full"))
	checkParity[shapes.Doc](t, "Doc/full-to-empty", mk(4, "full"), shapes.Doc{Name: "bare"})
}

func TestParityExternalTypes(t *testing.T) {
	at := time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC)
	later := at.Add(time.Hour)
	// Distinct pointers with equal contents: pointer sharing between fields is
	// covered on its own in TestSharedForeignPointersAreNotDeduplicated.
	atRetry, laterRetry := at, later
	a := external.Job{
		StartAt:  &at,
		Deadline: at,
		Timeout:  30 * time.Second,
		Window:   []time.Time{at},
		Retries:  map[string]*time.Time{"first": &atRetry},
		Stages:   []external.Stage{{Name: "build", At: at}},
		Owners:   map[string]*external.Stage{"lead": {Name: "ann", At: at}},
		Priority: 1,
		Grid:     [2]int{1, 2},
	}
	b := external.Job{
		StartAt:  &later,
		Deadline: later,
		Timeout:  time.Minute,
		Window:   []time.Time{later},
		Retries:  map[string]*time.Time{"first": &laterRetry},
		Stages:   []external.Stage{{Name: "test", At: later}},
		Owners:   map[string]*external.Stage{"lead": {Name: "bob", At: later}},
		Priority: 2,
		Grid:     [2]int{3, 4},
	}
	checkParity[external.Job](t, "Job", a, b)
}

// ── recursive models ─────────────────────────────────────────────────────────

// selfCycle returns a Node whose Next points at a node that points back at
// itself, so the value reaches a cycle one step below its root.
func selfCycle(name string, rank int) graph.Node {
	inner := &graph.Node{Name: name + "-inner", Label: graph.Label{Text: name, Rank: rank}}
	inner.Next = inner
	return graph.Node{Name: name, Label: graph.Label{Text: name, Rank: rank}, Next: inner}
}

func TestParityCyclicThroughPointer(t *testing.T) {
	checkParity[graph.Node](t, "Node/self-cycle", selfCycle("a", 1), selfCycle("b", 2))
}

func TestParityCyclicThroughSlice(t *testing.T) {
	mk := func(name string, rank int) graph.Node {
		n := &graph.Node{Name: name, Label: graph.Label{Text: name, Rank: rank}}
		n.Peers = []*graph.Node{n, {Name: name + "-leaf"}}
		return graph.Node{Name: name + "-root", Peers: []*graph.Node{n}}
	}
	checkParity[graph.Node](t, "Node/slice-cycle", mk("a", 1), mk("b", 2))
}

func TestParityCyclicThroughMap(t *testing.T) {
	mk := func(name string, rank int) graph.Node {
		n := &graph.Node{Name: name, Label: graph.Label{Text: name, Rank: rank}}
		n.Children = map[string]*graph.Node{"self": n, "leaf": {Name: name + "-leaf"}}
		return graph.Node{Name: name + "-root", Children: map[string]*graph.Node{"child": n}}
	}
	checkParity[graph.Node](t, "Node/map-cycle", mk("a", 1), mk("b", 2))
}

func TestParityCyclicThroughValueStructs(t *testing.T) {
	// The cycle runs through a slice of structs and a map of structs, neither
	// of which is a pointer: the recursion goes through the *Node those
	// structs hold.
	mk := func(name string, weight int) graph.Node {
		n := &graph.Node{Name: name}
		n.Edges = []graph.Edge{{Weight: weight, To: n}}
		n.Links = map[string]graph.Edge{"back": {Weight: weight, To: n}, "nil": {Weight: 0}}
		return graph.Node{
			Name:  name + "-root",
			Edges: []graph.Edge{{Weight: weight, To: n}},
			Links: map[string]graph.Edge{"in": {Weight: weight, To: n}},
		}
	}
	checkParity[graph.Node](t, "Node/value-struct-cycle", mk("a", 1), mk("b", 2))
}

func TestParitySharedNodeReachedTwice(t *testing.T) {
	// No cycle at all — just one node reached by two different routes. The two
	// paths must both copy it once and point both routes at that one copy.
	mk := func(name string, rank int) graph.Node {
		shared := &graph.Node{Name: name + "-shared", Label: graph.Label{Text: name, Rank: rank}}
		return graph.Node{
			Name:     name,
			Next:     shared,
			Peers:    []*graph.Node{shared, shared},
			Children: map[string]*graph.Node{"a": shared, "b": shared},
		}
	}
	checkParity[graph.Node](t, "Node/shared", mk("a", 1), mk("b", 2))
}

func TestParityReferenceTypesWithoutCycles(t *testing.T) {
	// Plain maps and slices of non-recursive elements, including nil versus
	// empty, which the two paths must agree about.
	mk := func(tags map[string]string, scores []int) graph.Node {
		return graph.Node{Name: "plain", Tags: tags, Scores: scores}
	}
	checkParity[graph.Node](t, "Node/nil-to-populated",
		mk(nil, nil), mk(map[string]string{"k": "v"}, []int{1, 2, 3}))
	checkParity[graph.Node](t, "Node/empty-to-populated",
		mk(map[string]string{}, []int{}), mk(map[string]string{"k": "v"}, []int{1}))
	checkParity[graph.Node](t, "Node/populated-to-nil",
		mk(map[string]string{"k": "v"}, []int{1, 2}), mk(nil, nil))
	checkParity[graph.Node](t, "Node/changed-in-place",
		mk(map[string]string{"k": "v", "drop": "me"}, []int{1, 2, 3}),
		mk(map[string]string{"k": "w", "add": "me"}, []int{1, 9, 3}))
}

func TestParityNonRecursiveTypeInRecursivePackage(t *testing.T) {
	// Label cannot reach a cycle, so it keeps the plain generated methods even
	// though the package around it is recursive. It has to stay in parity too.
	checkParity[graph.Label](t, "Label",
		graph.Label{Text: "one", Rank: 1}, graph.Label{Text: "two", Rank: 2})
}

func TestSharedForeignPointersAreDeduplicated(t *testing.T) {
	// A Job holds *time.Time in a plain field and in a map. When both point at
	// one time.Time, the clone must hold one copied time.Time pointed at by
	// both routes — on the generated path exactly as on the reflection path.
	// deep-gen sees that two routes can lead to one *time.Time and threads a
	// CloneMemo through Job's Clone for it.
	at := time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC)
	job := external.Job{StartAt: &at, Retries: map[string]*time.Time{"first": &at}}

	gen := job.Clone()
	if gen.StartAt != gen.Retries["first"] {
		t.Error("generated Clone duplicated a foreign pointer reached by two routes")
	}
	if gen.StartAt == &at {
		t.Error("generated Clone shared the pointee with the source")
	}

	ref, err := engine.Copy(&job)
	if err != nil {
		t.Fatalf("reflection copy: %v", err)
	}
	if ref.StartAt != ref.Retries["first"] {
		t.Error("reflection Copy stopped preserving sharing of foreign pointers")
	}

	if got, want := shape(gen), shape(ref); got != want {
		t.Errorf("copies disagree\ngenerated:  %s\nreflection: %s", got, want)
	}
}

// ── cost of the cycle handling ───────────────────────────────────────────────

func cyclicNode() *graph.Node {
	shared := &graph.Node{Name: "shared", Label: graph.Label{Text: "s", Rank: 1}}
	n := &graph.Node{
		Name:     "root",
		Label:    graph.Label{Text: "r", Rank: 2},
		Peers:    []*graph.Node{shared, shared},
		Children: map[string]*graph.Node{"a": shared},
		Tags:     map[string]string{"k": "v"},
		Scores:   []int{1, 2, 3},
	}
	n.Next = n
	return n
}

// BenchmarkCloneCyclicGenerated measures the fast path with the memo it needs
// to handle a value that reaches itself; BenchmarkCloneAcyclicGenerated
// measures a type in the same package that cannot, and so is generated without
// one.
func BenchmarkCloneCyclicGenerated(b *testing.B) {
	n := cyclicNode()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = n.Clone()
	}
}

func BenchmarkCloneCyclicReflection(b *testing.B) {
	n := cyclicNode()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = engine.Copy(n)
	}
}

func BenchmarkCloneAcyclicGenerated(b *testing.B) {
	l := &graph.Label{Text: "t", Rank: 1}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = l.Clone()
	}
}

func TestSharedPointerToMemoFreeStructIsDeduplicated(t *testing.T) {
	// Meta has nothing shareable inside, so it generates no memo of its own —
	// but a *Meta held by both a field and a map entry is still one identity.
	// The shared enclosing type wraps the call site: load from the memo or
	// clone once and record it.
	m := &shapes.Meta{Version: 7, Author: "ann"}
	doc := shapes.Doc{Side: m, Stages: map[string]*shapes.Meta{"one": m}}

	gen := doc.Clone()
	if gen.Side != gen.Stages["one"] {
		t.Error("generated Clone duplicated a shared *Meta")
	}
	if gen.Side == m {
		t.Error("generated Clone shared the Meta with the source")
	}

	ref, err := engine.Copy(&doc)
	if err != nil {
		t.Fatalf("reflection copy: %v", err)
	}
	if got, want := shape(gen), shape(ref); got != want {
		t.Errorf("copies disagree\ngenerated:  %s\nreflection: %s", got, want)
	}
}

func TestAliasSurvivesFlatJSONButNotRFC(t *testing.T) {
	// The native flat encoding round-trips alias ops; the RFC 6902 export maps
	// them to "copy", which reproduces the values but not the sharing — JSON
	// values have no identity to share.
	n := &graph.Node{Name: "old"}
	a := graph.Node{Name: "r", Next: n, Children: map[string]*graph.Node{"c": n}}
	nn := &graph.Node{Name: "new"}
	b := graph.Node{Name: "r", Next: nn, Children: map[string]*graph.Node{"c": nn}}

	p, err := deep.Diff(a, b)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	hasAlias := false
	for _, op := range p.Operations {
		if op.Kind == deep.OpAlias {
			hasAlias = true
			if op.From == "" {
				t.Error("alias op with empty From")
			}
		}
	}
	if !hasAlias {
		t.Fatal("expected an alias operation for the second route")
	}

	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back deep.Patch[graph.Node]
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	found := false
	for _, op := range back.Operations {
		if op.Kind == deep.OpAlias && op.From != "" {
			found = true
		}
	}
	if !found {
		t.Error("alias op lost in native JSON round-trip")
	}

	rfc, err := p.ToJSONPatch()
	if err != nil {
		t.Fatalf("ToJSONPatch: %v", err)
	}
	if !strings.Contains(string(rfc), `"copy"`) {
		t.Errorf("RFC export should map alias to copy, got: %s", rfc)
	}
	if strings.Contains(string(rfc), `"alias"`) {
		t.Errorf("RFC export leaked a non-RFC op kind: %s", rfc)
	}
}
