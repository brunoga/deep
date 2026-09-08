package server

import (
	"bytes"
	"context"
	"math/rand/v2"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	deep "github.com/brunoga/deep/v6"

	"github.com/brunoga/deep/examples/arena/client"
	"github.com/brunoga/deep/examples/arena/replay"
	"github.com/brunoga/deep/examples/arena/transport"
	"github.com/brunoga/deep/examples/arena/world"
)

func startArena(t *testing.T, opts ...Option) (*Server, string, context.CancelFunc) {
	t.Helper()
	s := New(world.New(12, 10), append([]Option{
		WithTick(5 * time.Millisecond),
		WithSnapshotEvery(10),
		WithSeed(42),
	}, opts...)...)
	ctx, cancel := context.WithCancel(context.Background())
	go s.Run(ctx)
	srv := httptest.NewServer(s)
	t.Cleanup(srv.Close)
	t.Cleanup(cancel)
	return s, "ws" + strings.TrimPrefix(srv.URL, "http") + "/?name=", cancel
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// The convergence property, end to end: several clients play at once for a
// while, and every replica — fed nothing but tick patches — matches every
// other, with zero drift-check failures.
func TestReplicasConverge(t *testing.T) {
	_, url, _ := startArena(t)
	ctx := context.Background()

	names := []string{"ana", "bob", "cyn", "dee"}
	clients := make([]*client.Client, len(names))
	for i, name := range names {
		c, err := client.Dial(ctx, url+name)
		if err != nil {
			t.Fatal(err)
		}
		clients[i] = c
		defer c.Close()
	}

	// Everybody plays: random legal moves against each one's own replica.
	var wg sync.WaitGroup
	for i, c := range clients {
		wg.Add(1)
		go func(i int, c *client.Client) {
			defer wg.Done()
			rng := rand.New(rand.NewPCG(uint64(i), 7))
			dirs := [][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}}
			for range 60 {
				d := dirs[rng.IntN(4)]
				if _, err := c.Move(ctx, d[0], d[1]); err != nil {
					t.Errorf("%s: %v", c.You(), err)
					return
				}
				time.Sleep(3 * time.Millisecond)
			}
		}(i, c)
	}
	wg.Wait()

	// Quiesce: wait until all replicas agree on the same tick, then compare.
	waitFor(t, "replicas to converge", func() bool {
		first := clients[0].World()
		for _, c := range clients[1:] {
			if !deep.Equal(c.World(), first) {
				return false
			}
		}
		return len(first.Players) == len(names)
	})
	for _, c := range clients {
		if c.Drift() != 0 {
			t.Errorf("%s: %d drift-check failures — a tick patch did not "+
				"reproduce the server's change", c.You(), c.Drift())
		}
	}
}

// Two players race for the same gem; the conditions decide and the total
// score equals the gem's value, once.
func TestGemRaceOneWinner(t *testing.T) {
	s, url, _ := startArena(t, WithGemTarget(0)) // no random gems in the way
	ctx := context.Background()

	ana, err := client.Dial(ctx, url+"ana")
	if err != nil {
		t.Fatal(err)
	}
	defer ana.Close()
	bob, err := client.Dial(ctx, url+"bob")
	if err != nil {
		t.Fatal(err)
	}
	defer bob.Close()

	waitFor(t, "both players present", func() bool {
		w := ana.World()
		_, okA := w.Players["ana"]
		_, okB := w.Players["bob"]
		return okA && okB
	})
	s.Mutate(func(w *world.World) {
		a, b := w.Players["ana"], w.Players["bob"]
		a.X, a.Y = 4, 4
		b.X, b.Y = 6, 4
		w.Players["ana"], w.Players["bob"] = a, b
		w.Gems["prize"] = world.Gem{X: 5, Y: 4, Value: 3}
	})
	waitFor(t, "gem visible in replicas", func() bool {
		_, okA := ana.World().Gems["prize"]
		_, okB := bob.World().Gems["prize"]
		return okA && okB
	})

	// Both step onto the gem in the same tick window.
	if _, err := ana.Move(ctx, 1, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := bob.Move(ctx, -1, 0); err != nil {
		t.Fatal(err)
	}

	waitFor(t, "the race to settle", func() bool {
		w := ana.World()
		_, gone := w.Gems["prize"]
		return !gone && w.Players["ana"].X == 5 && w.Players["bob"].X == 5
	})
	w := ana.World()
	total := w.Players["ana"].Score + w.Players["bob"].Score
	if total != 3 {
		t.Fatalf("gem paid out %d, want exactly 3 (ana=%d bob=%d)",
			total, w.Players["ana"].Score, w.Players["bob"].Score)
	}
}

// The replay file reconstructs the match in both directions: forward with
// Apply to the final state, backward with Reverse to the initial one.
func TestReplayRoundTrip(t *testing.T) {
	var rec bytes.Buffer
	writer := replay.NewWriter(&rec)
	s, url, cancel := startArena(t, WithReplay(writer))
	ctx := context.Background()

	c, err := client.Dial(ctx, url+"ana")
	if err != nil {
		t.Fatal(err)
	}
	dirs := [][2]int{{1, 0}, {0, 1}, {-1, 0}, {0, -1}}
	for i := range 40 {
		d := dirs[i%4]
		if _, err := c.Move(ctx, d[0], d[1]); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond)
	}
	c.Close()
	cancel() // stop the loop so the recording is complete
	<-s.done
	// The recording describes broadcast states; its final frame is whatever
	// lastBroadcast held when the loop stopped — not the live world, which
	// may hold changes (the leave, a last spawn) no patch ever carried.
	final := deep.Clone(s.lastBroadcast)

	initial, patches, err := replay.Read(&rec)
	if err != nil {
		t.Fatal(err)
	}
	if len(patches) == 0 {
		t.Fatal("empty recording")
	}

	forward, err := replay.StateAt(initial, patches, len(patches))
	if err != nil {
		t.Fatal(err)
	}
	if !deep.Equal(forward, final) {
		t.Fatalf("forward replay diverged:\n got  %+v\n want %+v", forward, final)
	}

	backward, err := replay.Rewind(forward, patches, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !deep.Equal(backward, initial) {
		t.Fatalf("rewind did not reach the initial world:\n got  %+v\n want %+v", backward, initial)
	}

	// Compacted keyframes land on the same states at their boundaries.
	compacted, err := replay.Compact(initial, patches, 8)
	if err != nil {
		t.Fatal(err)
	}
	viaKeyframes, err := replay.StateAt(initial, compacted, len(compacted))
	if err != nil {
		t.Fatal(err)
	}
	if !deep.Equal(viaKeyframes, final) {
		t.Fatalf("compacted replay diverged:\n got  %+v\n want %+v", viaKeyframes, final)
	}
}

// A cheating client is rejected server-side and nobody else sees a thing.
func TestCheaterChangesNothing(t *testing.T) {
	_, url, _ := startArena(t, WithGemTarget(0))
	ctx := context.Background()

	honest, err := client.Dial(ctx, url+"ana")
	if err != nil {
		t.Fatal(err)
	}
	defer honest.Close()
	cheat, err := client.Dial(ctx, url+"eve")
	if err != nil {
		t.Fatal(err)
	}
	defer cheat.Close()

	waitFor(t, "players present", func() bool {
		w := honest.World()
		return len(w.Players) == 2
	})
	before := honest.World().Players["eve"]

	// A hand-built teleport-and-enrich patch, sent raw.
	if err := cheat.Send(ctx, deep.Patch[world.World]{Operations: []deep.Operation{
		{Kind: deep.OpReplace, Path: "/players/eve/x", New: 0},
		{Kind: deep.OpReplace, Path: "/players/eve/score", New: 9000},
	}}); err != nil {
		t.Fatal(err)
	}

	time.Sleep(50 * time.Millisecond)
	after := honest.World().Players["eve"]
	if after.Score != 0 || after != before {
		t.Fatalf("cheat leaked into the world: %+v", after)
	}
}

// The drop path's contract: disconnecting a slow client mid-broadcast must
// not leak an unbroadcast change. The departure travels in the next tick's
// patch, so every replica — and the replay tape — stays exactly in step.
func TestSlowClientDropTravelsInAPatch(t *testing.T) {
	s := New(world.New(8, 8), WithGemTarget(0), WithSnapshotEvery(0))
	s.w.Players["slow"] = world.Player{X: 1, Y: 1}
	s.w.Players["obs"] = world.Player{X: 5, Y: 5}
	s.lastBroadcast = deep.Clone(s.w)

	slow := &conn{name: "slow", send: make(chan []byte)} // unbuffered: always full
	obs := &conn{name: "obs", send: make(chan []byte, 16)}
	s.conns[slow] = struct{}{}
	s.conns[obs] = struct{}{}

	replica := deep.Clone(s.lastBroadcast)

	// Tick 1: something changes, the broadcast finds slow's buffer full and
	// drops the connection. The world must not change under the diff's feet.
	s.w.Gems["g"] = world.Gem{X: 0, Y: 0, Value: 1}
	if err := s.step(nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.conns[slow]; ok {
		t.Fatal("slow connection was not dropped")
	}
	if _, ok := s.w.Players["slow"]; !ok {
		t.Fatal("drop mutated the world mid-tick — this change can never reach a patch")
	}

	// Tick 2: the departure rides the normal diff.
	if err := s.step(nil); err != nil {
		t.Fatal(err)
	}

	// The observer's received patches replay to exactly the broadcast state:
	// no ghost player, nothing missing.
	for {
		select {
		case frame := <-obs.send:
			if frame[0] != transport.KindTick {
				continue
			}
			var p transport.Patch
			if err := transport.Decode(frame[1:], &p); err != nil {
				t.Fatal(err)
			}
			if err := deep.Apply(&replica, p); err != nil {
				t.Fatal(err)
			}
			continue
		default:
		}
		break
	}
	if _, ok := replica.Players["slow"]; ok {
		t.Fatalf("replica still holds the dropped player: %+v", replica.Players)
	}
	if !deep.Equal(replica, s.lastBroadcast) {
		t.Fatalf("replica diverged from broadcast state:\n got  %+v\n want %+v", replica, s.lastBroadcast)
	}
}
