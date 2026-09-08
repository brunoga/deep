# The CRDT wire format

`crdt.Update` and `crdt.StateVector` have a compact binary encoding, used by
the websocket transport and by anything that looks for
`encoding.BinaryMarshaler`. This document specifies it well enough to
implement in another language.

The format is versioned by its first byte. Everything here describes version
`1`, as shipped in deep v6.

## Why not JSON

An update is mostly identifiers, and identifiers are mostly repetition. Every
run carries a hybrid logical clock; every clock carries the node id that
issued it; every run after the first also carries the clock of the run it
follows. A node id written once appears as many times as there are runs, twice
over — and wall times are 19-digit nanosecond counts differing from their
neighbours by microseconds.

This format says the two things JSON cannot: node ids go into a table and are
referred to by index, and wall times are stored as deltas from the preceding
clock. Nearly all of the size difference comes from those two; the varints are
a rounding error beside them.

## Primitives

| Name | Encoding |
| :--- | :--- |
| `uvarint` | Unsigned LEB128, as Go's `binary.AppendUvarint`: 7 bits per byte, low group first, high bit set on every byte but the last. |
| `varint` | Signed zig-zag then `uvarint`, as Go's `binary.AppendVarint`: `n` encodes as `uint64(n) << 1` with the sign folded into bit 0 (`n < 0` → `^uint64(n) << 1 | 1`). |
| `string` | `uvarint` byte length, then that many bytes of UTF-8. |

All integers are little-endian by construction (LEB128 is byte-oriented).

## Layout

Every payload is:

```
byte      version = 1
uvarint   node table length N
N × (uvarint length, bytes)   the node ids, in first-use order
…body…
```

The node table precedes the body but is *built* by encoding the body, since
that is what discovers which node ids occur. An encoder buffers the body,
then writes version, table and body in that order.

A decoder must **bounds-check the table length against the remaining payload
before allocating**. A payload claiming a huge count is otherwise an
out-of-memory attack on any process that decodes network bytes; this was a
real, remotely triggerable defect in deep v6.2.0, fixed in v6.2.1.

## Clocks

A hybrid logical clock is `{WallTime int64, Logical int32, NodeID string}`.
It encodes as:

```
uvarint   index into the node table
varint    WallTime − (WallTime of the previously written clock, 0 initially)
uvarint   Logical
```

The delta is stateful: encoder and decoder each keep a running "previous wall
time", updated after every clock, and the running value is shared across the
whole payload — runs, anchors and deleted ranges alike, in the order they
appear on the wire. An implementation that resets it per section will produce
payloads Go cannot read.

Clocks order by `WallTime`, then `Logical`, then `NodeID` bytewise. That
ordering is part of convergence, not an implementation detail: two replicas
that break ties differently will not converge.

## Update

```
uvarint   number of runs
runs…
uvarint   number of deleted ranges
ranges…
```

Each **run**:

```
byte      flags
clock     ID
string    Value
clock     Prev        (only if flags & 0x1)
uvarint   N           (only if flags & 0x4)
```

| Flag | Bit | Meaning |
| :--- | :--- | :--- |
| `hasPrev` | `0x1` | The run carries an anchor — the clock of the run it follows. Absent for a run at the start of the document. |
| `deleted` | `0x2` | The run is a tombstone: its characters are not in the visible text. |
| `hasN` | `0x4` | The run carries an explicit rune count. |

Packing the three into one byte means the common run — no anchor, not deleted
— costs a single byte for all of them.

`N` is the number of **runes** (not bytes) in `Value`. Positions and
identifiers are counted in runes throughout. A run without an explicit `N`
(an older payload, or one built by hand) has its count derived from `Value`.
An implementation in a language whose strings are UTF-16 (JavaScript) must
count code points, not code units: astral characters are one rune each.

Each **deleted range**:

```
clock     ID     the first identifier in the range
uvarint   N      how many consecutive logical counters it covers
```

A range covers ids sharing `ID`'s node and wall time, with logical counters
`ID.Logical` through `ID.Logical + N - 1`.

## StateVector

A state vector maps node id → the counter that node has been seen up to.

```
uvarint   number of entries
entries…  (uvarint node table index, uvarint counter)
```

Entries are written **sorted by node id**, so that the same vector always
encodes to the same bytes — which is what lets one be compared or used as a
cache key. An implementation that emits them in map order is still readable
by Go, but loses that property and will fail byte-comparison conformance
tests.

Note that the node ids in a state vector are *origins* (`node@walltime`), not
bare node ids: a node allocates identifiers from a fresh space each time it
starts, and the space is the node together with the point it started counting.

## Convergence rules

The encoding is the easy half. An implementation that wants to *interoperate*
— not merely parse — must also match:

- **Run ordering** (`Text.getOrdered`): the anchor-and-displacement rules that
  decide where a concurrently-inserted run lands relative to its siblings.
- **Tie-breaking**: by clock, as above.
- **Merge**: applying an update the replica already holds must be a no-op, at
  any granularity, including partial overlap of a run.
- **Compaction**: which tombstones may be dropped, and the watermark that
  makes dropping them safe.

These are not specified prose-first here, because prose is the wrong artifact
for them: the authority is the conformance corpus, and any implementation
claiming compatibility should pass it.

## Conformance

`testdata/conformance/` holds cases generated from the Go implementation's own
property tests and fuzzers: operation interleavings with their expected final
text, state vector and encoded bytes. Running that corpus in another language
is what makes "compatible" a checkable claim rather than a hope, and it is a
merge gate in this repository's CI.

Divergence in a CRDT is silent and permanent — two replicas simply disagree
forever, with no error anywhere. Treat a corpus failure as a release blocker.
