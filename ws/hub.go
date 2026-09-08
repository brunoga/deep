package deepws

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/brunoga/deep/v6/crdt"
	"github.com/brunoga/deep/v6/crdt/hlc"
	"github.com/coder/websocket"
)

// Hub serves rooms, each holding one document. It is an http.Handler; the
// room is named by the request's "room" query parameter.
type Hub struct {
	mu    sync.Mutex
	rooms map[string]*room

	// acceptOptions are passed to the websocket accept; a caller that fronts
	// the hub with its own origin checks can loosen or tighten them.
	acceptOptions *websocket.AcceptOptions
	// auth, when set, decides whether a request may join a room.
	auth func(r *http.Request, room string) error
	// pingInterval paces the liveness probe on every connection.
	pingInterval time.Duration
	// evictAfter and onEvict, when set, drop a room that has sat empty.
	evictAfter time.Duration
	onEvict    func(name string, doc *crdt.Document)
}

type room struct {
	mu  sync.Mutex
	doc *crdt.Document
	// emptySince ticks up each time the room empties; an eviction fires only
	// if nobody joined in between.
	emptySince uint64
	// conns maps each live connection to its send queue.
	conns map[*conn]struct{}
	// presence holds each connection's last announcement, replayed to a
	// joiner so it sees who is in the room without waiting for their next
	// heartbeat. The hub never looks inside.
	presence map[*conn][]byte
}

type conn struct {
	send chan []byte
}

// HubOption configures a Hub.
type HubOption func(*Hub)

// WithAcceptOptions sets the websocket accept options — origin patterns,
// compression — used for every connection.
func WithAcceptOptions(opts *websocket.AcceptOptions) HubOption {
	return func(h *Hub) { h.acceptOptions = opts }
}

// WithAuth installs a per-request check, called before the websocket upgrade
// with the request and the room it names. A non-nil error refuses the
// connection with 403. Origin policy belongs in [WithAcceptOptions]; this is
// for the authorization the request itself carries — a token in a header, a
// session cookie.
func WithAuth(fn func(r *http.Request, room string) error) HubOption {
	return func(h *Hub) { h.auth = fn }
}

// WithPingInterval sets how often the hub probes each connection for
// liveness. A connection whose peer stops answering is closed, which frees
// its room slot — without the probe, a silently dead TCP connection holds it
// until the operating system gives up, which can be never. The default is 20
// seconds; zero disables the probe.
func WithPingInterval(d time.Duration) HubOption {
	return func(h *Hub) { h.pingInterval = d }
}

// WithRoomEviction drops a room after it has sat empty for idle, calling
// onEvict with the document first so the host can persist it. Without this a
// hub keeps every room it has ever served; with it, a room's state lives in
// the host's store between sessions and the next joiner starts a fresh room
// the host can seed from that store via [Hub.Room].
//
// Every room is on the clock, including one that no client ever joined —
// seeded ahead of time, or created by a request that never became a
// websocket. Reaching for a room through [Hub.Room] postpones its eviction
// but cannot cancel it: a host that lists or snapshots its rooms is not
// thereby keeping them alive forever.
func WithRoomEviction(idle time.Duration, onEvict func(name string, doc *crdt.Document)) HubOption {
	return func(h *Hub) {
		h.evictAfter = idle
		h.onEvict = onEvict
	}
}

// NewHub returns an empty hub.
func NewHub(opts ...HubOption) *Hub {
	h := &Hub{rooms: make(map[string]*room), pingInterval: 20 * time.Second}
	for _, o := range opts {
		o(h)
	}
	return h
}

// Room runs fn with the named room's document under the room's lock, creating
// the room if needed. It is how a host application seeds a room before the
// first client arrives, or snapshots one for persistence — clients apply
// updates to the same document concurrently, so access goes through here.
//
// fn runs under the room's lock, so it must not call back into the hub —
// that inverts the lock order the eviction timer takes and can deadlock.
//
// Looking at a room does not keep it alive: being in one does. A host that
// reads its rooms on a schedule — a document listing, a metrics sweep — would
// otherwise postpone every eviction it touched. The consequence for seeding
// is that a room seeded a moment before a client joins may be evicted in
// between, and the client then finds an empty room; hosts seed on every join
// for that reason, which the CRDT makes free.
func (h *Hub) Room(name string, fn func(*crdt.Document)) {
	r := h.room(name)
	r.mu.Lock()
	defer r.mu.Unlock()
	fn(r.doc)
}

// room returns the named room, creating it if it does not exist. Looking at a
// room does not postpone its eviction — see [Hub.Room].
func (h *Hub) room(name string) *room {
	r, _ := h.roomFor(name, false)
	return r
}

// roomFor returns the named room. joining marks the caller as somebody about
// to occupy it rather than merely look at it.
func (h *Hub) roomFor(name string, joining bool) (*room, bool) {
	h.mu.Lock()
	r, ok := h.rooms[name]
	if !ok {
		r = &room{
			doc:      crdt.NewDocument(hlc.NewClock("hub:" + name)),
			conns:    make(map[*conn]struct{}),
			presence: make(map[*conn][]byte),
		}
		h.rooms[name] = r
		mark := r.emptySince
		h.mu.Unlock()
		if !joining {
			// A room created without a connection starts on the eviction
			// clock at once: one seeded speculatively, or made by a request
			// that never finished its upgrade, is collected like any other
			// rather than held for the life of the process.
			//
			// A room created *by* a joiner is not armed here. That connection
			// has not finished its handshake yet and holds no slot in the
			// room, so arming would let a slow client have its own room
			// evicted out from under it — it would then sync into a room the
			// hub has already forgotten, invisible to everybody else. Its
			// departure arms the clock instead, and every connection reaches
			// its departure.
			h.armEviction(name, r, mark)
		}
		return r, false
	}
	if joining {
		// Handing the room to a joiner invalidates any armed eviction, before
		// the connection is registered: the eviction timer checks the
		// generation while holding both locks, so a room retrieved here can
		// no longer be deleted out from under its new user.
		//
		// Only for a joiner. A host that looks at its rooms — to list them,
		// to snapshot them — would otherwise postpone every eviction it
		// touched, and a listing that runs often enough would postpone them
		// all forever.
		r.mu.Lock()
		r.emptySince++
		r.mu.Unlock()
	}
	h.mu.Unlock()
	return r, true
}

// ServeHTTP upgrades the connection and runs the sync protocol until the
// client goes away.
func (h *Hub) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	roomName := req.URL.Query().Get("room")
	if roomName == "" {
		http.Error(w, "missing room parameter", http.StatusBadRequest)
		return
	}
	if h.auth != nil {
		if err := h.auth(req, roomName); err != nil {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
	}

	sock, err := websocket.Accept(w, req, h.acceptOptions)
	if err != nil {
		return // Accept already replied
	}
	defer sock.Close(websocket.StatusInternalError, "hub closing")

	r, _ := h.roomFor(roomName, true)
	c := &conn{send: make(chan []byte, 64)}

	ctx, cancel := context.WithCancel(req.Context())
	defer cancel()

	// The liveness probe: a peer that stops answering pings gets its
	// connection closed, which unblocks the read below and frees the slot.
	if h.pingInterval > 0 {
		go func() {
			ticker := time.NewTicker(h.pingInterval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					pingCtx, pingCancel := context.WithTimeout(ctx, h.pingInterval)
					err := sock.Ping(pingCtx)
					pingCancel()
					if err != nil {
						// CloseNow: no close handshake with a peer that has
						// already stopped answering.
						sock.CloseNow()
						return
					}
				}
			}
		}()
	}

	// The writer: one goroutine owns the socket's write side, fed by the send
	// queue, so broadcasts from other connections never interleave writes.
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for msg := range c.send {
			if err := sock.Write(ctx, websocket.MessageBinary, msg); err != nil {
				return
			}
		}
		sock.Close(websocket.StatusNormalClosure, "")
	}()

	err = h.serve(ctx, sock, r, c)

	h.detach(roomName, r, c)
	close(c.send)
	<-writerDone
	if err != nil && websocket.CloseStatus(err) == -1 {
		sock.Close(websocket.StatusInternalError, "sync error")
	}
}

// serve runs the protocol: handshake, then relay until the connection drops.
func (h *Hub) serve(ctx context.Context, sock *websocket.Conn, r *room, c *conn) error {
	// 1. The client says what it has seen.
	kind, payload, err := readFrame(ctx, sock)
	if err != nil {
		return err
	}
	if kind != frameStateVector {
		return fmt.Errorf("deepws: handshake expected a state vector, got frame %d", kind)
	}
	var clientSV crdt.StateVector
	if err := clientSV.UnmarshalBinary(payload); err != nil {
		return err
	}

	// 2. The hub answers with what the client is missing, its own vector so
	// the client can answer in kind, and the room's presence.
	r.mu.Lock()
	missing := r.doc.Since(clientSV)
	hubSV := r.doc.StateVector()
	pres := make([][]byte, 0, len(r.presence))
	for _, p := range r.presence {
		pres = append(pres, p)
	}
	r.conns[c] = struct{}{}
	r.emptySince++ // invalidate any armed eviction: the room is live again
	r.mu.Unlock()

	if !missing.IsEmpty() {
		frame, err := encodeUpdate(missing)
		if err != nil {
			return err
		}
		c.send <- frame
	}
	svFrame, err := encodeSV(hubSV)
	if err != nil {
		return err
	}
	c.send <- svFrame
	for _, p := range pres {
		c.send <- encodeFrame(framePresence, p)
	}

	// 3. Steady state: updates are applied to the room and relayed; presence
	// is cached and relayed.
	for {
		kind, payload, err := readFrame(ctx, sock)
		if err != nil {
			return err
		}
		switch kind {
		case frameUpdate:
			var u crdt.Update
			if err := u.UnmarshalBinary(payload); err != nil {
				return err
			}
			r.mu.Lock()
			r.doc.Apply(u)
			r.broadcastLocked(c, encodeFrame(frameUpdate, payload))
			r.mu.Unlock()
		case framePresence:
			r.mu.Lock()
			r.presence[c] = payload
			r.broadcastLocked(c, encodeFrame(framePresence, payload))
			r.mu.Unlock()
		case frameStateVector:
			// A client may re-sync mid-session; answer with the delta.
			var sv crdt.StateVector
			if err := sv.UnmarshalBinary(payload); err != nil {
				return err
			}
			r.mu.Lock()
			diff := r.doc.Since(sv)
			r.mu.Unlock()
			if !diff.IsEmpty() {
				frame, err := encodeUpdate(diff)
				if err != nil {
					return err
				}
				c.send <- frame
			}
		default:
			return fmt.Errorf("deepws: unknown frame %d", kind)
		}
	}
}

// broadcastLocked queues a frame for every connection but from. A connection
// whose queue is full is dropped from the room rather than allowed to stall
// everyone else; it will reconnect and resync, which the handshake makes
// cheap.
func (r *room) broadcastLocked(from *conn, frame []byte) {
	for c := range r.conns {
		if c == from {
			continue
		}
		select {
		case c.send <- frame:
		default:
			delete(r.conns, c)
			delete(r.presence, c)
		}
	}
}

// detach removes the connection and, when the room empties and eviction is
// configured, arms the eviction timer. The emptySince counter defeats the
// obvious race: a joiner between emptying and firing bumps it, and the timer
// finds its number stale and does nothing.
func (h *Hub) detach(name string, r *room, c *conn) {
	r.mu.Lock()
	delete(r.conns, c)
	delete(r.presence, c)
	empty := len(r.conns) == 0
	var mark uint64
	if empty {
		r.emptySince++
		mark = r.emptySince
	}
	r.mu.Unlock()

	if !empty {
		return
	}
	h.armEviction(name, r, mark)
}

// armEviction schedules the check that drops a room which has sat empty since
// generation mark.
func (h *Hub) armEviction(name string, r *room, mark uint64) {
	if h.evictAfter <= 0 {
		return
	}
	time.AfterFunc(h.evictAfter, func() {
		// Check and delete under both locks, h.mu first (the order every
		// other path uses): a join in flight has either already bumped the
		// generation through room() — the check fails — or has not reached
		// room() yet and will get a fresh room after the delete. Either way,
		// nobody is left holding a room the map no longer knows.
		h.mu.Lock()
		r.mu.Lock()
		// current guards the second of two timers armed against the same
		// room: the first evicted it, and this one must not evict it again —
		// nor arm a third against a room the hub has already let go.
		current := h.rooms[name] == r
		still := current && len(r.conns) == 0 && r.emptySince == mark
		doc := r.doc
		if still {
			delete(h.rooms, name)
		}
		r.mu.Unlock()
		h.mu.Unlock()
		if still && h.onEvict != nil {
			h.onEvict(name, doc)
		}
	})
}

func readFrame(ctx context.Context, sock *websocket.Conn) (byte, []byte, error) {
	typ, data, err := sock.Read(ctx)
	if err != nil {
		return 0, nil, err
	}
	if typ != websocket.MessageBinary || len(data) == 0 {
		return 0, nil, fmt.Errorf("deepws: expected a non-empty binary frame")
	}
	return data[0], data[1:], nil
}
