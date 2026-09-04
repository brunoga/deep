package crdt

import (
	"fmt"
	"testing"

	"github.com/brunoga/deep/v6/crdt/hlc"
)

// Typing a document one character at a time, the way an editor does.
func BenchmarkTextTypeSequential(b *testing.B) {
	for _, n := range []int{200, 500, 1000} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				clock := hlc.NewClock("a")
				doc := Text{}
				for j := 0; j < n; j++ {
					doc = doc.Insert(doc.Len(), "x", clock)
				}
			}
		})
	}
}

// Typing in the middle of an existing document.
func BenchmarkTextTypeMiddle(b *testing.B) {
	for _, n := range []int{200, 500} {
		b.Run(fmt.Sprint(n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				clock := hlc.NewClock("a")
				doc := Text{}.Insert(0, "seed", clock)
				for j := 0; j < n; j++ {
					doc = doc.Insert(2, "y", clock)
				}
			}
		})
	}
}

// Reading the document back.
func BenchmarkTextString(b *testing.B) {
	clock := hlc.NewClock("a")
	doc := Text{}
	for j := 0; j < 1000; j++ {
		doc = doc.Insert(doc.Len(), "x", clock)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = doc.String()
	}
}

// Typing a word into the middle of a document, the way a person does — each
// character after the last one typed, not repeatedly at a fixed position.
func BenchmarkTextTypeWordMidDocument(b *testing.B) {
	for i := 0; i < b.N; i++ {
		clock := hlc.NewClock("a")
		doc := Text{}.Insert(0, "The quick brown fox jumps over the lazy dog. ", clock)
		pos := 20
		for _, r := range "interjected sentence typed one character at a time. " {
			doc = doc.Insert(pos, string(r), clock)
			pos++
		}
	}
}
