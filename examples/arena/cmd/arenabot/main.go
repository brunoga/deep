// arenabot fills an arena with players who walk toward the nearest gem —
// company for a human, and a load generator for the tick loop.
//
//	arenabot -server ws://localhost:7777 -n 4
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math/rand/v2"
	"os"
	"os/signal"
	"sync"
	"time"

	"github.com/brunoga/deep/examples/arena/client"
	"github.com/brunoga/deep/examples/arena/world"
)

func main() {
	server := flag.String("server", "ws://localhost:7777", "arena server")
	n := flag.Int("n", 3, "how many bots")
	pace := flag.Duration("pace", 150*time.Millisecond, "time between bot moves")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	var wg sync.WaitGroup
	for i := range *n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := fmt.Sprintf("bot%d", i+1)
			if err := runBot(ctx, *server, name, *pace); err != nil && ctx.Err() == nil {
				log.Printf("%s: %v", name, err)
			}
		}(i)
	}
	wg.Wait()
}

func runBot(ctx context.Context, server, name string, pace time.Duration) error {
	c, err := client.Dial(ctx, server+"/?name="+name, name)
	if err != nil {
		return err
	}
	defer c.Close()
	rng := rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64()))

	ticker := time.NewTicker(pace)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-c.Done():
			return c.Err()
		case <-ticker.C:
			w := c.World()
			me, ok := w.Players[name]
			if !ok {
				continue // our join has not been broadcast yet
			}
			dx, dy := step(w, me, rng)
			if _, err := c.Move(ctx, dx, dy); err != nil {
				return err
			}
		}
	}
}

// step walks one cell toward the nearest gem, or wanders when the floor is
// bare.
func step(w world.World, me world.Player, rng *rand.Rand) (int, int) {
	bestDist := 1 << 30
	var target world.Gem
	for _, g := range w.Gems {
		d := abs(g.X-me.X) + abs(g.Y-me.Y)
		if d < bestDist {
			bestDist, target = d, g
		}
	}
	if bestDist == 1<<30 {
		dirs := [][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}}
		d := dirs[rng.IntN(4)]
		return d[0], d[1]
	}
	// One axis at a time; break ties randomly so bots do not stack.
	if target.X != me.X && (target.Y == me.Y || rng.IntN(2) == 0) {
		return sign(target.X - me.X), 0
	}
	if target.Y != me.Y {
		return 0, sign(target.Y - me.Y)
	}
	return 0, 0
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func sign(v int) int {
	if v < 0 {
		return -1
	}
	if v > 0 {
		return 1
	}
	return 0
}
