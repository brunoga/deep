/**
 * A collaborative text document: the replica a client holds, and the two
 * operations that keep it in step with its peers.
 *
 * `since(theirVector)` says what a peer is missing, and `apply(update)` folds
 * in what they sent. Nothing else is needed to converge — no central order,
 * no acknowledgements, and no cost proportional to the document when only a
 * word changed.
 */
import * as hlc from './hlc.ts';
import type { StateVector, TextRun, Update } from './types.ts';
export declare class Document {
    private runs;
    private sv;
    readonly clock: hlc.Clock;
    constructor(nodeID: string, wallTime?: bigint);
    /** The visible text. */
    toString(): string;
    /** The number of visible characters, counted in code points. */
    get length(): number;
    /** The runs, for inspection. */
    get text(): readonly TextRun[];
    /** Inserts at a visible position. */
    insert(pos: number, value: string): void;
    /** Deletes a visible range. */
    delete(pos: number, count: number): void;
    /**
     * What this replica holds, as one bound per origin. It is what a peer needs
     * to work out what to send back, and it is small: one entry per writer, not
     * per character.
     */
    stateVector(): StateVector;
    /**
     * What a replica holding `sv` is missing. A run the peer holds entirely is
     * left out; one it holds part of is trimmed to the part it does not — which
     * is what makes syncing cost the size of the change rather than the size of
     * the document.
     */
    since(sv: StateVector): Update;
    /** Folds in an update from a peer. Applying one twice changes nothing. */
    apply(u: Update): void;
    /** Replaces the contents wholesale — for adopting a snapshot. */
    reset(runs: TextRun[]): void;
    /** Records the origins of everything currently held. */
    private rebuild;
    private note;
    private noteRun;
}
