package gen_test

import (
	"fmt"
	"testing"

	deep "github.com/brunoga/deep/v5"
	"github.com/brunoga/deep/v5/gen"
)

// Two ways to make a map diff deterministic, at the sizes where the difference
// shows. Ordering the keys costs a slice and a sort over every entry in the
// map; ordering the emitted operations costs a sort over the entries that
// actually changed, which for most diffs is a handful whatever the map holds.
//
// Both are here because code generated before v5.15 calls SortedKeys.
func BenchmarkOrderingStrategies(b *testing.B) {
	for _, size := range []int{10, 100, 1000, 10000} {
		m := make(map[string]int, size)
		for i := 0; i < size; i++ {
			m[fmt.Sprintf("k%05d", i)] = i
		}
		// One changed entry, which is what a realistic diff emits.
		ops := []deep.Operation{{Kind: deep.OpReplace, Path: "/scores/k00000"}}

		b.Run(fmt.Sprintf("SortedKeys/entries=%d", size), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				gen.SortedKeys(m)
			}
		})
		b.Run(fmt.Sprintf("SortOperations/entries=%d", size), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				gen.SortOperations(ops)
			}
		})
	}
}

func TestSortedKeysOrdersByRenderedForm(t *testing.T) {
	got := gen.SortedKeys(map[string]int{"b": 1, "a": 2, "/slash": 3})
	want := []string{"/slash", "a", "b"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
	// Non-string keys sort by how they render, which is how they appear in a
	// path.
	ints := gen.SortedKeys(map[int]string{10: "", 2: "", 1: ""})
	if fmt.Sprint(ints) != "[1 10 2]" {
		t.Errorf("got %v, want [1 10 2]", ints)
	}
}

func TestSortOperationsOrdersByPath(t *testing.T) {
	ops := []deep.Operation{
		{Path: "/b"}, {Path: "/a"}, {Path: "/c"},
	}
	gen.SortOperations(ops)
	for i, want := range []string{"/a", "/b", "/c"} {
		if ops[i].Path != want {
			t.Fatalf("operation %d is %q, want %q", i, ops[i].Path, want)
		}
	}
}
