package engine

import (
	"fmt"
	"testing"
)

func BenchmarkDiff_Slice_Large(b *testing.B) {
	sizes := []int{10, 100, 1000}
	for _, size := range sizes {
		b.Run(fmt.Sprintf("Size%d", size), func(b *testing.B) {
			s1 := make([]int, size)
			for i := 0; i < size; i++ {
				s1[i] = i
			}
			s2 := make([]int, size)
			copy(s2, s1)
			s2[size/2] = -1 // One change in the middle

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				MustDiff(s1, s2)
			}
		})
	}
}

func BenchmarkDiff_Slice_Append(b *testing.B) {
	sizes := []int{10, 100, 1000}
	for _, size := range sizes {
		b.Run(fmt.Sprintf("Size%d", size), func(b *testing.B) {
			s1 := make([]int, size)
			for i := 0; i < size; i++ {
				s1[i] = i
			}
			s2 := append(s1, size) // Append one element

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				MustDiff(s1, s2)
			}
		})
	}
}

func BenchmarkCopy_Basic(b *testing.B) {
	src := "a relatively short string to copy"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Copy(src)
	}
}

func BenchmarkCopy_LargeStruct(b *testing.B) {
	type S struct {
		A, B, C, D, E int
		F, G, H, I, J string
		K, L, M, N, O float64
	}
	src := S{
		A: 1, B: 2, C: 3, D: 4, E: 5,
		F: "a", G: "b", H: "c", I: "d", J: "e",
		K: 1.1, L: 2.2, M: 3.3, N: 4.4, O: 5.5,
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Copy(src)
	}
}

func BenchmarkCopy_Map(b *testing.B) {
	src := make(map[string]int)
	for i := 0; i < 100; i++ {
		src[fmt.Sprintf("key%d", i)] = i
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Copy(src)
	}
}

func BenchmarkDiff_Struct(b *testing.B) {
	type S struct {
		A, B, C, D, E int
		F, G, H, I, J string
		K, L, M, N, O float64
	}
	s1 := S{
		A: 1, B: 2, C: 3, D: 4, E: 5,
		F: "a", G: "b", H: "c", I: "d", J: "e",
		K: 1.1, L: 2.2, M: 3.3, N: 4.4, O: 5.5,
	}
	s2 := s1
	s2.O = 6.6 // One change

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		MustDiff(s1, s2)
	}
}

func BenchmarkDiff_LargeNested(b *testing.B) {
	type Node struct {
		ID       int
		Metadata map[string]string
		Children []*Node
	}

	var makeTree func(depth int) *Node
	makeTree = func(depth int) *Node {
		if depth == 0 {
			return nil
		}
		return &Node{
			ID:       depth,
			Metadata: map[string]string{"env": "prod", "tier": "backend"},
			Children: []*Node{makeTree(depth - 1), makeTree(depth - 1)},
		}
	}

	tree1 := makeTree(10)
	tree2, _ := Copy(tree1)
	// Change one value deep in the tree
	curr := tree2
	for curr.Children[0] != nil {
		curr = curr.Children[0]
	}
	curr.ID = 999

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		MustDiff(tree1, tree2)
	}
}
