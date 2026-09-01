package deep_test

import (
	"github.com/brunoga/deep/v5"
	"github.com/brunoga/deep/v5/internal/testmodels"

	"testing"
)

func TestBuilder(t *testing.T) {
	type Config struct {
		Theme string `json:"theme"`
	}

	c1 := Config{Theme: "dark"}

	patch := deep.Edit(&c1).
		With(deep.Set(deep.Field(func(c *Config) *string { return &c.Theme }), "light")).
		Build()

	if err := deep.Apply(&c1, patch); err != nil {
		t.Fatalf("deep.Apply failed: %v", err)
	}

	if c1.Theme != "light" {
		t.Errorf("got %s, want light", c1.Theme)
	}
}

func TestComplexBuilder(t *testing.T) {
	u1 := testmodels.User{
		ID:    1,
		Name:  "Alice",
		Roles: []string{"user"},
		Score: map[string]int{"a": 10},
	}

	namePath := deep.Field(func(u *testmodels.User) *string { return &u.Name })
	agePath := deep.Field(func(u *testmodels.User) *int { return &u.Info.Age })
	rolesPath := deep.Field(func(u *testmodels.User) *[]string { return &u.Roles })
	scorePath := deep.Field(func(u *testmodels.User) *map[string]int { return &u.Score })

	patch := deep.Edit(&u1).
		With(
			deep.Set(namePath, "Alice Smith"),
			deep.Set(agePath, 35),
			deep.Add(deep.At(rolesPath, 1), "admin"),
			deep.Set(deep.MapKey(scorePath, "b"), 20),
			deep.Remove(deep.MapKey(scorePath, "a")),
		).
		Build()

	u2 := u1
	if err := deep.Apply(&u2, patch); err != nil {
		t.Fatalf("deep.Apply failed: %v", err)
	}

	if u2.Name != "Alice Smith" {
		t.Errorf("Name failed: %s", u2.Name)
	}
	if u2.Info.Age != 35 {
		t.Errorf("Age failed: %d", u2.Info.Age)
	}
	if len(u2.Roles) != 2 || u2.Roles[1] != "admin" {
		t.Errorf("Roles failed: %v", u2.Roles)
	}
	if u2.Score["b"] != 20 {
		t.Errorf("Score failed: %v", u2.Score)
	}
	if _, ok := u2.Score["a"]; ok {
		t.Errorf("Score 'a' should have been removed")
	}
}

func TestLog(t *testing.T) {
	u := testmodels.User{ID: 1, Name: "Alice"}

	namePath := deep.Field(func(u *testmodels.User) *string { return &u.Name })

	p := deep.Edit(&u).
		Log("Starting update").
		With(deep.Set(namePath, "Bob")).
		Log("Finished update").
		Build()

	deep.Apply(&u, p)
}

func TestBuilderAdvanced(t *testing.T) {
	u := &testmodels.User{}
	idPath := deep.Field(func(u *testmodels.User) *int { return &u.ID })
	namePath := deep.Field(func(u *testmodels.User) *string { return &u.Name })

	p := deep.Edit(u).
		Guard(deep.Eq(idPath, 1)).
		With(
			deep.Set(idPath, 2).Unless(deep.Eq(idPath, 1)),
		).
		Build()

	_ = deep.Gt(idPath, 0)
	_ = deep.Lt(idPath, 10)
	_ = deep.Exists(namePath)

	if p.Guard == nil || p.Guard.Op != "==" {
		t.Error("Guard failed")
	}
}

// TestReflectionMapKeyEscaping asserts the reflection engine escapes map keys
// per RFC 6901 when flattening a diff into operations, so keys containing '/'
// or '~' survive the diff→apply round-trip. (Generated code is covered by the
// internal/testmodels/shapes tests.)
func TestReflectionMapKeyEscaping(t *testing.T) {
	type holder struct {
		M map[string]int
	}
	a := holder{M: map[string]int{}}
	b := holder{M: map[string]int{"a/b": 1, "t~e": 2}}

	p, err := deep.Diff(a, b)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	for _, op := range p.Operations {
		if op.Path == "/M/a/b" {
			t.Errorf("unescaped path emitted: %q", op.Path)
		}
	}

	got := deep.Clone(a)
	if err := deep.Apply(&got, p); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !deep.Equal(got, b) {
		t.Errorf("round-trip: got %v, want %v", got.M, b.M)
	}
}

// TestSliceRoundTrip asserts that Diff followed by Apply reproduces the target
// for unkeyed slices. The reflection engine computes an edit script whose
// indices only mean anything as a batch; flattening it into independent
// operations used to corrupt the slice ([a b c] -> [a B c] produced [a c B]).
func TestSliceRoundTrip(t *testing.T) {
	type holder struct {
		Items []string
	}
	cases := []struct {
		name     string
		from, to []string
	}{
		{"replace-middle", []string{"a", "b", "c"}, []string{"a", "B", "c"}},
		{"replace-first", []string{"a", "b", "c"}, []string{"A", "b", "c"}},
		{"replace-last", []string{"a", "b", "c"}, []string{"a", "b", "C"}},
		{"insert-middle", []string{"a", "c"}, []string{"a", "b", "c"}},
		{"append", []string{"a", "b"}, []string{"a", "b", "c"}},
		{"truncate", []string{"a", "b", "c"}, []string{"a", "b"}},
		{"reorder", []string{"a", "b", "c"}, []string{"c", "b", "a"}},
		{"clear", []string{"a", "b"}, nil},
		{"grow-from-nil", nil, []string{"a"}},
		{"replace-all", []string{"a", "b"}, []string{"x", "y"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := holder{Items: tc.from}
			b := holder{Items: tc.to}

			p, err := deep.Diff(a, b)
			if err != nil {
				t.Fatalf("Diff: %v", err)
			}
			got := deep.Clone(a)
			if err := deep.Apply(&got, p); err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if !deep.Equal(got, b) {
				t.Errorf("round-trip: %v -> %v produced %v", tc.from, tc.to, got.Items)
			}

			// The reverse patch must restore the original just as faithfully.
			if err := deep.Apply(&got, p.Reverse()); err != nil {
				t.Fatalf("Apply reverse: %v", err)
			}
			if !deep.Equal(got, a) {
				t.Errorf("reverse: %v -> %v did not restore, got %v", tc.from, tc.to, got.Items)
			}
		})
	}
}

// TestApplyTypeMismatchErrors asserts that a value of the wrong type produces
// an error rather than panicking. Patches routinely arrive as untrusted JSON,
// so a mismatch must not be able to crash the process.
func TestApplyTypeMismatchErrors(t *testing.T) {
	type target struct {
		Age   int            `json:"age"`
		Tags  []string       `json:"tags"`
		Score map[string]int `json:"score"`
	}

	for _, tc := range []struct {
		name string
		op   deep.Operation
	}{
		{"struct field", deep.Operation{Kind: deep.OpReplace, Path: "/age", New: "not a number"}},
		{"slice element", deep.Operation{Kind: deep.OpReplace, Path: "/tags/0", New: struct{ X int }{1}}},
		{"map value", deep.Operation{Kind: deep.OpReplace, Path: "/score/a", New: []string{"nope"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := target{Age: 30, Tags: []string{"x"}, Score: map[string]int{"a": 1}}
			err := deep.Apply(&v, deep.Patch[target]{Operations: []deep.Operation{tc.op}})
			if err == nil {
				t.Fatal("expected an error, got nil")
			}
			if v.Age != 30 || v.Tags[0] != "x" || v.Score["a"] != 1 {
				t.Errorf("target was mutated by a failed operation: %+v", v)
			}
		})
	}
}

// A type can supply its own diff, which the engine uses instead of comparing
// the value structurally. Such a patch describes the change in terms only that
// type understands, and the flat operation form has no way to carry that — so
// it fell out entirely: Diff returned no operations and the change was lost
// without a word. The flat form now falls back to saying what the value
// became, which any consumer can apply.
type diffCounter struct{ N int }

type diffCounterPatch struct{ Delta int }

func (p *diffCounterPatch) Apply(c *diffCounter) { c.N += p.Delta }

func (c *diffCounter) Diff(other *diffCounter) (*diffCounterPatch, error) {
	if c.N == other.N {
		return nil, nil
	}
	return &diffCounterPatch{Delta: other.N - c.N}, nil
}

func TestCustomDifferSurvivesFlattening(t *testing.T) {
	type holder struct {
		Name    string
		Counter *diffCounter
	}

	a := holder{Name: "x", Counter: &diffCounter{N: 1}}
	b := holder{Name: "y", Counter: &diffCounter{N: 5}}

	p, err := deep.Diff(a, b)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if p.IsEmpty() {
		t.Fatal("a custom differ produced no operations at all")
	}

	got := deep.Clone(a)
	if err := deep.Apply(&got, p); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got.Counter.N != 5 {
		t.Errorf("the custom-differ change did not survive: N = %d, want 5", got.Counter.N)
	}
	if got.Name != "y" {
		t.Errorf("the ordinary field did not survive: Name = %q, want %q", got.Name, "y")
	}

	// Unchanged values must still produce nothing.
	same, err := deep.Diff(a, a)
	if err != nil {
		t.Fatalf("Diff of equal values: %v", err)
	}
	if !same.IsEmpty() {
		t.Errorf("diffing a value with itself produced %v", same.Operations)
	}
}
