// Package server is arena's authority: one goroutine owns the world, applies
// every action, and broadcasts each tick's diff.
//
// The synchronization design is the whole example. The server never sends
// state after the join handshake — it sends deep.Diff(lastBroadcast, world)
// once per tick, and every client folds the patch into its replica. There is
// no dirty-flag bookkeeping anywhere in the game logic: mutate the world
// however the rules allow, and the diff finds what changed.
package server

import (
	"context"
	"log"
	"math/rand/v2"
	"net/http"
	"time"

	"github.com/coder/websocket"

	deep "github.com/brunoga/deep/v6"

	"github.com/brunoga/deep/examples/arena/game"
	"github.com/brunoga/deep/examples/arena/replay"
	"github.com/brunoga/deep/examples/arena/transport"
	"github.com/brunoga/deep/examples/arena/world"
)

// Option configures a Server.
type Option func(*Server)

// WithTick sets the tick interval. The default is 100ms.
func WithTick(d time.Duration) Option {
	return func(s *Server) { s.tickEvery = d }
}

// WithSnapshotEvery sets how many ticks pass between full-world snapshot
// frames — the periodic proof that a replica has not drifted. Zero disables
// them. The default is 100.
func WithSnapshotEvery(n int) Option {
	return func(s *Server) { s.snapshotEvery = n }
}

// WithGemTarget sets how many gems the spawner keeps on the floor.
func WithGemTarget(n int) Option {
	return func(s *Server) { s.gemTarget = n }
}

// WithSeed makes the run deterministic — spawn cells, gem values, join
// positions.
func WithSeed(seed uint64) Option {
	return func(s *Server) { s.rng = rand.New(rand.NewPCG(seed, seed)) }
}

// WithReplay records the match: the initial world, then every tick patch.
func WithReplay(w *replay.Writer) Option {
	return func(s *Server) { s.replay = w }
}

// Server runs one arena. It is an http.Handler; clients connect to it as a
// websocket with a "name" query parameter.
type Server struct {
	tickEvery     time.Duration
	snapshotEvery int
	gemTarget     int
	rng           *rand.Rand
	replay        *replay.Writer

	// The game loop owns everything below; other goroutines reach it only
	// through the channels.
	w             world.World
	lastBroadcast world.World
	conns         map[*conn]struct{}
	// dropped holds players disconnected mid-broadcast; their departure is
	// applied to the world at the top of the next tick, so it travels in a
	// patch like every other change.
	dropped []string

	joins     chan joinReq
	leaves    chan *conn
	actions   chan actionReq
	mutations chan func(*world.World)

	done chan struct{}
}

type conn struct {
	name string
	send chan []byte
}

type joinReq struct {
	c     *conn
	reply chan joinReply
}

type joinReply struct {
	hello transport.Hello
	err   error
}

type actionReq struct {
	c     *conn
	patch transport.Patch
}

// New builds a server around an initial world.
func New(initial world.World, opts ...Option) *Server {
	s := &Server{
		tickEvery:     100 * time.Millisecond,
		snapshotEvery: 100,
		gemTarget:     10,
		rng:           rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64())),
		w:             deep.Clone(initial),
		conns:         map[*conn]struct{}{},
		joins:         make(chan joinReq),
		leaves:        make(chan *conn, 16),
		actions:       make(chan actionReq, 256),
		mutations:     make(chan func(*world.World)),
		done:          make(chan struct{}),
	}
	for _, o := range opts {
		o(s)
	}
	s.lastBroadcast = deep.Clone(s.w)
	return s
}

// Mutate runs fn inside the game loop, with the world — the host's own
// hands: seeding a level, an admin command, a scripted event. Whatever fn
// changes reaches every client in the next tick's patch, exactly like a
// player action; a read-only fn is how a host snapshots the world. It blocks
// until fn has run, or returns without running it if the loop has stopped.
func (s *Server) Mutate(fn func(*world.World)) {
	ran := make(chan struct{})
	select {
	case s.mutations <- func(w *world.World) { fn(w); close(ran) }:
		<-ran
	case <-s.done:
	}
}

// Run drives the game loop until ctx ends. It must be running for clients to
// join.
func (s *Server) Run(ctx context.Context) error {
	defer close(s.done)
	// On exit, release every connection's writer: their handlers wait on the
	// writer, the writer waits on the send channel, and nothing else will
	// ever close it once this loop stops.
	defer func() {
		for c := range s.conns {
			close(c.send)
		}
	}()
	if s.replay != nil {
		if err := s.replay.Init(s.lastBroadcast); err != nil {
			return err
		}
	}
	spawner := game.NewSpawner(s.gemTarget, s.rng)
	ticker := time.NewTicker(s.tickEvery)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case req := <-s.joins:
			_, err := game.Join(&s.w, req.c.name, s.rng)
			if err == nil {
				s.conns[req.c] = struct{}{}
			}
			// The hello world is lastBroadcast, not the live world: the
			// client must start exactly where the patch stream stands, and
			// its own join reaches it inside the next tick's patch like any
			// other change.
			req.reply <- joinReply{
				hello: transport.Hello{You: req.c.name, World: deep.Clone(s.lastBroadcast)},
				err:   err,
			}

		case c := <-s.leaves:
			if _, ok := s.conns[c]; ok {
				delete(s.conns, c)
				game.Leave(&s.w, c.name)
				close(c.send) // releases the connection's writer
			}

		case fn := <-s.mutations:
			fn(&s.w)

		case req := <-s.actions:
			if _, ok := s.conns[req.c]; !ok {
				continue // already gone
			}
			if _, err := game.Apply(&s.w, req.c.name, req.patch); err != nil {
				// A rejection is the rules working, not a server fault; the
				// world is untouched and the client learns the truth from
				// the next tick.
				log.Printf("arena: rejected action from %s: %v", req.c.name, err)
			}

		case <-ticker.C:
			if err := s.step(spawner); err != nil {
				return err
			}
		}
	}
}

// step is one tick: departures queued during the previous broadcast join the
// world's changes, the spawner drips, the clock advances, and the diff goes
// out.
func (s *Server) step(spawner *game.Spawner) error {
	for _, name := range s.dropped {
		game.Leave(&s.w, name)
	}
	s.dropped = s.dropped[:0]
	if spawner != nil {
		spawner.Refill(&s.w)
	}
	s.w.Tick++
	return s.tick()
}

// tick diffs, broadcasts, records.
func (s *Server) tick() error {
	patch, err := deep.Diff(s.lastBroadcast, s.w)
	if err != nil {
		return err
	}
	if patch.IsEmpty() {
		return nil
	}
	frame, err := transport.Encode(transport.KindTick, patch)
	if err != nil {
		return err
	}
	s.broadcast(frame)
	if s.replay != nil {
		if err := s.replay.Append(patch); err != nil {
			return err
		}
	}
	s.lastBroadcast = deep.Clone(s.w)

	if s.snapshotEvery > 0 && s.w.Tick%int64(s.snapshotEvery) == 0 {
		snap, err := transport.Encode(transport.KindSnapshot, s.lastBroadcast)
		if err != nil {
			return err
		}
		s.broadcast(snap)
	}
	return nil
}

// broadcast queues a frame for every connection. One that cannot keep up is
// dropped rather than allowed to stall the tick; it can reconnect and get a
// fresh hello.
//
// The drop must not touch the world here: this runs after the tick's diff
// was computed and recorded, and a change made now would be absorbed into
// lastBroadcast without ever travelling in a patch — every other client
// would render a ghost player, and the replay tape would diverge from the
// world it claims to describe. The departure is queued instead and becomes
// part of the next tick's diff.
func (s *Server) broadcast(frame []byte) {
	for c := range s.conns {
		select {
		case c.send <- frame:
		default:
			delete(s.conns, c)
			s.dropped = append(s.dropped, c.name)
			close(c.send)
		}
	}
}

// ServeHTTP upgrades the connection, joins the player, and relays until the
// client goes away.
func (s *Server) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	name := req.URL.Query().Get("name")
	if !world.IDPattern.MatchString(name) {
		http.Error(rw, "bad or missing name", http.StatusBadRequest)
		return
	}
	sock, err := websocket.Accept(rw, req, nil)
	if err != nil {
		return
	}
	defer sock.CloseNow()

	ctx := req.Context()
	c := &conn{name: name, send: make(chan []byte, 64)}

	reply := make(chan joinReply, 1)
	select {
	case s.joins <- joinReq{c: c, reply: reply}:
	case <-s.done:
		return
	}
	r := <-reply
	if r.err != nil {
		sock.Close(websocket.StatusPolicyViolation, r.err.Error())
		return
	}
	hello, err := transport.Encode(transport.KindHello, r.hello)
	if err != nil {
		return
	}
	if err := sock.Write(ctx, websocket.MessageBinary, hello); err != nil {
		select {
		case s.leaves <- c:
		case <-s.done:
		}
		return
	}

	// Writer: one goroutine owns the socket's write side. A closed send
	// channel means the game loop dropped us.
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for frame := range c.send {
			if err := sock.Write(ctx, websocket.MessageBinary, frame); err != nil {
				return
			}
		}
		// A closed channel means the game loop dropped us — closing the
		// socket unblocks the read loop below so the handler can finish.
		sock.Close(websocket.StatusPolicyViolation, "dropped")
	}()

	// Reader: actions in, until the connection drops.
	for {
		kind, body, err := transport.Read(ctx, sock)
		if err != nil {
			break
		}
		if kind != transport.KindAction {
			break
		}
		var patch transport.Patch
		if err := transport.Decode(body, &patch); err != nil {
			break
		}
		select {
		case s.actions <- actionReq{c: c, patch: patch}:
		case <-s.done:
		}
	}

	select {
	case s.leaves <- c:
	case <-s.done:
	}
	<-writerDone
}
