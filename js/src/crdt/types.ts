import type { HLC } from './hlc.ts';

/**
 * A run of characters written consecutively by one node.
 *
 * Text is held as runs rather than characters because typing produces runs:
 * a word typed in one go is one identifier and one string, not eight of each.
 * A run is split only when something has to be said about part of it.
 */
export interface TextRun {
  /** The identifier of the run's first character. */
  id: HLC;
  /** The characters. */
  value: string;
  /**
   * The character this run follows, absent at the start of the document.
   * Concurrent insertions at the same place share an anchor, and the order
   * between them is decided by their identifiers.
   */
  prev?: HLC;
  /**
   * The number of **code points** in `value`, carried so that operations
   * depend on the number of runs rather than the length of the text. Zero
   * means "count it".
   */
  n: number;
  /** A tombstone: still present, no longer visible. */
  deleted?: boolean;
}

/** A stretch of removed characters, named by identifier rather than by text. */
export interface DeletedRange {
  id: HLC;
  n: number;
}

/** What one replica sends another: the text it lacks, and what has been deleted. */
export interface Update {
  runs: TextRun[];
  deleted: DeletedRange[];
}

/**
 * How much of each origin's output a replica holds, keyed by origin
 * (`node@walltime`). One entry per writer, not per character.
 */
export type StateVector = Map<string, number>;
