package crdt

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"testing"

	"github.com/brunoga/deep/v5/crdt/hlc"
)

// genUpdate builds an update the way a real session produces one: runs from a
// handful of writers, wall times close together, anchors pointing at earlier
// runs.
func genUpdate(rng *rand.Rand, writers, runs int) Update {
	nodes := make([]string, writers)
	for i := range nodes {
		nodes[i] = fmt.Sprintf("node-%d-%08x", i, rng.Uint32())
	}
	base := int64(1767225600_000000000)

	var u Update
	var prev hlc.HLC
	for i := 0; i < runs; i++ {
		node := nodes[rng.Intn(len(nodes))]
		base += int64(rng.Intn(5_000)) + 1 // microseconds apart
		id := hlc.HLC{WallTime: base, Logical: int32(rng.Intn(3)), NodeID: node}
		r := TextRun{ID: id, Value: words[rng.Intn(len(words))]}
		r.N = int32(len([]rune(r.Value)))
		if i > 0 && rng.Intn(4) > 0 {
			r.Prev = prev
		}
		r.Deleted = rng.Intn(8) == 0
		u.Runs = append(u.Runs, r)
		prev = id
	}
	for i := 0; i < rng.Intn(4); i++ {
		base += int64(rng.Intn(1000)) + 1
		u.Deleted = append(u.Deleted, DeletedRange{
			ID: hlc.HLC{WallTime: base, Logical: 0, NodeID: nodes[rng.Intn(len(nodes))]},
			N:  int32(rng.Intn(20) + 1),
		})
	}
	return u
}

var words = []string{"the", "quick", "brown", "fox", "jumps", "over", "lazy", "dog", "", "héllo wörld"}

func TestUpdateBinaryRoundTrip(t *testing.T) {
	for seed := 0; seed < 300; seed++ {
		rng := rand.New(rand.NewSource(int64(seed)))
		want := genUpdate(rng, 1+rng.Intn(4), rng.Intn(30))

		data, err := want.MarshalBinary()
		if err != nil {
			t.Fatalf("seed %d: marshal: %v", seed, err)
		}
		var got Update
		if err := got.UnmarshalBinary(data); err != nil {
			t.Fatalf("seed %d: unmarshal: %v", seed, err)
		}

		if len(got.Runs) != len(want.Runs) || len(got.Deleted) != len(want.Deleted) {
			t.Fatalf("seed %d: shape differs: %d/%d runs, %d/%d deleted",
				seed, len(got.Runs), len(want.Runs), len(got.Deleted), len(want.Deleted))
		}
		for i := range want.Runs {
			if got.Runs[i] != want.Runs[i] {
				t.Fatalf("seed %d: run %d: got %+v want %+v", seed, i, got.Runs[i], want.Runs[i])
			}
		}
		for i := range want.Deleted {
			if got.Deleted[i] != want.Deleted[i] {
				t.Fatalf("seed %d: deleted %d: got %+v want %+v", seed, i, got.Deleted[i], want.Deleted[i])
			}
		}
	}
}

func TestStateVectorBinaryRoundTrip(t *testing.T) {
	for seed := 0; seed < 100; seed++ {
		rng := rand.New(rand.NewSource(int64(seed)))
		want := StateVector{}
		for i := 0; i < rng.Intn(6); i++ {
			want[fmt.Sprintf("node-%d@%d", i, rng.Intn(1000))] = int32(rng.Intn(1 << 20))
		}
		data, err := want.MarshalBinary()
		if err != nil {
			t.Fatalf("seed %d: marshal: %v", seed, err)
		}
		var got StateVector
		if err := got.UnmarshalBinary(data); err != nil {
			t.Fatalf("seed %d: unmarshal: %v", seed, err)
		}
		if len(got) != len(want) {
			t.Fatalf("seed %d: %d entries, want %d", seed, len(got), len(want))
		}
		for k, v := range want {
			if got[k] != v {
				t.Fatalf("seed %d: %s = %d, want %d", seed, k, got[k], v)
			}
		}
	}
}

func TestStateVectorEncodingIsStable(t *testing.T) {
	sv := StateVector{"c": 3, "a": 1, "b": 2}
	first, _ := sv.MarshalBinary()
	for i := 0; i < 20; i++ {
		again, _ := sv.MarshalBinary()
		if string(again) != string(first) {
			t.Fatal("the same state vector encoded to different bytes")
		}
	}
}

func TestBinaryRejectsBadInput(t *testing.T) {
	good, _ := genUpdate(rand.New(rand.NewSource(1)), 2, 10).MarshalBinary()

	cases := map[string][]byte{
		"empty":            {},
		"unknown version":  {99, 0, 0, 0},
		"truncated":        good[:len(good)/2],
		"trailing garbage": append(append([]byte{}, good...), 0xff, 0xff),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			var u Update
			if err := u.UnmarshalBinary(data); err == nil {
				t.Error("accepted a payload it should have refused")
			}
		})
	}
}

func TestBinaryIsSmallerThanJSON(t *testing.T) {
	// The point of the format. A realistic session: a few writers, many short
	// runs, anchors throughout.
	for _, runs := range []int{10, 100, 1000} {
		u := genUpdate(rand.New(rand.NewSource(7)), 3, runs)

		bin, err := u.MarshalBinary()
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		js, err := json.Marshal(u)
		if err != nil {
			t.Fatalf("json: %v", err)
		}
		ratio := float64(len(js)) / float64(len(bin))
		t.Logf("%4d runs: json %6d B, binary %5d B  (%.1fx smaller)", runs, len(js), len(bin), ratio)
		if len(bin) >= len(js) {
			t.Errorf("%d runs: binary (%d B) is not smaller than JSON (%d B)", runs, len(bin), len(js))
		}
	}
}
