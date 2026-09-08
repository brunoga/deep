/**
 * Presence: who else is here, and where their cursor is.
 *
 * Presence is not part of the document — it is not edited, not merged and not
 * kept. It is a last-writer-wins register per peer with an expiry, so a peer
 * that stops announcing simply stops being drawn rather than lingering
 * forever as a ghost cursor.
 */
/** One peer's announcement, as it travels. */
export interface AwarenessEntry<T> {
    /** The peer. */
    n: string;
    /** Their counter; a higher one supersedes what we hold. */
    c: number;
    /** Their state, absent when they are saying goodbye. */
    s?: T;
}
export interface AwarenessUpdate<T> {
    e?: AwarenessEntry<T>[];
}
export interface AwarenessOptions {
    /** How long a peer stays visible after its last announcement. */
    ttlMs?: number;
    /** The clock, injectable for tests. */
    now?: () => number;
}
export declare class Awareness<T> {
    readonly node: string;
    private held;
    private ttl;
    private now;
    private listeners;
    constructor(node: string, opts?: AwarenessOptions);
    /** Records this client's own state and returns the announcement to send. */
    setLocal(state: T): AwarenessUpdate<T>;
    /**
     * The goodbye: an entry with no state, so peers drop this client at once
     * rather than waiting out its expiry.
     */
    leave(): AwarenessUpdate<T>;
    /** Folds in what a peer announced. Older announcements are ignored. */
    apply(update: AwarenessUpdate<T>): void;
    /** Everyone currently present, this client included. */
    states(): Map<string, T>;
    /** Drops peers that have gone quiet. */
    expire(): void;
    /** Runs fn whenever the presence view changes. */
    onChange(fn: () => void): () => void;
    private notify;
}
