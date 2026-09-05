package deepws

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/brunoga/deep/v6/crdt"
	"github.com/brunoga/deep/v6/crdt/hlc"
	"github.com/coder/websocket"
)

// Client keeps a local document converged with a hub room, and carries
// presence for the peers editing alongside. P is the presence state — a
// cursor, a name — and is this client's business alone: the hub relays it
// without looking.
type Client[P any] struct {
	sock *websocket.Conn

	mu        sync.Mutex
	doc       *crdt.Document
	published crdt.StateVector
	onUpdate  func()

	awareness *crdt.Awareness[P]

	done chan struct{}
	err  error
}

// Dial connects to a hub and completes the sync handshake: whatever the room
// has that this client does not arrives before Dial returns, and whatever
// this client has that the room does not — offline edits, on a reconnect — is
// sent up.
//
// node identifies this client's edits and presence; reuse the same id across
// reconnects so its clock keeps counting from where it left off.
func Dial[P any](ctx context.Context, url, node string) (*Client[P], error) {
	sock, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		return nil, err
	}

	c := &Client[P]{
		sock:      sock,
		doc:       crdt.NewDocument(hlc.NewClock(node)),
		awareness: crdt.NewAwareness[P](node),
		done:      make(chan struct{}),
	}

	if err := c.handshake(ctx); err != nil {
		sock.Close(websocket.StatusProtocolError, "handshake failed")
		return nil, err
	}

	go c.readLoop()
	return c, nil
}

func (c *Client[P]) handshake(ctx context.Context) error {
	// 1. Say what we have seen.
	frame, err := encodeSV(c.doc.StateVector())
	if err != nil {
		return err
	}
	if err := c.sock.Write(ctx, websocket.MessageBinary, frame); err != nil {
		return err
	}

	// 2. The hub sends what we are missing (perhaps nothing) and its own
	// vector; presence replays may follow and are handled by the read loop.
	for {
		kind, payload, err := readFrame(ctx, c.sock)
		if err != nil {
			return err
		}
		switch kind {
		case frameUpdate:
			var u crdt.Update
			if err := u.UnmarshalBinary(payload); err != nil {
				return err
			}
			c.doc.Apply(u)
		case frameStateVector:
			var hubSV crdt.StateVector
			if err := hubSV.UnmarshalBinary(payload); err != nil {
				return err
			}
			// 3. Send what the hub is missing — the offline edits.
			pending := c.doc.Since(hubSV)
			if !pending.IsEmpty() {
				frame, err := encodeUpdate(pending)
				if err != nil {
					return err
				}
				if err := c.sock.Write(ctx, websocket.MessageBinary, frame); err != nil {
					return err
				}
			}
			c.published = c.doc.StateVector()
			return nil
		case framePresence:
			c.applyPresence(payload)
		default:
			return fmt.Errorf("deepws: unknown frame %d in handshake", kind)
		}
	}
}

// Edit runs fn with the document, holding the client's lock: remote updates
// land on the same document from the read loop, and crdt.Document is not
// safe for unsynchronised concurrent use. Call [Client.Publish] afterwards to
// send what fn changed.
func (c *Client[P]) Edit(fn func(*crdt.Document)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	fn(c.doc)
}

// Text returns the document's current contents.
func (c *Client[P]) Text() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.doc.String()
}

// Len returns the document's length in runes.
func (c *Client[P]) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.doc.Len()
}

// Awareness returns the presence view: peers appear, update and expire here.
// Announce this client through [Client.Announce] rather than SetLocal, so the
// announcement also reaches the room.
func (c *Client[P]) Awareness() *crdt.Awareness[P] { return c.awareness }

// OnUpdate registers fn to run after a remote update lands in the document.
// It runs on the read goroutine; keep it short and hand real work elsewhere.
func (c *Client[P]) OnUpdate(fn func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onUpdate = fn
}

// Publish sends every local edit made since the last Publish.
func (c *Client[P]) Publish(ctx context.Context) error {
	c.mu.Lock()
	pending := c.doc.Since(c.published)
	c.published = c.doc.StateVector()
	c.mu.Unlock()

	if pending.IsEmpty() {
		return nil
	}
	frame, err := encodeUpdate(pending)
	if err != nil {
		return err
	}
	return c.sock.Write(ctx, websocket.MessageBinary, frame)
}

// Announce records this client's presence state and sends it to the room. It
// is also the heartbeat: call it periodically — and on every cursor move —
// so peers keep drawing this client.
func (c *Client[P]) Announce(ctx context.Context, state P) error {
	update := c.awareness.SetLocal(state)
	payload, err := json.Marshal(update)
	if err != nil {
		return err
	}
	return c.sock.Write(ctx, websocket.MessageBinary, encodeFrame(framePresence, payload))
}

// Close says goodbye — so peers drop this client at once instead of waiting
// out the presence timeout — and closes the connection.
func (c *Client[P]) Close(ctx context.Context) error {
	leave := c.awareness.Leave()
	if payload, err := json.Marshal(leave); err == nil {
		_ = c.sock.Write(ctx, websocket.MessageBinary, encodeFrame(framePresence, payload))
	}
	err := c.sock.Close(websocket.StatusNormalClosure, "bye")
	<-c.done
	return err
}

// Err reports why the read loop stopped, once it has.
func (c *Client[P]) Err() error {
	select {
	case <-c.done:
		return c.err
	default:
		return nil
	}
}

// Done is closed when the connection is over.
func (c *Client[P]) Done() <-chan struct{} { return c.done }

func (c *Client[P]) readLoop() {
	defer close(c.done)
	ctx := context.Background()
	for {
		kind, payload, err := readFrame(ctx, c.sock)
		if err != nil {
			c.err = err
			return
		}
		switch kind {
		case frameUpdate:
			var u crdt.Update
			if err := u.UnmarshalBinary(payload); err != nil {
				c.err = err
				return
			}
			c.mu.Lock()
			before := c.doc.StateVector()
			c.doc.Apply(u)
			after := c.doc.StateVector()
			// What arrived is now seen — without this, the next Publish would
			// echo the room's changes back at it. But only origins with no
			// unpublished local edits advance: taking the whole vector would
			// also mark local edits as published without ever sending them.
			for origin, n := range after {
				if before[origin] == c.published[origin] {
					if c.published == nil {
						c.published = crdt.StateVector{}
					}
					c.published[origin] = n
				}
			}
			fn := c.onUpdate
			c.mu.Unlock()
			if fn != nil {
				fn()
			}
		case framePresence:
			c.applyPresence(payload)
		case frameStateVector:
			// Informational mid-session; nothing to do.
		default:
			c.err = fmt.Errorf("deepws: unknown frame %d", kind)
			return
		}
	}
}

func (c *Client[P]) applyPresence(payload []byte) {
	var u crdt.AwarenessUpdate[P]
	if err := json.Unmarshal(payload, &u); err != nil {
		return // a peer with a different P; not ours to read
	}
	c.awareness.Apply(u)
}
