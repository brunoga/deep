/**
 * The compact binary format for updates and state vectors, as specified in
 * docs/wire-crdt.md.
 *
 * Node ids are interned into a table and referred to by index, and wall times
 * are stored as differences from the preceding clock. Those two do nearly all
 * of the work; the varints are a rounding error beside them.
 */
import type { HLC } from './hlc.ts';
import type { DeletedRange, StateVector, TextRun, Update } from './types.ts';

const FORMAT_VERSION = 1;
const FLAG_HAS_PREV = 1;
const FLAG_DELETED = 2;
const FLAG_HAS_N = 4;

const encoder = new TextEncoder();
const decoder = new TextDecoder();

class Writer {
  bytes: number[] = [];
  private nodes = new Map<string, number>();
  private order: string[] = [];
  private prevWall = 0n;

  intern(node: string): number {
    const existing = this.nodes.get(node);
    if (existing !== undefined) return existing;
    const index = this.order.length;
    this.nodes.set(node, index);
    this.order.push(node);
    return index;
  }

  uvarint(value: bigint | number): void {
    let v = BigInt(value);
    if (v < 0n) throw new Error(`crdt: uvarint of negative value ${v}`);
    while (v >= 0x80n) {
      this.bytes.push(Number((v & 0x7fn) | 0x80n));
      v >>= 7n;
    }
    this.bytes.push(Number(v));
  }

  varint(value: bigint | number): void {
    // Zig-zag: the sign goes into bit 0, so a small negative stays small.
    const v = BigInt(value);
    this.uvarint(v < 0n ? (~v << 1n) | 1n : v << 1n);
  }

  byte(b: number): void {
    this.bytes.push(b);
  }

  str(s: string): void {
    const bytes = encoder.encode(s);
    this.uvarint(bytes.length);
    for (const b of bytes) this.bytes.push(b);
  }

  clock(h: HLC): void {
    this.uvarint(this.intern(h.n));
    this.varint(h.w - this.prevWall);
    this.prevWall = h.w;
    this.uvarint(h.l);
  }

  /**
   * Assembles version, node table and body. The table goes first on the wire
   * but is written last, because encoding the body is what discovers it.
   */
  finish(): Uint8Array {
    const table = new Writer();
    table.uvarint(this.order.length);
    for (const node of this.order) table.str(node);
    return Uint8Array.from([FORMAT_VERSION, ...table.bytes, ...this.bytes]);
  }
}

class Reader {
  private pos = 0;
  private nodes: string[] = [];
  private prevWall = 0n;
  private buf: Uint8Array;

  constructor(buf: Uint8Array) {
    this.buf = buf;
    if (buf.length === 0) throw new Error('crdt: empty payload');
    if (buf[0] !== FORMAT_VERSION) {
      throw new Error(`crdt: unsupported wire format version ${buf[0]}`);
    }
    this.pos = 1;
    const count = this.uvarint();
    // Bounds-checked before allocating. A payload claiming a huge table is
    // otherwise an out-of-memory attack on anything that decodes network
    // bytes — which was a real, remotely triggerable defect in the Go
    // decoder, fixed in v6.2.1.
    if (count > BigInt(this.buf.length)) {
      throw new Error(`crdt: node table of ${count} entries exceeds the payload`);
    }
    for (let i = 0n; i < count; i++) this.nodes.push(this.str());
  }

  uvarint(): bigint {
    let result = 0n;
    let shift = 0n;
    for (;;) {
      if (this.pos >= this.buf.length) throw new Error('crdt: truncated varint');
      const b = this.buf[this.pos++]!;
      result |= BigInt(b & 0x7f) << shift;
      if ((b & 0x80) === 0) return result;
      shift += 7n;
      if (shift > 70n) throw new Error('crdt: varint overflow');
    }
  }

  varint(): bigint {
    const v = this.uvarint();
    return (v & 1n) === 1n ? ~(v >> 1n) : v >> 1n;
  }

  byte(): number {
    if (this.pos >= this.buf.length) throw new Error('crdt: truncated payload');
    return this.buf[this.pos++]!;
  }

  str(): string {
    const length = Number(this.uvarint());
    if (this.pos + length > this.buf.length) throw new Error('crdt: truncated string');
    const out = decoder.decode(this.buf.subarray(this.pos, this.pos + length));
    this.pos += length;
    return out;
  }

  node(): string {
    const index = Number(this.uvarint());
    const name = this.nodes[index];
    if (name === undefined) throw new Error(`crdt: node index ${index} out of range`);
    return name;
  }

  clock(): HLC {
    const n = this.node();
    const w = this.prevWall + this.varint();
    this.prevWall = w;
    const l = Number(this.uvarint());
    return { w, l, n };
  }
}

/** Encodes an update. */
export function encodeUpdate(u: Update): Uint8Array {
  const w = new Writer();
  w.uvarint(u.runs.length);
  for (const run of u.runs) {
    let flags = 0;
    if (run.prev !== undefined) flags |= FLAG_HAS_PREV;
    if (run.deleted) flags |= FLAG_DELETED;
    if (run.n !== 0) flags |= FLAG_HAS_N;
    w.byte(flags);
    w.clock(run.id);
    w.str(run.value);
    if (run.prev !== undefined) w.clock(run.prev);
    if (run.n !== 0) w.uvarint(run.n);
  }
  w.uvarint(u.deleted.length);
  for (const d of u.deleted) {
    w.clock(d.id);
    w.uvarint(d.n);
  }
  return w.finish();
}

/** Decodes an update. */
export function decodeUpdate(data: Uint8Array): Update {
  const r = new Reader(data);
  const runCount = Number(r.uvarint());
  const runs: TextRun[] = [];
  for (let i = 0; i < runCount; i++) {
    const flags = r.byte();
    const id = r.clock();
    const value = r.str();
    const run: TextRun = { id, value, n: 0 };
    if (flags & FLAG_HAS_PREV) run.prev = r.clock();
    if (flags & FLAG_HAS_N) run.n = Number(r.uvarint());
    if (flags & FLAG_DELETED) run.deleted = true;
    runs.push(run);
  }
  const deletedCount = Number(r.uvarint());
  const deleted: DeletedRange[] = [];
  for (let i = 0; i < deletedCount; i++) {
    deleted.push({ id: r.clock(), n: Number(r.uvarint()) });
  }
  return { runs, deleted };
}

/**
 * Encodes a state vector. Entries are sorted by origin, so the same vector
 * always encodes to the same bytes — which is what lets one be compared or
 * used as a cache key.
 */
export function encodeStateVector(sv: StateVector): Uint8Array {
  const w = new Writer();
  const origins = [...sv.keys()].sort();
  w.uvarint(origins.length);
  for (const o of origins) {
    w.uvarint(w.intern(o));
    w.uvarint(sv.get(o)!);
  }
  return w.finish();
}

/** Decodes a state vector. */
export function decodeStateVector(data: Uint8Array): StateVector {
  const r = new Reader(data);
  const count = Number(r.uvarint());
  const sv: StateVector = new Map();
  for (let i = 0; i < count; i++) {
    const node = r.node();
    sv.set(node, Number(r.uvarint()));
  }
  return sv;
}

/** Hex helpers, for fixtures and debugging. */
export function toHex(data: Uint8Array): string {
  return [...data].map((b) => b.toString(16).padStart(2, '0')).join('');
}

export function fromHex(hex: string): Uint8Array {
  const out = new Uint8Array(hex.length / 2);
  for (let i = 0; i < out.length; i++) out[i] = parseInt(hex.slice(i * 2, i * 2 + 2), 16);
  return out;
}
