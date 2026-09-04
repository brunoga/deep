package crdt

import (
	"encoding/binary"
	"fmt"

	"github.com/brunoga/deep/v5/crdt/hlc"
)

// A compact wire format for Update and StateVector.
//
// An update is mostly identifiers, and identifiers are mostly repetition. Every
// run carries a hybrid logical clock, every clock carries the node id that
// issued it, and every run that is not the first also carries the clock of the
// run it follows — so a node id written once appears in the payload as many
// times as there are runs, twice over. The wall times are 19-digit nanosecond
// counts that differ from their neighbours by microseconds.
//
// JSON spends around 120 bytes on a five-character run and cannot do better,
// because it has no way to say "the same node id as before" or "three
// microseconds later than the last one". This format says both: node ids are
// written once into a table and referred to by index, and wall times are stored
// as differences from the preceding run. That is where nearly all of the
// difference comes from; the varints are a rounding error beside it.
//
// Update and StateVector implement encoding.BinaryMarshaler and
// BinaryUnmarshaler, so anything that looks for those — gob, most cache
// clients, many message queues — uses this without being asked.

// updateFormatVersion is the first byte of every encoded value. A reader that
// does not know a version refuses the payload rather than misreading it.
const updateFormatVersion = 1

// ── encoding ─────────────────────────────────────────────────────────────────

type encoder struct {
	buf    []byte
	nodes  map[string]uint64
	order  []string
	prevWT int64 // the last wall time written, for delta encoding
}

func newEncoder() *encoder {
	return &encoder{nodes: make(map[string]uint64)}
}

// intern returns the table index for a node id, adding it if new.
func (e *encoder) intern(node string) uint64 {
	if i, ok := e.nodes[node]; ok {
		return i
	}
	i := uint64(len(e.order))
	e.nodes[node] = i
	e.order = append(e.order, node)
	return i
}

func (e *encoder) uvarint(v uint64) { e.buf = binary.AppendUvarint(e.buf, v) }
func (e *encoder) varint(v int64)   { e.buf = binary.AppendVarint(e.buf, v) }

func (e *encoder) str(s string) {
	e.uvarint(uint64(len(s)))
	e.buf = append(e.buf, s...)
}

// clock writes an HLC as a node index, a wall time relative to the previous
// clock written, and a logical counter.
func (e *encoder) clock(c hlc.HLC) {
	e.uvarint(e.intern(c.NodeID))
	e.varint(c.WallTime - e.prevWT)
	e.prevWT = c.WallTime
	e.uvarint(uint64(c.Logical))
}

// nodeTable writes the interned node ids. It runs after the body, because the
// body is what discovers them, and is placed before it on the wire.
func (e *encoder) nodeTable() []byte {
	var out []byte
	out = binary.AppendUvarint(out, uint64(len(e.order)))
	for _, n := range e.order {
		out = binary.AppendUvarint(out, uint64(len(n)))
		out = append(out, n...)
	}
	return out
}

// finish assembles the payload: version, node table, body.
func (e *encoder) finish() []byte {
	table := e.nodeTable()
	out := make([]byte, 0, 1+len(table)+len(e.buf))
	out = append(out, updateFormatVersion)
	out = append(out, table...)
	return append(out, e.buf...)
}

// Run flags, packed into one byte so that the common run — no anchor, not
// deleted — costs a single byte for all three.
const (
	flagHasPrev = 1 << iota
	flagDeleted
	flagHasN
)

// MarshalBinary encodes the update in the compact format described above.
func (u Update) MarshalBinary() ([]byte, error) {
	e := newEncoder()

	e.uvarint(uint64(len(u.Runs)))
	for _, r := range u.Runs {
		var flags byte
		if r.Prev != (hlc.HLC{}) {
			flags |= flagHasPrev
		}
		if r.Deleted {
			flags |= flagDeleted
		}
		if r.N != 0 {
			flags |= flagHasN
		}
		e.buf = append(e.buf, flags)

		e.clock(r.ID)
		e.str(r.Value)
		if flags&flagHasPrev != 0 {
			e.clock(r.Prev)
		}
		if flags&flagHasN != 0 {
			e.uvarint(uint64(r.N))
		}
	}

	e.uvarint(uint64(len(u.Deleted)))
	for _, d := range u.Deleted {
		e.clock(d.ID)
		e.uvarint(uint64(d.N))
	}

	return e.finish(), nil
}

// MarshalBinary encodes a state vector: one entry per node, with the counter
// each has been seen up to.
func (sv StateVector) MarshalBinary() ([]byte, error) {
	e := newEncoder()
	e.uvarint(uint64(len(sv)))
	// Sorted so that the same vector always encodes to the same bytes, which
	// is what lets one be compared or used as a cache key.
	for _, node := range sortedNodes(sv) {
		e.uvarint(e.intern(node))
		e.uvarint(uint64(sv[node]))
	}
	return e.finish(), nil
}

func sortedNodes(sv StateVector) []string {
	out := make([]string, 0, len(sv))
	for n := range sv {
		out = append(out, n)
	}
	// Insertion sort: state vectors have one entry per writer, so this is a
	// handful of elements and never worth an allocation.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// ── decoding ─────────────────────────────────────────────────────────────────

type decoder struct {
	buf    []byte
	nodes  []string
	prevWT int64
	err    error
}

func newDecoder(data []byte) (*decoder, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("crdt: empty payload")
	}
	if data[0] != updateFormatVersion {
		return nil, fmt.Errorf("crdt: unsupported wire format version %d", data[0])
	}
	d := &decoder{buf: data[1:]}

	count := d.uvarint()
	if d.err != nil {
		return nil, d.err
	}
	d.nodes = make([]string, 0, count)
	for i := uint64(0); i < count; i++ {
		d.nodes = append(d.nodes, d.str())
		if d.err != nil {
			return nil, d.err
		}
	}
	return d, nil
}

func (d *decoder) fail(format string, args ...any) {
	if d.err == nil {
		d.err = fmt.Errorf(format, args...)
	}
}

func (d *decoder) uvarint() uint64 {
	if d.err != nil {
		return 0
	}
	v, n := binary.Uvarint(d.buf)
	if n <= 0 {
		d.fail("crdt: truncated payload reading an unsigned value")
		return 0
	}
	d.buf = d.buf[n:]
	return v
}

func (d *decoder) varint() int64 {
	if d.err != nil {
		return 0
	}
	v, n := binary.Varint(d.buf)
	if n <= 0 {
		d.fail("crdt: truncated payload reading a signed value")
		return 0
	}
	d.buf = d.buf[n:]
	return v
}

func (d *decoder) byteVal() byte {
	if d.err != nil {
		return 0
	}
	if len(d.buf) == 0 {
		d.fail("crdt: truncated payload reading a flag byte")
		return 0
	}
	b := d.buf[0]
	d.buf = d.buf[1:]
	return b
}

func (d *decoder) str() string {
	n := d.uvarint()
	if d.err != nil {
		return ""
	}
	if uint64(len(d.buf)) < n {
		d.fail("crdt: truncated payload reading a %d-byte string", n)
		return ""
	}
	s := string(d.buf[:n])
	d.buf = d.buf[n:]
	return s
}

func (d *decoder) node() string {
	i := d.uvarint()
	if d.err != nil {
		return ""
	}
	if i >= uint64(len(d.nodes)) {
		d.fail("crdt: node index %d is outside a table of %d", i, len(d.nodes))
		return ""
	}
	return d.nodes[i]
}

func (d *decoder) clock() hlc.HLC {
	node := d.node()
	wall := d.prevWT + d.varint()
	d.prevWT = wall
	logical := d.uvarint()
	return hlc.HLC{WallTime: wall, Logical: int32(logical), NodeID: node}
}

// UnmarshalBinary decodes an update encoded by MarshalBinary.
func (u *Update) UnmarshalBinary(data []byte) error {
	d, err := newDecoder(data)
	if err != nil {
		return err
	}

	runs := d.uvarint()
	if d.err != nil {
		return d.err
	}
	// The count is checked against what is left rather than trusted: a
	// corrupted length would otherwise ask for an allocation of that size.
	if runs > uint64(len(d.buf)) {
		return fmt.Errorf("crdt: payload claims %d runs but holds %d bytes", runs, len(d.buf))
	}
	u.Runs = make(Text, 0, runs)
	for i := uint64(0); i < runs; i++ {
		flags := d.byteVal()
		var r TextRun
		r.ID = d.clock()
		r.Value = d.str()
		if flags&flagHasPrev != 0 {
			r.Prev = d.clock()
		}
		r.Deleted = flags&flagDeleted != 0
		if flags&flagHasN != 0 {
			r.N = int32(d.uvarint())
		}
		if d.err != nil {
			return d.err
		}
		u.Runs = append(u.Runs, r)
	}
	if len(u.Runs) == 0 {
		u.Runs = nil
	}

	deleted := d.uvarint()
	if d.err != nil {
		return d.err
	}
	if deleted > uint64(len(d.buf)) {
		return fmt.Errorf("crdt: payload claims %d deleted ranges but holds %d bytes", deleted, len(d.buf))
	}
	u.Deleted = make([]DeletedRange, 0, deleted)
	for i := uint64(0); i < deleted; i++ {
		var r DeletedRange
		r.ID = d.clock()
		r.N = int32(d.uvarint())
		if d.err != nil {
			return d.err
		}
		u.Deleted = append(u.Deleted, r)
	}
	if len(u.Deleted) == 0 {
		u.Deleted = nil
	}

	if len(d.buf) != 0 {
		return fmt.Errorf("crdt: %d bytes left over after decoding", len(d.buf))
	}
	return d.err
}

// UnmarshalBinary decodes a state vector encoded by MarshalBinary.
func (sv *StateVector) UnmarshalBinary(data []byte) error {
	d, err := newDecoder(data)
	if err != nil {
		return err
	}
	count := d.uvarint()
	if d.err != nil {
		return d.err
	}
	if count > uint64(len(d.buf)) {
		return fmt.Errorf("crdt: payload claims %d entries but holds %d bytes", count, len(d.buf))
	}
	out := make(StateVector, count)
	for i := uint64(0); i < count; i++ {
		node := d.node()
		n := d.uvarint()
		if d.err != nil {
			return d.err
		}
		out[node] = int32(n)
	}
	if len(d.buf) != 0 {
		return fmt.Errorf("crdt: %d bytes left over after decoding", len(d.buf))
	}
	*sv = out
	return nil
}
