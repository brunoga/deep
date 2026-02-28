package engine

import (
	"reflect"
	"testing"
)

func TestPatch_ReverseFormat_Exhaustive(t *testing.T) {
	// valuePatch
	t.Run("valuePatch", func(t *testing.T) {
		p := &valuePatch{oldVal: reflect.ValueOf(1), newVal: reflect.ValueOf(2)}
		p.reverse()
		p.format(0)
		p.toJSONPatch("/p")
		p.toJSONPatch("") // root

		pRem := &valuePatch{oldVal: reflect.ValueOf(1)}
		pRem.toJSONPatch("/p")
	})
	// ptrPatch
	t.Run("ptrPatch", func(t *testing.T) {
		p := &ptrPatch{elemPatch: &valuePatch{}}
		p.reverse()
		p.format(0)
		p.toJSONPatch("/p")
	})
	// interfacePatch
	t.Run("interfacePatch", func(t *testing.T) {
		p := &interfacePatch{elemPatch: &valuePatch{}}
		p.reverse()
		p.format(0)
		p.toJSONPatch("/p")
	})
	// structPatch
	t.Run("structPatch", func(t *testing.T) {
		p := &structPatch{fields: map[string]diffPatch{"A": &valuePatch{}}}
		p.reverse()
		p.format(0)
		p.toJSONPatch("/p")
	})
	// arrayPatch
	t.Run("arrayPatch", func(t *testing.T) {
		p := &arrayPatch{indices: map[int]diffPatch{0: &valuePatch{}}}
		p.reverse()
		p.format(0)
		p.toJSONPatch("/p")
	})
	// mapPatch
	t.Run("mapPatch", func(t *testing.T) {
		p := &mapPatch{
			added:    map[any]reflect.Value{"a": reflect.ValueOf(1)},
			removed:  map[any]reflect.Value{"b": reflect.ValueOf(2)},
			modified: map[any]diffPatch{"c": &valuePatch{}},
		}
		p.reverse()
		p.format(0)
		p.toJSONPatch("/p")
	})
	// slicePatch
	t.Run("slicePatch", func(t *testing.T) {
		p := &slicePatch{
			ops: []sliceOp{
				{Kind: OpAdd, Index: 0, Val: reflect.ValueOf(1)},
				{Kind: OpRemove, Index: 1, Val: reflect.ValueOf(2)},
				{Kind: OpReplace, Index: 2, Patch: &valuePatch{}},
			},
		}
		p.reverse()
		p.format(0)
		p.toJSONPatch("/p")
	})
	// testPatch
	t.Run("testPatch", func(t *testing.T) {
		p := &testPatch{expected: reflect.ValueOf(1)}
		p.reverse()
		p.format(0)
		p.toJSONPatch("/p")
	})
	// copyPatch
	t.Run("copyPatch", func(t *testing.T) {
		p := &copyPatch{from: "/a", path: "/b"}
		p.reverse()
		p.format(0)
		p.toJSONPatch("/p")
	})
	// movePatch
	t.Run("movePatch", func(t *testing.T) {
		p := &movePatch{from: "/a", path: "/b"}
		p.reverse()
		p.format(0)
		p.toJSONPatch("/p")
	})
	// logPatch
	t.Run("logPatch", func(t *testing.T) {
		p := &logPatch{message: "test"}
		p.reverse()
		p.format(0)
		p.toJSONPatch("/p")
	})
}

func TestPatch_MiscCoverage(t *testing.T) {
	// valuePatch reverse/format/toJSONPatch
	t.Run("valuePatch", func(t *testing.T) {
		p := &valuePatch{oldVal: reflect.ValueOf(1), newVal: reflect.ValueOf(2)}
		p.reverse()
		p.format(0)
		p.toJSONPatch("/path")
		p.toJSONPatch("") // root

		pRem := &valuePatch{oldVal: reflect.ValueOf(1)}
		pRem.toJSONPatch("/path")
	})

	// ptrPatch reverse/format/toJSONPatch
	t.Run("ptrPatch", func(t *testing.T) {
		p := &ptrPatch{elemPatch: &valuePatch{oldVal: reflect.ValueOf(1), newVal: reflect.ValueOf(2)}}
		p.reverse()
		p.format(0)
		p.toJSONPatch("/path")
	})

	// interfacePatch reverse/format/toJSONPatch
	t.Run("interfacePatch", func(t *testing.T) {
		p := &interfacePatch{elemPatch: &valuePatch{oldVal: reflect.ValueOf(1), newVal: reflect.ValueOf(2)}}
		p.reverse()
		p.format(0)
		p.toJSONPatch("/path")
	})

	// structPatch format/toJSONPatch
	t.Run("structPatch", func(t *testing.T) {
		p := &structPatch{fields: map[string]diffPatch{"A": &valuePatch{newVal: reflect.ValueOf(1)}}}
		p.format(0)
		p.toJSONPatch("/path")
	})

	// arrayPatch format/toJSONPatch
	t.Run("arrayPatch", func(t *testing.T) {
		p := &arrayPatch{indices: map[int]diffPatch{0: &valuePatch{newVal: reflect.ValueOf(1)}}}
		p.format(0)
		p.toJSONPatch("/path")
	})

	// mapPatch format/toJSONPatch
	t.Run("mapPatch", func(t *testing.T) {
		p := &mapPatch{
			added:    map[any]reflect.Value{"a": reflect.ValueOf(1)},
			removed:  map[any]reflect.Value{"b": reflect.ValueOf(2)},
			modified: map[any]diffPatch{"c": &valuePatch{newVal: reflect.ValueOf(3)}},
		}
		p.format(0)
		p.toJSONPatch("/path")
	})

	// slicePatch format
	t.Run("slicePatch", func(t *testing.T) {
		p := &slicePatch{
			ops: []sliceOp{
				{Kind: OpAdd, Index: 0, Val: reflect.ValueOf(1)},
				{Kind: OpRemove, Index: 1, Val: reflect.ValueOf(2)},
				{Kind: OpReplace, Index: 2, Patch: &valuePatch{newVal: reflect.ValueOf(3)}},
			},
		}
		p.format(0)
	})
}
func TestDependencyResolution_Swap(t *testing.T) {
	type S struct {
		A int
		B int
	}

	s := S{A: 1, B: 2}

	// Create a swap patch manually
	// A = Copy(B)
	// B = Copy(A)
	// Note: Move(B->A) and Move(A->B) is also a swap but deletes sources.
	// Since A and B are both overwritten, deletion is implicit.

	patch := &structPatch{
		fields: map[string]diffPatch{
			"A": &copyPatch{from: "/B"}, // A reads B
			"B": &copyPatch{from: "/A"}, // B reads A
		},
	}

	// Apply to s
	// Expected: A=2, B=1
	// Original: A=1, B=2

	// Logic:
	// Cycle A <-> B.
	// A reads B. B reads A.
	// Both should be pre-read.
	// A becomes Replace(2).
	// B becomes Replace(1).
	// Result: A=2, B=1.

	// We need to wrap it in a typedPatch to call Apply easily, or call apply directly.
	// calling apply directly requires root value.

	val := reflect.ValueOf(&s)
	patch.apply(val, val.Elem(), "/")

	if s.A != 2 {
		t.Errorf("Expected s.A = 2, got %d", s.A)
	}
	if s.B != 1 {
		t.Errorf("Expected s.B = 1, got %d", s.B)
	}
}

func TestDependencyResolution_Chain(t *testing.T) {
	type S struct {
		A int
		B int
		C int
	}
	// A -> B -> C -> A (Rotate)
	s := S{A: 1, B: 2, C: 3}

	patch := &structPatch{
		fields: map[string]diffPatch{
			"A": &copyPatch{from: "/C"},
			"B": &copyPatch{from: "/A"},
			"C": &copyPatch{from: "/B"},
		},
	}
	// Expected: A=3, B=1, C=2

	val := reflect.ValueOf(&s)
	patch.apply(val, val.Elem(), "/")

	if s.A != 3 {
		t.Errorf("Expected s.A = 3, got %d", s.A)
	}
	if s.B != 1 {
		t.Errorf("Expected s.B = 1, got %d", s.B)
	}
	if s.C != 2 {
		t.Errorf("Expected s.C = 2, got %d", s.C)
	}
}

func TestDependencyResolution_Move(t *testing.T) {
	type S struct {
		A int
		B int
	}
	s := S{A: 1, B: 2}

	// Move B -> A.
	// A reads B. A writes A. A writes B (delete).
	// Expected: A=2, B=0 (deleted)

	patch := &structPatch{
		fields: map[string]diffPatch{
			"A": &movePatch{from: "/B", path: "/A"},
		},
	}

	val := reflect.ValueOf(&s)
	patch.apply(val, val.Elem(), "/")

	if s.A != 2 {
		t.Errorf("Expected s.A = 2, got %d", s.A)
	}
	if s.B != 0 {
		t.Errorf("Expected s.B = 0 (deleted), got %d", s.B)
	}
}

func TestDependencyResolution_MoveSwap(t *testing.T) {
	type S struct {
		A int
		B int
	}
	s := S{A: 1, B: 2}

	// Move B -> A, Move A -> B
	// Cycle.
	// Expected: A=2, B=1.

	patch := &structPatch{
		fields: map[string]diffPatch{
			"A": &movePatch{from: "/B", path: "/A"},
			"B": &movePatch{from: "/A", path: "/B"},
		},
	}

	val := reflect.ValueOf(&s)
	patch.apply(val, val.Elem(), "/")

	if s.A != 2 {
		t.Errorf("Expected s.A = 2, got %d", s.A)
	}
	if s.B != 1 {
		t.Errorf("Expected s.B = 1, got %d", s.B)
	}
}

func TestDependencyResolution_Overlap(t *testing.T) {
	type Inner struct {
		X int
	}
	type S struct {
		A Inner
		B Inner
	}
	s := S{A: Inner{X: 1}, B: Inner{X: 2}}

	// Copy B.X -> A.X
	// Remove B

	// A field patch: replace A (valuePatch? No, let's use structPatch for A)
	// A structPatch -> fields["X"] -> copyPatch(from="/B/X")

	// B field patch: remove (valuePatch with nil new)

	// Dependency:
	// A reads /B/X.
	// B writes /B.
	// /B/X is inside /B.
	// So A reads what B writes.
	// A depends on B.
	// Read before Write -> A must run before B.

	patch := &structPatch{
		fields: map[string]diffPatch{
			"A": &structPatch{
				fields: map[string]diffPatch{
					"X": &copyPatch{from: "/B/X", path: "/A/X"},
				},
			},
			"B": &valuePatch{oldVal: reflect.ValueOf(s.B)}, // Remove B
		},
	}

	val := reflect.ValueOf(&s)
	patch.apply(val, val.Elem(), "/")

	if s.A.X != 2 {
		t.Errorf("Expected s.A.X = 2, got %d", s.A.X)
	}
	if s.B.X != 0 {
		// B should be removed (zeroed)
		t.Errorf("Expected s.B.X = 0, got %d", s.B.X)
	}
}
