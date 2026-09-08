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

interface Held<T> {
  clock: number;
  state?: T;
  seen: number;
  gone: boolean;
}

export interface AwarenessOptions {
  /** How long a peer stays visible after its last announcement. */
  ttlMs?: number;
  /** The clock, injectable for tests. */
  now?: () => number;
}

export class Awareness<T> {
  readonly node: string;
  private held = new Map<string, Held<T>>();
  private ttl: number;
  private now: () => number;
  private listeners = new Set<() => void>();

  constructor(node: string, opts: AwarenessOptions = {}) {
    this.node = node;
    this.ttl = opts.ttlMs ?? 30_000;
    this.now = opts.now ?? (() => Date.now());
  }

  /** Records this client's own state and returns the announcement to send. */
  setLocal(state: T): AwarenessUpdate<T> {
    const prev = this.held.get(this.node);
    const clock = (prev?.clock ?? 0) + 1;
    this.held.set(this.node, { clock, state, seen: this.now(), gone: false });
    this.notify();
    return { e: [{ n: this.node, c: clock, s: state }] };
  }

  /**
   * The goodbye: an entry with no state, so peers drop this client at once
   * rather than waiting out its expiry.
   */
  leave(): AwarenessUpdate<T> {
    const prev = this.held.get(this.node);
    const clock = (prev?.clock ?? 0) + 1;
    this.held.set(this.node, { clock, seen: this.now(), gone: true });
    this.notify();
    return { e: [{ n: this.node, c: clock }] };
  }

  /** Folds in what a peer announced. Older announcements are ignored. */
  apply(update: AwarenessUpdate<T>): void {
    let changed = false;
    for (const entry of update.e ?? []) {
      const prev = this.held.get(entry.n);
      if (prev && prev.clock >= entry.c) continue;
      this.held.set(entry.n, {
        clock: entry.c,
        state: entry.s,
        seen: this.now(),
        gone: entry.s === undefined,
      });
      changed = true;
    }
    if (changed) this.notify();
  }

  /** Everyone currently present, this client included. */
  states(): Map<string, T> {
    this.expire();
    const out = new Map<string, T>();
    for (const [node, held] of this.held) {
      if (!held.gone && held.state !== undefined) out.set(node, held.state);
    }
    return out;
  }

  /** Drops peers that have gone quiet. */
  expire(): void {
    const cutoff = this.now() - this.ttl;
    let changed = false;
    for (const [node, held] of this.held) {
      if (node === this.node) continue;
      if (held.seen < cutoff) {
        this.held.delete(node);
        changed = true;
      }
    }
    if (changed) this.notify();
  }

  /** Runs fn whenever the presence view changes. */
  onChange(fn: () => void): () => void {
    this.listeners.add(fn);
    return () => this.listeners.delete(fn);
  }

  private notify(): void {
    for (const fn of this.listeners) fn();
  }
}
