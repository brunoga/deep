export const ZERO = { w: 0n, l: 0, n: '' };
export function isZero(h) {
    return h === undefined || (h.w === 0n && h.l === 0 && h.n === '');
}
/** Orders two identifiers: wall time, then counter, then node id. */
export function compare(a, b) {
    if (a.w !== b.w)
        return a.w < b.w ? -1 : 1;
    if (a.l !== b.l)
        return a.l < b.l ? -1 : 1;
    if (a.n !== b.n)
        return a.n < b.n ? -1 : 1;
    return 0;
}
export function after(a, b) {
    return compare(a, b) > 0;
}
export function equal(a, b) {
    return a.w === b.w && a.l === b.l && a.n === b.n;
}
/** A map key for an identifier, since objects cannot be keys by value. */
export function key(h) {
    return `${h.n} ${h.w} ${h.l}`;
}
/** The identifier `offset` characters after h. */
export function plus(h, offset) {
    return { w: h.w, l: h.l + offset, n: h.n };
}
/**
 * The space an identifier was allocated from: a node together with the point
 * it started counting. A node that restarts allocates from a fresh space, so
 * identifiers issued before a restart can never be issued again after one.
 */
export function origin(h) {
    return `${h.n}@${h.w}`;
}
/**
 * Clock allocates identifiers for one node.
 *
 * Sequence identifiers come from their own range, anchored once at the
 * clock's wall time — the counter only ever climbs within that range, which
 * is what lets a state vector describe "everything this node wrote" as a
 * single number.
 */
export class Clock {
    nodeID;
    seq;
    constructor(nodeID, wallTime) {
        this.nodeID = nodeID;
        const w = wallTime ?? BigInt(Date.now()) * 1000000n;
        this.seq = { w, l: 0, n: nodeID };
    }
    /** Reserves n consecutive identifiers and returns the first. */
    reserveSequence(n) {
        const start = { ...this.seq };
        this.seq = { ...this.seq, l: this.seq.l + n };
        return start;
    }
}
