package deep_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/brunoga/deep/v6"
	"github.com/brunoga/deep/v6/crdt"
	"github.com/brunoga/deep/v6/crdt/hlc"
	"github.com/brunoga/deep/v6/internal/testmodels"
)

func TestCausality(t *testing.T) {
	type Doc struct {
		Title string
		Score int
	}

	nodeA := crdt.NewCRDT(Doc{Title: "Original", Score: 0}, "node-a")
	nodeB := crdt.NewCRDT(Doc{Title: "Original", Score: 0}, "node-b")

	// Node A updates Title; node B updates Score concurrently.
	deltaA := nodeA.Edit(func(d *Doc) { d.Title = "Updated" })
	deltaB := nodeB.Edit(func(d *Doc) { d.Score = 42 })

	// Both nodes apply both deltas — should converge.
	nodeA.ApplyDelta(deltaB)
	nodeB.ApplyDelta(deltaA)

	vA, vB := nodeA.View(), nodeB.View()
	if vA != vB {
		t.Errorf("nodes did not converge: A=%+v B=%+v", vA, vB)
	}
	if vA.Title != "Updated" || vA.Score != 42 {
		t.Errorf("wrong converged state: %+v", vA)
	}

	// Stale delta: applying an older edit after a newer one should be a no-op.
	stale := nodeA.Edit(func(d *Doc) { d.Title = "Stale" })
	_ = nodeA.Edit(func(d *Doc) { d.Title = "Definitive" })
	nodeA.ApplyDelta(stale)
	if nodeA.View().Title != "Definitive" {
		t.Errorf("stale delta overwrote newer update")
	}
}

func TestApplyOperation(t *testing.T) {
	u := testmodels.User{
		ID:   1,
		Name: "Alice",
		Bio:  crdt.Text{{Value: "Hello"}},
	}

	p := deep.Patch[testmodels.User]{}
	p.Operations = append(p.Operations, deep.Operation{
		Kind: deep.OpReplace,
		Path: "/full_name",
		New:  "Bob",
	})

	if err := deep.Apply(&u, p); err != nil {
		t.Fatalf("Apply failed: %v", err)
	}

	if u.Name != "Bob" {
		t.Errorf("expected Bob, got %s", u.Name)
	}
}

func TestApplyError(t *testing.T) {
	err1 := fmt.Errorf("error 1")
	err2 := fmt.Errorf("error 2")
	ae := &deep.ApplyError{Errors: []error{err1, err2}}

	s := ae.Error()
	if !strings.Contains(s, "2 errors during apply") {
		t.Errorf("expected 2 errors message, got %s", s)
	}
	if !strings.Contains(s, "error 1") || !strings.Contains(s, "error 2") {
		t.Errorf("missing individual errors in message: %s", s)
	}

	aeSingle := &deep.ApplyError{Errors: []error{err1}}
	if aeSingle.Error() != "error 1" {
		t.Errorf("expected error 1, got %s", aeSingle.Error())
	}
}

func TestNilMapDiff(t *testing.T) {
	type S struct {
		M map[string]int
	}
	// nil source map → all keys should produce OpAdd, not OpReplace
	a := S{M: nil}
	b := S{M: map[string]int{"x": 1, "y": 2}}
	p, err := deep.Diff(a, b)
	if err != nil {
		t.Fatalf("Diff failed: %v", err)
	}
	for _, op := range p.Operations {
		if op.Kind != deep.OpReplace && op.Kind != deep.OpAdd {
			continue
		}
		if op.Kind == deep.OpReplace {
			t.Errorf("Diff with nil source map should emit OpAdd, got OpReplace at %s", op.Path)
		}
	}
}

// TestOpCopyDeepCopies asserts OpCopy on a reference-typed field gives the
// destination its own backing storage, so mutations to the source no longer
// leak into the destination.
func TestOpCopyDeepCopies(t *testing.T) {
	type S struct {
		A []int
		B []int
		M map[string]int
		N map[string]int
	}
	s := &S{A: []int{1, 2, 3}, M: map[string]int{"k": 1}}
	p := deep.Patch[S]{Operations: []deep.Operation{
		{Kind: deep.OpCopy, Path: "/B", From: "/A"},
		{Kind: deep.OpCopy, Path: "/N", From: "/M"},
	}}
	if err := deep.Apply(s, p); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	s.A[0] = 99
	s.M["k"] = 99
	if s.B[0] == 99 {
		t.Errorf("OpCopy on []int aliases source: B=%v", s.B)
	}
	if s.N["k"] == 99 {
		t.Errorf("OpCopy on map aliases source: N=%v", s.N)
	}
}

func TestReflectionEngineAdvanced(t *testing.T) {
	type Data struct {
		A int
		B int
	}
	d := &Data{A: 1, B: 2}

	p := deep.Patch[Data]{}
	p.Operations = []deep.Operation{
		{Kind: deep.OpMove, Path: "/B", From: "/A"},
		{Kind: deep.OpCopy, Path: "/A", From: "/B"},
		{Kind: deep.OpRemove, Path: "/A"},
	}

	if err := deep.Apply(d, p); err != nil {
		t.Errorf("Apply failed: %v", err)
	}
}

// TestStrictRootMismatchedOldType asserts that a strict OpReplace at root
// whose Old value carries the wrong concrete type returns an error rather
// than panicking on the type assertion.
func TestStrictRootMismatchedOldType(t *testing.T) {
	u := &testmodels.User{Name: "alice"}
	p := deep.Patch[testmodels.User]{
		Strict: true,
		Operations: []deep.Operation{
			{Kind: deep.OpReplace, Path: "/", Old: "not-a-User", New: testmodels.User{Name: "bob"}},
		},
	}
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Apply panicked on mismatched Old type: %v", r)
		}
	}()
	if err := deep.Apply(u, p); err == nil {
		t.Error("expected strict check error on mismatched Old type, got nil")
	}
}

func TestEngineFailures(t *testing.T) {
	u := &testmodels.User{}

	// Move from non-existent path must surface an error rather than silently
	// no-op (previously this test ignored the return value).
	p1 := deep.Patch[testmodels.User]{}
	p1.Operations = []deep.Operation{{Kind: deep.OpMove, Path: "/id", From: "/nonexistent"}}
	if err := deep.Apply(u, p1); err == nil {
		t.Error("OpMove from non-existent source should return an error")
	}

	// Copy from non-existent path must also surface an error.
	p2 := deep.Patch[testmodels.User]{}
	p2.Operations = []deep.Operation{{Kind: deep.OpCopy, Path: "/id", From: "/nonexistent"}}
	if err := deep.Apply(u, p2); err == nil {
		t.Error("OpCopy from non-existent source should return an error")
	}

	// Move/Copy with empty From must reject early with a clear error.
	p3 := deep.Patch[testmodels.User]{}
	p3.Operations = []deep.Operation{{Kind: deep.OpMove, Path: "/id"}}
	if err := deep.Apply(u, p3); err == nil {
		t.Error("OpMove with empty From should return an error")
	}
	p4 := deep.Patch[testmodels.User]{}
	p4.Operations = []deep.Operation{{Kind: deep.OpCopy, Path: "/id"}}
	if err := deep.Apply(u, p4); err == nil {
		t.Error("OpCopy with empty From should return an error")
	}

	// Apply to nil
	if err := deep.Apply((*testmodels.User)(nil), p1); err == nil {
		t.Error("Apply to nil should fail")
	}
}

func TestFinalPush(t *testing.T) {
	// 1. All deep.OpKinds
	for _, k := range []deep.OpKind{
		deep.OpAdd, deep.OpRemove, deep.OpReplace, deep.OpMove,
		deep.OpCopy, deep.OpLog, deep.OpAlias, "",
	} {
		_ = k.String()
	}

	// 2. Nested delegation failure (nil field)
	type NestedNil struct {
		User *testmodels.User
	}
	nn := &NestedNil{}
	deep.Apply(nn, deep.Patch[NestedNil]{Operations: []deep.Operation{{Kind: deep.OpReplace, Path: "/User/id", New: 1}}})
}

func TestReflectionEqualCopy(t *testing.T) {
	type Simple struct {
		A int
	}
	s1 := Simple{A: 1}
	s2 := Simple{A: 2}

	if deep.Equal(s1, s2) {
		t.Error("deep.Equal failed for different simple structs")
	}

	s3 := deep.Clone(s1)
	if s3.A != 1 {
		t.Error("deep.Clone failed for simple struct")
	}
}

func TestTextAdvanced(t *testing.T) {
	clock := hlc.NewClock("node-a")
	t1 := clock.Now()
	t2 := clock.Now()

	// Complex ordering
	text := crdt.Text{
		{ID: t2, Value: "world", Prev: t1},
		{ID: t1, Value: "hello "},
	}

	s := text.String()
	if s != "hello world" {
		t.Errorf("expected hello world, got %q", s)
	}

	text2 := crdt.Text{{Value: "old"}}
	p2 := deep.Patch[crdt.Text]{Operations: []deep.Operation{
		{Kind: deep.OpReplace, Path: "/", New: crdt.Text{{Value: "new"}}},
	}}
	text2.Patch(p2, nil)
}

// reflectionUser mirrors testmodels.User field for field but has no generated
// code, so the two benchmark variants of each pair run the exact same shape
// through the generated fast path and the reflection engine.
type reflectionDetail struct {
	Age     int
	Address string `json:"addr"`
}

type reflectionUser struct {
	ID    int              `json:"id"`
	Name  string           `json:"full_name"`
	Info  reflectionDetail `json:"info"`
	Roles []string         `json:"roles"`
	Score map[string]int   `json:"score"`
}

func benchUsers() (testmodels.User, testmodels.User) {
	u1 := testmodels.User{
		ID:    1,
		Name:  "Alice",
		Info:  testmodels.Detail{Age: 30, Address: "1 Main St"},
		Roles: []string{"admin", "user"},
		Score: map[string]int{"chess": 1500, "go": 2000},
	}
	u2 := testmodels.User{
		ID:    1,
		Name:  "Bob",
		Info:  testmodels.Detail{Age: 31, Address: "1 Main St"},
		Roles: []string{"admin"},
		Score: map[string]int{"chess": 1500, "go": 2100},
	}
	return u1, u2
}

func benchReflectionUsers() (reflectionUser, reflectionUser) {
	u1 := reflectionUser{
		ID:    1,
		Name:  "Alice",
		Info:  reflectionDetail{Age: 30, Address: "1 Main St"},
		Roles: []string{"admin", "user"},
		Score: map[string]int{"chess": 1500, "go": 2000},
	}
	u2 := reflectionUser{
		ID:    1,
		Name:  "Bob",
		Info:  reflectionDetail{Age: 31, Address: "1 Main St"},
		Roles: []string{"admin"},
		Score: map[string]int{"chess": 1500, "go": 2100},
	}
	return u1, u2
}

func BenchmarkDiffGenerated(b *testing.B) {
	u1, u2 := benchUsers()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		deep.Diff(u1, u2)
	}
}

func BenchmarkDiffReflection(b *testing.B) {
	u1, u2 := benchReflectionUsers()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		deep.Diff(u1, u2)
	}
}

func BenchmarkEqualGenerated(b *testing.B) {
	u1, _ := benchUsers()
	u2 := u1
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		deep.Equal(u1, u2)
	}
}

func BenchmarkEqualReflection(b *testing.B) {
	u1, _ := benchReflectionUsers()
	u2 := u1
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		deep.Equal(u1, u2)
	}
}

func BenchmarkCloneGenerated(b *testing.B) {
	u1, _ := benchUsers()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		deep.Clone(u1)
	}
}

func BenchmarkCloneReflection(b *testing.B) {
	u1, _ := benchReflectionUsers()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		deep.Clone(u1)
	}
}

func BenchmarkApplyGenerated(b *testing.B) {
	u1, u2 := benchUsers()
	p, err := deep.Diff(u1, u2)
	if err != nil {
		b.Fatal(err)
	}
	// The patch holds only replace ops, so re-applying it to the same value
	// is idempotent — the clone stays outside the timed loop.
	u3 := deep.Clone(u1)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		deep.Apply(&u3, p)
	}
}

func BenchmarkApplyReflection(b *testing.B) {
	u1, u2 := benchReflectionUsers()
	p, err := deep.Diff(u1, u2)
	if err != nil {
		b.Fatal(err)
	}
	u3 := deep.Clone(u1)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		deep.Apply(&u3, p)
	}
}

// benchLargeMapUsers returns two users whose Score map holds size entries and
// differs in exactly one of them.
func benchLargeMapUsers(size int) (testmodels.User, testmodels.User) {
	u1 := testmodels.User{Name: "Alice", Score: make(map[string]int, size)}
	for i := 0; i < size; i++ {
		u1.Score[fmt.Sprintf("k%05d", i)] = i
	}
	u2 := deep.Clone(u1)
	u2.Score["k00000"] = -1
	return u1, u2
}

// BenchmarkDiffGeneratedLargeMap shows what ordering the emitted operations
// buys over ordering the map's keys. The work is proportional to the entries
// that changed — one, at every size — rather than to the size of the map.
func BenchmarkDiffGeneratedLargeMap(b *testing.B) {
	for _, size := range []int{10, 100, 1000, 10000} {
		b.Run(fmt.Sprintf("entries=%d", size), func(b *testing.B) {
			u1, u2 := benchLargeMapUsers(size)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				deep.Diff(u1, u2)
			}
		})
	}
}

// BenchmarkApplyGeneratedWirePatch applies a patch that has been through JSON,
// as a server applying client patches does on every request. Its values arrive
// still encoded (RawValue) and are decoded against the target field's type by
// the same generated fast path that handles in-process patches — this is what
// ValueAs buys over falling through to the reflection engine, which is what a
// failed type assertion used to mean.
func BenchmarkApplyGeneratedWirePatch(b *testing.B) {
	u1, u2 := benchUsers()
	p, err := deep.Diff(u1, u2)
	if err != nil {
		b.Fatal(err)
	}
	data, err := json.Marshal(p)
	if err != nil {
		b.Fatal(err)
	}
	var wire deep.Patch[testmodels.User]
	if err := json.Unmarshal(data, &wire); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	for b.Loop() {
		target := u1
		if err := deep.Apply(&target, wire); err != nil {
			b.Fatal(err)
		}
	}
}
