/**
 * The sequence CRDT: runs of text, ordered by what they follow.
 *
 * This is a port of the Go implementation's algorithm, and it has to be an
 * exact one. Two replicas that order runs differently produce no error — they
 * simply hold different text, forever — so every rule here (the anchor
 * traversal, the tie-break by identifier, where a merge splits a run) matches
 * `crdt/text.go` deliberately, and the conformance corpus is what proves it.
 */
import * as hlc from './hlc.ts';
import type { DeletedRange, TextRun } from './types.ts';
/**
 * Characters are counted in **code points**, not UTF-16 units: an astral
 * character is one character, and a replica that counted it as two would
 * place every later edit in the wrong position.
 */
export declare function runes(s: string): string[];
export declare function runeCount(run: TextRun): number;
/** Splits a run at an offset, giving the tail the identifier it must have. */
export declare function splitRun(run: TextRun, offset: number): [TextRun, TextRun];
/** The text of a document, skipping tombstones. */
export declare function toString(text: TextRun[]): string;
/** The number of visible characters. */
export declare function length(text: TextRun[]): number;
/**
 * Puts runs in document order.
 *
 * Runs are grouped by the character they follow; within a group the newest
 * identifier comes first, which is the tie-break that decides between two
 * people typing at the same place. The walk then visits a run and, before
 * moving on, everything anchored to each of its characters in turn.
 */
export declare function ordered(text: TextRun[]): TextRun[];
/** Joins runs that are consecutive in both identifier and position. */
export declare function mergeAdjacent(text: TextRun[]): TextRun[];
export declare function normalize(text: TextRun[]): TextRun[];
/** Inserts text at a visible position, allocating identifiers from clock. */
export declare function insert(text: TextRun[], pos: number, value: string, clock: hlc.Clock): TextRun[];
/** Marks a visible range deleted, dividing runs at its edges. */
export declare function remove(text: TextRun[], pos: number, count: number): TextRun[];
/**
 * Combines two sets of runs.
 *
 * Runs from either side may cover overlapping stretches of the same origin,
 * so both are cut at every boundary either side knows about, and the pieces
 * are then keyed by identifier. A run anchored partway into another forces a
 * cut there too: without it the anchor sits inside a run, the run is emitted
 * whole, and everything anchored to a character in its middle lands after its
 * end instead — at which point the two replicas order the document
 * differently.
 */
export declare function mergeRuns(a: TextRun[], b: TextRun[]): TextRun[];
/** Applies deletion ranges, dividing a run when only part of it was deleted. */
export declare function markDeleted(text: TextRun[], ranges: DeletedRange[]): TextRun[];
