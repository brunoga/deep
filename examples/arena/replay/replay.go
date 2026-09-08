// Package replay is arena's match recording: the initial world followed by
// every tick's patch, as one gob stream. There is no bespoke format here —
// the replay file IS the patch stream the clients saw, which is what makes
// the three playback tricks work:
//
//   - play forward by applying patches, exactly as a live client does;
//   - rewind by applying each patch's Reverse in the opposite order, which
//     provably lands back on the initial world;
//   - compact for seeking by collapsing runs of patches into keyframes,
//     each one a diff between boundary states.
package replay

import (
	"encoding/gob"
	"errors"
	"fmt"
	"io"

	deep "github.com/brunoga/deep/v6"

	"github.com/brunoga/deep/examples/arena/transport"
	"github.com/brunoga/deep/examples/arena/world"
)

// Writer records a match: the initial world once, then one patch per tick.
type Writer struct {
	enc     *gob.Encoder
	started bool
}

// NewWriter wraps w; nothing is written until Init.
func NewWriter(w io.Writer) *Writer {
	return &Writer{enc: gob.NewEncoder(w)}
}

// Init writes the world every following patch applies against.
func (w *Writer) Init(initial world.World) error {
	if w.started {
		return errors.New("replay: Init called twice")
	}
	w.started = true
	return w.enc.Encode(initial)
}

// Append records one tick's patch.
func (w *Writer) Append(p transport.Patch) error {
	if !w.started {
		return errors.New("replay: Append before Init")
	}
	return w.enc.Encode(p)
}

// Read decodes a whole recording.
func Read(r io.Reader) (initial world.World, patches []transport.Patch, err error) {
	dec := gob.NewDecoder(r)
	if err := dec.Decode(&initial); err != nil {
		return world.World{}, nil, fmt.Errorf("replay: reading initial world: %w", err)
	}
	if err := initial.Validate(); err != nil {
		return world.World{}, nil, fmt.Errorf("replay: %w", err)
	}
	for {
		var p transport.Patch
		if err := dec.Decode(&p); err != nil {
			if errors.Is(err, io.EOF) {
				return initial, patches, nil
			}
			return world.World{}, nil, fmt.Errorf("replay: reading patch %d: %w", len(patches), err)
		}
		patches = append(patches, p)
	}
}

// StateAt replays forward to just after patches[n-1] — n of 0 is the initial
// world, n of len(patches) the final one.
func StateAt(initial world.World, patches []transport.Patch, n int) (world.World, error) {
	w := deep.Clone(initial)
	for i := range min(n, len(patches)) {
		if err := deep.Apply(&w, patches[i]); err != nil {
			return world.World{}, fmt.Errorf("replay: applying patch %d: %w", i, err)
		}
	}
	return w, nil
}

// Rewind steps a final world backwards through the recording to the state
// after patch n — the same walk StateAt makes forward, done entirely with
// Reverse.
func Rewind(final world.World, patches []transport.Patch, n int) (world.World, error) {
	w := deep.Clone(final)
	for i := len(patches) - 1; i >= n; i-- {
		if err := deep.Apply(&w, patches[i].Reverse()); err != nil {
			return world.World{}, fmt.Errorf("replay: reversing patch %d: %w", i, err)
		}
	}
	return w, nil
}

// Compact collapses every run of interval patches into one keyframe patch,
// for recordings meant to be seeked rather than watched: a seek costs one
// apply per keyframe instead of one per tick.
//
// A keyframe is not a merge of the run's patches — it is Diff between the
// run's boundary states. The distinction matters twice over. Semantically,
// deep.Merge resolves *concurrent* edits of one base, and its collision rule
// (an ancestor path drops what it encloses) would discard a tick's
// "Add /players/ana" in favour of a later tick's "/players/ana/x" — a patch
// that cannot apply. And practically, diffing endpoints is smaller: a player
// who wandered eight cells and came home contributes nothing at all.
func Compact(initial world.World, patches []transport.Patch, interval int) ([]transport.Patch, error) {
	if interval < 2 || len(patches) == 0 {
		return patches, nil
	}
	var out []transport.Patch
	prev := deep.Clone(initial)
	cur := deep.Clone(initial)
	for start := 0; start < len(patches); start += interval {
		end := min(start+interval, len(patches))
		for _, p := range patches[start:end] {
			if err := deep.Apply(&cur, p); err != nil {
				return nil, fmt.Errorf("replay: compacting: %w", err)
			}
		}
		key, err := deep.Diff(prev, cur)
		if err != nil {
			return nil, fmt.Errorf("replay: compacting: %w", err)
		}
		out = append(out, key)
		prev = deep.Clone(cur)
	}
	return out, nil
}
