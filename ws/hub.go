package deepws

import (
	"context"
	"fmt"
	"net/http"
	"sync"

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
}

type room struct {
	mu  sync.Mutex
	doc *crdt.Document
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

// NewHub returns an empty hub.
func NewHub(opts ...HubOption) *Hub {
	h := &Hub{rooms: make(map[string]*room)}
	for _, o := range opts {
		o(h)
	}
	return h
}

// Room runs fn with the named room's document under the room's lock, creating
// the room if needed. It is how a host application seeds a room before the
// first client arrives, or snapshots one for persistence — clients apply
// updates to the same document concurrently, so access goes through here.
func (h *Hub) Room(name string, fn func(*crdt.Document)) {
	r := h.room(name)
	r.mu.Lock()
	defer r.mu.Unlock()
	fn(r.doc)
}

func (h *Hub) room(name string) *room {
	h.mu.Lock()
	defer h.mu.Unlock()
	r, ok := h.rooms[name]
	if !ok {
		r = &room{
			doc:      crdt.NewDocument(hlc.NewClock("hub:" + name)),
			conns:    make(map[*conn]struct{}),
			presence: make(map[*conn][]byte),
		}
		h.rooms[name] = r
	}
	return r
}

// ServeHTTP upgrades the connection and runs the sync protocol until the
// client goes away.
func (h *Hub) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	roomName := req.URL.Query().Get("room")
	if roomName == "" {
		http.Error(w, "missing room parameter", http.StatusBadRequest)
		return
	}

	sock, err := websocket.Accept(w, req, h.acceptOptions)
	if err != nil {
		return // Accept already replied
	}
	defer sock.Close(websocket.StatusInternalError, "hub closing")

	r := h.room(roomName)
	c := &conn{send: make(chan []byte, 64)}

	ctx := req.Context()

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

	r.detach(c)
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

func (r *room) detach(c *conn) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.conns, c)
	delete(r.presence, c)
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
