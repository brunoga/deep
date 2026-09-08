/**
 * Hybrid logical clocks, as deep's CRDT uses them.
 *
 * Wall times are Unix **nanoseconds**, which run past 2^53 and so cannot be
 * held in a JavaScript number without losing the low digits — two edits a
 * microsecond apart would land on the same identifier. They are `bigint`
 * here for that reason, all the way through the codec.
 */
export interface HLC {
    /** Wall time, Unix nanoseconds. */
    w: bigint;
    /** Logical counter, breaking ties within a wall time. */
    l: number;
    /** The node that issued it. */
    n: string;
}
export declare const ZERO: HLC;
export declare function isZero(h: HLC | undefined): boolean;
/** Orders two identifiers: wall time, then counter, then node id. */
export declare function compare(a: HLC, b: HLC): number;
export declare function after(a: HLC, b: HLC): boolean;
export declare function equal(a: HLC, b: HLC): boolean;
/** A map key for an identifier, since objects cannot be keys by value. */
export declare function key(h: HLC): string;
/** The identifier `offset` characters after h. */
export declare function plus(h: HLC, offset: number): HLC;
/**
 * The space an identifier was allocated from: a node together with the point
 * it started counting. A node that restarts allocates from a fresh space, so
 * identifiers issued before a restart can never be issued again after one.
 */
export declare function origin(h: HLC): string;
/**
 * Clock allocates identifiers for one node.
 *
 * Sequence identifiers come from their own range, anchored once at the
 * clock's wall time — the counter only ever climbs within that range, which
 * is what lets a state vector describe "everything this node wrote" as a
 * single number.
 */
export declare class Clock {
    readonly nodeID: string;
    private seq;
    constructor(nodeID: string, wallTime?: bigint);
    /** Reserves n consecutive identifiers and returns the first. */
    reserveSequence(n: number): HLC;
}
