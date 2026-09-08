// Package client keeps a replica of the arena current by applying the
// server's tick patches — the receiving half of the diff/apply loop.
package client

import (
	"context"
	"fmt"
	"sync"

	"github.com/coder/websocket"

	deep "github.com/brunoga/deep/v6"

	"github.com/brunoga/deep/examples/arena/game"
	"github.com/brunoga/deep/examples/arena/transport"
	"github.com/brunoga/deep/examples/arena/world"
)

// RingSize is how many recent tick patches a client retains — the material
// for instant replay.
const RingSize = 100

// Client is one connected player.
type Client struct {
	sock *websocket.Conn
	you  string

	mu      sync.Mutex
	replica world.World
	ring    []transport.Patch // last RingSize tick patches, oldest first
	drift   int
	onTick  func()

	done chan struct{}
	err  error
}

// Dial joins an arena. The hello frame carries this client's id and the
// exact world the patch stream continues from.
func Dial(ctx context.Context, url string) (*Client, error) {
	sock, _, err := websocket.Dial(ctx, url, nil)
	if err != nil {
		return nil, err
	}
	kind, body, err := transport.Read(ctx, sock)
	if err != nil {
		sock.CloseNow()
		return nil, err
	}
	if kind != transport.KindHello {
		sock.CloseNow()
		return nil, fmt.Errorf("client: expected hello, got frame %d", kind)
	}
	var hello transport.Hello
	if err := transport.Decode(body, &hello); err != nil {
		sock.CloseNow()
		return nil, err
	}

	c := &Client{sock: sock, you: hello.You, replica: hello.World, done: make(chan struct{})}
	go c.readLoop()
	return c, nil
}

// You returns this client's player id.
func (c *Client) You() string { return c.you }

// World returns a copy of the replica; the caller can render it, diff it,
// or build actions against it without holding anything.
func (c *Client) World() world.World {
	c.mu.Lock()
	defer c.mu.Unlock()
	return deep.Clone(c.replica)
}

// Snapshot returns the replica and the retained tick patches (oldest first)
// as one consistent pair — the material for instant replay. Taking them in
// separate calls would let a tick land in between, leaving patches that do
// not match the world they are meant to rewind.
func (c *Client) Snapshot() (world.World, []transport.Patch) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ring := make([]transport.Patch, len(c.ring))
	copy(ring, c.ring)
	return deep.Clone(c.replica), ring
}

// Drift reports how many snapshot checks found the replica out of sync. It
// staying zero is the diff/apply contract holding.
func (c *Client) Drift() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.drift
}

// OnTick registers fn to run after each applied tick patch, on the read
// goroutine.
func (c *Client) OnTick(fn func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onTick = fn
}

// Move sends the one-step action built against the current replica —
// conditions and gem pickup included. It reports whether a legal move could
// be built at all.
func (c *Client) Move(ctx context.Context, dx, dy int) (bool, error) {
	c.mu.Lock()
	patch, ok := game.Move(c.replica, c.you, dx, dy)
	c.mu.Unlock()
	if !ok {
		return false, nil
	}
	return true, c.Send(ctx, patch)
}

// Send submits an arbitrary action patch. The server's rules judge it the
// same as any other — which is the point: a client that sends nonsense
// changes nothing.
func (c *Client) Send(ctx context.Context, patch transport.Patch) error {
	frame, err := transport.Encode(transport.KindAction, patch)
	if err != nil {
		return err
	}
	return c.sock.Write(ctx, websocket.MessageBinary, frame)
}

// Close leaves the arena.
func (c *Client) Close() error {
	err := c.sock.Close(websocket.StatusNormalClosure, "bye")
	<-c.done
	return err
}

// Err reports why the read loop stopped, once it has.
func (c *Client) Err() error {
	select {
	case <-c.done:
		return c.err
	default:
		return nil
	}
}

// Done is closed when the connection is over.
func (c *Client) Done() <-chan struct{} { return c.done }

func (c *Client) readLoop() {
	defer close(c.done)
	ctx := context.Background()
	for {
		kind, body, err := transport.Read(ctx, c.sock)
		if err != nil {
			c.err = err
			return
		}
		switch kind {
		case transport.KindTick:
			var patch transport.Patch
			if err := transport.Decode(body, &patch); err != nil {
				c.err = err
				return
			}
			c.mu.Lock()
			if err := deep.Apply(&c.replica, patch); err != nil {
				c.mu.Unlock()
				c.err = fmt.Errorf("client: applying tick patch: %w", err)
				return
			}
			c.ring = append(c.ring, patch)
			if len(c.ring) > RingSize {
				c.ring = c.ring[1:]
			}
			fn := c.onTick
			c.mu.Unlock()
			if fn != nil {
				fn()
			}
		case transport.KindSnapshot:
			var snap world.World
			if err := transport.Decode(body, &snap); err != nil {
				c.err = err
				return
			}
			c.mu.Lock()
			// The drift check: a replica built purely from patches must equal
			// the server's world, bit for bit. If it ever does not, the
			// snapshot resynchronizes — and Drift records that the contract
			// broke, which the tests treat as failure.
			if !deep.Equal(c.replica, snap) {
				c.drift++
				c.replica = snap
				// The retained patches describe the abandoned timeline;
				// rewinding the resynchronized world through them would render
				// states that never existed.
				c.ring = nil
			}
			c.mu.Unlock()
		default:
			c.err = fmt.Errorf("client: unknown frame %d", kind)
			return
		}
	}
}
