package gen_test

import (
	"testing"

	deep "github.com/brunoga/deep/v6"
	"github.com/brunoga/deep/v6/gen"
)

// BenchmarkSortOperations measures the cost determinism adds to a map diff:
// one sort over the operations the map's changed entries produced. For a
// realistic diff that is a handful of entries whatever the map holds, which is
// why the v5.14 approach of sorting every key first was abandoned — at ten
// thousand entries it cost a millisecond and 160 KB where this costs neither.
func BenchmarkSortOperations(b *testing.B) {
	ops := []deep.Operation{{Kind: deep.OpReplace, Path: "/scores/k00000"}}
	b.ReportAllocs()
	for b.Loop() {
		gen.SortOperations(ops)
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
