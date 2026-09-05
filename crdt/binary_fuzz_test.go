package crdt

import (
	"math/rand"
	"testing"
)

// The binary decoders read bytes from the network — deepws feeds them frames
// from whoever connected — so they must reject anything at all without
// panicking or over-allocating.
func FuzzUpdateUnmarshalBinary(f *testing.F) {
	good, _ := genUpdate(rand.New(rand.NewSource(1)), 2, 5).MarshalBinary()
	f.Add(good)
	f.Add([]byte{1})
	f.Add([]byte{1, 0xff, 0xff, 0xff, 0xff, 0x0f})
	f.Fuzz(func(t *testing.T, data []byte) {
		var u Update
		_ = u.UnmarshalBinary(data) // must not panic
	})
}

func FuzzStateVectorUnmarshalBinary(f *testing.F) {
	sv := StateVector{"a": 1, "b": 2}
	good, _ := sv.MarshalBinary()
	f.Add(good)
	f.Fuzz(func(t *testing.T, data []byte) {
		var out StateVector
		_ = out.UnmarshalBinary(data)
	})
}
