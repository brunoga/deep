// Package deepws is a websocket sync provider for collaborative documents: a
// hub that relays [crdt.Document] updates and presence between the clients of
// a room, and a client that keeps a local document converged with it.
//
// It is a separate module on purpose. A transport means owning a network
// dependency, and most users of deep never open a socket — the core stays
// dependency-free, and the integration point this package fills is the one
// examples/websocket_sync sketches.
//
// The protocol is three frame kinds over binary websocket messages, each one
// byte of type followed by its payload:
//
//   - state vector — the compact binary form; "this is what I have seen"
//   - update — the compact binary form; "this is what you are missing"
//   - presence — opaque bytes relayed to the room, carrying an
//     [crdt.AwarenessUpdate] the server never decodes
//
// A connecting client sends its state vector; the hub answers with what the
// client is missing and its own vector, the client sends back what the hub is
// missing — offline edits survive a reconnect — and from there both sides
// stream deltas as they happen. Presence is relayed, never stored durably:
// the hub caches each connection's last announcement so a joiner sees the
// room, and expiry is every client's own affair, which is safe because
// presence is ephemeral.
package deepws

import (
	"github.com/brunoga/deep/v6/crdt"
)

// Frame kinds on the wire.
const (
	frameStateVector byte = 1
	frameUpdate      byte = 2
	framePresence    byte = 3
)

func encodeFrame(kind byte, payload []byte) []byte {
	out := make([]byte, 0, 1+len(payload))
	out = append(out, kind)
	return append(out, payload...)
}

func encodeSV(sv crdt.StateVector) ([]byte, error) {
	data, err := sv.MarshalBinary()
	if err != nil {
		return nil, err
	}
	return encodeFrame(frameStateVector, data), nil
}

func encodeUpdate(u crdt.Update) ([]byte, error) {
	data, err := u.MarshalBinary()
	if err != nil {
		return nil, err
	}
	return encodeFrame(frameUpdate, data), nil
}
