package crdt

import (
	"fmt"
	"testing"

	"github.com/brunoga/deep/v5/crdt/hlc"
)

// Editing a document that has accumulated many runs, which is where Text's
// per-edit cost grows: it walks the runs, Document descends them.
func BenchmarkEditFragmented(b *testing.B) {
	for _, runs := range []int{500, 2000, 8000} {
		b.Run(fmt.Sprintf("Text/%d", runs), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				clock := hlc.NewClock("a")
				doc := Text{}.Insert(0, "seed", clock)
				for j := 0; j < runs; j++ {
					doc = doc.Insert(0, "x", clock) // prepending forces a new run
				}
				b.StartTimer()
				for j := 0; j < 50; j++ {
					doc = doc.Insert(doc.Len(), "y", clock)
				}
			}
		})
		b.Run(fmt.Sprintf("Document/%d", runs), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				d := NewDocument(hlc.NewClock("a"))
				d.Insert(0, "seed")
				for j := 0; j < runs; j++ {
					d.Insert(0, "x")
				}
				b.StartTimer()
				for j := 0; j < 50; j++ {
					d.Insert(d.Len(), "y")
				}
			}
		})
	}
}

// Straight typing, where Text is already fast.
func BenchmarkTypeSequentialCompared(b *testing.B) {
	const n = 2000
	b.Run("Text", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			clock := hlc.NewClock("a")
			doc := Text{}
			for j := 0; j < n; j++ {
				doc = doc.Insert(doc.Len(), "x", clock)
			}
		}
	})
	b.Run("Document", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			d := NewDocument(hlc.NewClock("a"))
			for j := 0; j < n; j++ {
				d.Insert(d.Len(), "x")
			}
		}
	})
}
