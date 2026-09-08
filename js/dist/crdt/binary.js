const FORMAT_VERSION = 1;
const FLAG_HAS_PREV = 1;
const FLAG_DELETED = 2;
const FLAG_HAS_N = 4;
const encoder = new TextEncoder();
const decoder = new TextDecoder();
class Writer {
    bytes = [];
    nodes = new Map();
    order = [];
    prevWall = 0n;
    intern(node) {
        const existing = this.nodes.get(node);
        if (existing !== undefined)
            return existing;
        const index = this.order.length;
        this.nodes.set(node, index);
        this.order.push(node);
        return index;
    }
    uvarint(value) {
        let v = BigInt(value);
        if (v < 0n)
            throw new Error(`crdt: uvarint of negative value ${v}`);
        while (v >= 0x80n) {
            this.bytes.push(Number((v & 0x7fn) | 0x80n));
            v >>= 7n;
        }
        this.bytes.push(Number(v));
    }
    varint(value) {
        // Zig-zag: the sign goes into bit 0, so a small negative stays small.
        const v = BigInt(value);
        this.uvarint(v < 0n ? (~v << 1n) | 1n : v << 1n);
    }
    byte(b) {
        this.bytes.push(b);
    }
    str(s) {
        const bytes = encoder.encode(s);
        this.uvarint(bytes.length);
        for (const b of bytes)
            this.bytes.push(b);
    }
    clock(h) {
        this.uvarint(this.intern(h.n));
        this.varint(h.w - this.prevWall);
        this.prevWall = h.w;
        this.uvarint(h.l);
    }
    /**
     * Assembles version, node table and body. The table goes first on the wire
     * but is written last, because encoding the body is what discovers it.
     */
    finish() {
        const table = new Writer();
        table.uvarint(this.order.length);
        for (const node of this.order)
            table.str(node);
        return Uint8Array.from([FORMAT_VERSION, ...table.bytes, ...this.bytes]);
    }
}
class Reader {
    pos = 0;
    nodes = [];
    prevWall = 0n;
    buf;
    constructor(buf) {
        this.buf = buf;
        if (buf.length === 0)
            throw new Error('crdt: empty payload');
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
        for (let i = 0n; i < count; i++)
            this.nodes.push(this.str());
    }
    uvarint() {
        let result = 0n;
        let shift = 0n;
        for (;;) {
            if (this.pos >= this.buf.length)
                throw new Error('crdt: truncated varint');
            const b = this.buf[this.pos++];
            result |= BigInt(b & 0x7f) << shift;
            if ((b & 0x80) === 0)
                return result;
            shift += 7n;
            if (shift > 70n)
                throw new Error('crdt: varint overflow');
        }
    }
    varint() {
        const v = this.uvarint();
        return (v & 1n) === 1n ? ~(v >> 1n) : v >> 1n;
    }
    byte() {
        if (this.pos >= this.buf.length)
            throw new Error('crdt: truncated payload');
        return this.buf[this.pos++];
    }
    str() {
        const length = Number(this.uvarint());
        if (this.pos + length > this.buf.length)
            throw new Error('crdt: truncated string');
        const out = decoder.decode(this.buf.subarray(this.pos, this.pos + length));
        this.pos += length;
        return out;
    }
    node() {
        const index = Number(this.uvarint());
        const name = this.nodes[index];
        if (name === undefined)
            throw new Error(`crdt: node index ${index} out of range`);
        return name;
    }
    clock() {
        const n = this.node();
        const w = this.prevWall + this.varint();
        this.prevWall = w;
        const l = Number(this.uvarint());
        return { w, l, n };
    }
    /**
     * Checks a claimed count against the bytes that remain before anything is
     * allocated for it. Every entry costs at least one byte, so a count larger
     * than what is left cannot be honest — and honouring it would let a short
     * payload ask for an enormous allocation.
     */
    count(what) {
        const n = this.uvarint();
        if (n > BigInt(this.buf.length - this.pos)) {
            throw new Error(`crdt: payload claims ${n} ${what} but holds ${this.buf.length - this.pos} bytes`);
        }
        return Number(n);
    }
    /** Rejects anything left over, as Go does: a frame is exactly one value. */
    end() {
        if (this.pos !== this.buf.length) {
            throw new Error(`crdt: ${this.buf.length - this.pos} bytes left over after decoding`);
        }
    }
}
/** Encodes an update. */
export function encodeUpdate(u) {
    const w = new Writer();
    w.uvarint(u.runs.length);
    for (const run of u.runs) {
        let flags = 0;
        if (run.prev !== undefined)
            flags |= FLAG_HAS_PREV;
        if (run.deleted)
            flags |= FLAG_DELETED;
        if (run.n !== 0)
            flags |= FLAG_HAS_N;
        w.byte(flags);
        w.clock(run.id);
        w.str(run.value);
        if (run.prev !== undefined)
            w.clock(run.prev);
        if (run.n !== 0)
            w.uvarint(run.n);
    }
    w.uvarint(u.deleted.length);
    for (const d of u.deleted) {
        w.clock(d.id);
        w.uvarint(d.n);
    }
    return w.finish();
}
/** Decodes an update. */
export function decodeUpdate(data) {
    const r = new Reader(data);
    const runCount = r.count('runs');
    const runs = [];
    for (let i = 0; i < runCount; i++) {
        const flags = r.byte();
        const id = r.clock();
        const value = r.str();
        const run = { id, value, n: 0 };
        if (flags & FLAG_HAS_PREV)
            run.prev = r.clock();
        if (flags & FLAG_HAS_N)
            run.n = Number(r.uvarint());
        if (flags & FLAG_DELETED)
            run.deleted = true;
        runs.push(run);
    }
    const deletedCount = r.count('deleted ranges');
    const deleted = [];
    for (let i = 0; i < deletedCount; i++) {
        deleted.push({ id: r.clock(), n: Number(r.uvarint()) });
    }
    r.end();
    return { runs, deleted };
}
/**
 * Encodes a state vector. Entries are sorted by origin, so the same vector
 * always encodes to the same bytes — which is what lets one be compared or
 * used as a cache key.
 */
export function encodeStateVector(sv) {
    const w = new Writer();
    // Sorted by UTF-8 bytes, which is what Go compares — ordinary string
    // comparison here sorts by UTF-16 code unit, and the two disagree for any
    // node id above the basic plane, which would make the same vector encode
    // differently on the two sides.
    const origins = [...sv.keys()].sort(compareUTF8);
    w.uvarint(origins.length);
    for (const o of origins) {
        w.uvarint(w.intern(o));
        w.uvarint(sv.get(o));
    }
    return w.finish();
}
/** Decodes a state vector. */
export function decodeStateVector(data) {
    const r = new Reader(data);
    const count = r.count('entries');
    const sv = new Map();
    for (let i = 0; i < count; i++) {
        const node = r.node();
        sv.set(node, Number(r.uvarint()));
    }
    r.end();
    return sv;
}
/** Orders strings by their UTF-8 bytes, as Go's string comparison does. */
function compareUTF8(a, b) {
    const x = encoder.encode(a);
    const y = encoder.encode(b);
    const n = Math.min(x.length, y.length);
    for (let i = 0; i < n; i++) {
        if (x[i] !== y[i])
            return x[i] - y[i];
    }
    return x.length - y.length;
}
/** Hex helpers, for fixtures and debugging. */
export function toHex(data) {
    return [...data].map((b) => b.toString(16).padStart(2, '0')).join('');
}
export function fromHex(hex) {
    const out = new Uint8Array(hex.length / 2);
    for (let i = 0; i < out.length; i++)
        out[i] = parseInt(hex.slice(i * 2, i * 2 + 2), 16);
    return out;
}
