// Package transport is arena's wire protocol: one byte of frame kind, then a
// gob body. Gob is the natural encoding for a Go-to-Go game loop — patches
// carry their operations through it untouched, and nobody pays for JSON
// field names sixty times a second.
package transport

import (
	"bytes"
	"context"
	"encoding/gob"
	"fmt"

	"github.com/coder/websocket"

	deep "github.com/brunoga/deep/v6"

	"github.com/brunoga/deep/examples/arena/world"
)

// Frame kinds.
const (
	// KindHello is the server's first frame: your id and the world as the
	// patch stream will see it.
	KindHello byte = 1
	// KindTick carries one tick's patch, server to client.
	KindTick byte = 2
	// KindAction carries one action patch, client to server.
	KindAction byte = 3
	// KindSnapshot carries a periodic full world, for drift checking.
	KindSnapshot byte = 4
)

// Hello is the join handshake payload.
type Hello struct {
	You   string
	World world.World
}

// Encode renders a frame: kind byte, then the gob of v.
func Encode(kind byte, v any) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte(kind)
	if err := gob.NewEncoder(&buf).Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Read pulls one frame off the socket and returns its kind and gob body.
func Read(ctx context.Context, sock *websocket.Conn) (byte, []byte, error) {
	typ, data, err := sock.Read(ctx)
	if err != nil {
		return 0, nil, err
	}
	if typ != websocket.MessageBinary || len(data) == 0 {
		return 0, nil, fmt.Errorf("transport: expected a non-empty binary frame")
	}
	return data[0], data[1:], nil
}

// Decode unpacks a frame body into v.
func Decode(data []byte, v any) error {
	return gob.NewDecoder(bytes.NewReader(data)).Decode(v)
}

// A tick patch's concrete type, named once so both sides agree.
type Patch = deep.Patch[world.World]
