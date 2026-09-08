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
import * as text from './text.ts';
import type { DeletedRange, StateVector, TextRun, Update } from './types.ts';

/** How many of the run's leading characters a state vector already accounts for. */
function covers(sv: StateVector, run: TextRun): number {
  const seen = sv.get(hlc.origin(run.id));
  if (seen === undefined) return 0;
  const covered = seen - run.id.l;
  if (covered < 0) return 0;
  const n = text.runeCount(run);
  return covered > n ? n : covered;
}

export class Document {
  private runs: TextRun[] = [];
  private sv: StateVector = new Map();
  readonly clock: hlc.Clock;

  constructor(nodeID: string, wallTime?: bigint) {
    this.clock = new hlc.Clock(nodeID, wallTime);
  }

  /** The visible text. */
  toString(): string {
    return text.toString(this.runs);
  }

  /** The number of visible characters, counted in code points. */
  get length(): number {
    return text.length(this.runs);
  }

  /** The runs, for inspection. */
  get text(): readonly TextRun[] {
    return this.runs;
  }

  /** Inserts at a visible position. */
  insert(pos: number, value: string): void {
    this.runs = text.insert(this.runs, pos, value, this.clock);
    this.note();
  }

  /** Deletes a visible range. */
  delete(pos: number, count: number): void {
    this.runs = text.remove(this.runs, pos, count);
    this.note();
  }

  /**
   * What this replica holds, as one bound per origin. It is what a peer needs
   * to work out what to send back, and it is small: one entry per writer, not
   * per character.
   */
  stateVector(): StateVector {
    return new Map(this.sv);
  }

  /**
   * What a replica holding `sv` is missing. A run the peer holds entirely is
   * left out; one it holds part of is trimmed to the part it does not — which
   * is what makes syncing cost the size of the change rather than the size of
   * the document.
   */
  since(sv: StateVector): Update {
    const runs: TextRun[] = [];
    const deleted: DeletedRange[] = [];
    for (const run of text.ordered(this.runs)) {
      const covered = covers(sv, run);
      if (covered === 0) runs.push(run);
      else if (covered < text.runeCount(run)) runs.push(text.splitRun(run, covered)[1]);
      if (run.deleted) deleted.push({ id: run.id, n: text.runeCount(run) });
    }
    return { runs, deleted };
  }

  /** Folds in an update from a peer. Applying one twice changes nothing. */
  apply(u: Update): void {
    if (u.runs.length === 0 && u.deleted.length === 0) return;
    let runs = this.runs;
    if (u.runs.length > 0) runs = text.mergeRuns(runs, u.runs);
    if (u.deleted.length > 0) runs = text.markDeleted(runs, u.deleted);
    this.runs = runs;
    this.rebuild();
  }

  /** Replaces the contents wholesale — for adopting a snapshot. */
  reset(runs: TextRun[]): void {
    this.runs = text.normalize(runs);
    this.rebuild();
  }

  /** Records the origins of everything currently held. */
  private rebuild(): void {
    this.sv = new Map();
    for (const run of this.runs) this.noteRun(run);
  }

  private note(): void {
    for (const run of this.runs) this.noteRun(run);
  }

  private noteRun(run: TextRun): void {
    const origin = hlc.origin(run.id);
    const end = run.id.l + text.runeCount(run);
    const seen = this.sv.get(origin);
    if (seen === undefined || end > seen) this.sv.set(origin, end);
  }
}
