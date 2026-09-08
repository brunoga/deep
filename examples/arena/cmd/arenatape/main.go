// arenatape plays back a match recorded by arenad -replay. The tape holds
// the initial world and one patch per tick — nothing else — and that is
// enough for three tricks:
//
//	arenatape match.tape              # watch the match in the terminal
//	arenatape -rewind match.tape      # watch it backwards, via Reverse
//	arenatape -verify match.tape      # prove forward and reverse agree
//	arenatape -keyframes 50 match.tape# report keyframe compaction savings
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	deep "github.com/brunoga/deep/v6"

	"github.com/brunoga/deep/examples/arena/replay"
	"github.com/brunoga/deep/examples/arena/transport"
	"github.com/brunoga/deep/examples/arena/world"
)

func main() {
	speed := flag.Duration("speed", 50*time.Millisecond, "time per tick during playback")
	rewind := flag.Bool("rewind", false, "play the match backwards")
	verify := flag.Bool("verify", false, "check forward replay and full rewind against each other, no playback")
	keyframes := flag.Int("keyframes", 0, "compact runs of this many ticks into keyframes and report the size change")
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}

	f, err := os.Open(flag.Arg(0))
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	initial, patches, err := replay.Read(f)
	if err != nil {
		log.Fatal(err)
	}
	final, err := replay.StateAt(initial, patches, len(patches))
	if err != nil {
		log.Fatalf("forward replay: %v", err)
	}

	switch {
	case *verify:
		back, err := replay.Rewind(final, patches, 0)
		if err != nil {
			log.Fatalf("rewind: %v", err)
		}
		if !deep.Equal(back, initial) {
			log.Fatal("REWIND MISMATCH: reversing every patch did not restore the initial world")
		}
		fmt.Printf("ok: %d ticks; forward replay and full rewind agree\n", len(patches))

	case *keyframes > 1:
		compacted, err := replay.Compact(initial, patches, *keyframes)
		if err != nil {
			log.Fatal(err)
		}
		viaKeys, err := replay.StateAt(initial, compacted, len(compacted))
		if err != nil {
			log.Fatal(err)
		}
		if !deep.Equal(viaKeys, final) {
			log.Fatal("KEYFRAME MISMATCH: compacted replay reaches a different final world")
		}
		fmt.Printf("%d tick patches (%d ops) -> %d keyframes (%d ops), same final world\n",
			len(patches), countOps(patches), len(compacted), countOps(compacted))

	case *rewind:
		w := deep.Clone(final)
		show(w, len(patches), len(patches))
		for i := len(patches) - 1; i >= 0; i-- {
			time.Sleep(*speed)
			if err := deep.Apply(&w, patches[i].Reverse()); err != nil {
				log.Fatalf("reversing patch %d: %v", i, err)
			}
			show(w, i, len(patches))
		}

	default:
		w := deep.Clone(initial)
		show(w, 0, len(patches))
		for i, p := range patches {
			time.Sleep(*speed)
			if err := deep.Apply(&w, p); err != nil {
				log.Fatalf("applying patch %d: %v", i, err)
			}
			show(w, i+1, len(patches))
		}
	}
}

func countOps(patches []transport.Patch) int {
	n := 0
	for _, p := range patches {
		n += len(p.Operations)
	}
	return n
}

// show clears the terminal and draws one frame.
func show(w world.World, at, total int) {
	var b strings.Builder
	b.WriteString("\x1b[H\x1b[2J")
	fmt.Fprintf(&b, "tick %d  (%d/%d)\n\n", w.Tick, at, total)

	grid := make([][]byte, w.Height)
	for y := range grid {
		grid[y] = []byte(strings.Repeat(".", w.Width))
	}
	for _, g := range w.Gems {
		if w.InBounds(g.X, g.Y) {
			grid[g.Y][g.X] = byte('0' + g.Value)
		}
	}
	for id, p := range w.Players {
		if w.InBounds(p.X, p.Y) {
			grid[p.Y][p.X] = strings.ToUpper(id[:1])[0]
		}
	}
	for _, row := range grid {
		b.WriteString("  ")
		for _, c := range row {
			b.WriteByte(c)
			b.WriteByte(' ')
		}
		b.WriteByte('\n')
	}

	ids := make([]string, 0, len(w.Players))
	for id := range w.Players {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		if w.Players[ids[i]].Score != w.Players[ids[j]].Score {
			return w.Players[ids[i]].Score > w.Players[ids[j]].Score
		}
		return ids[i] < ids[j]
	})
	b.WriteByte('\n')
	for _, id := range ids {
		fmt.Fprintf(&b, "  %-10s %4d\n", id, w.Players[id].Score)
	}
	os.Stdout.WriteString(b.String())
}

