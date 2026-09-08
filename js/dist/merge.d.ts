import type { Patch } from './types.ts';
/**
 * Decides what a path holds when two patches both write it. `theirs` is the
 * value from the first patch, `mine` from the second.
 */
export type ConflictResolver = (path: string, theirs: unknown, mine: unknown) => unknown;
/**
 * Combines two patches computed against **the same starting state**.
 *
 * Operations are matched by path; where both write the same path the resolver
 * decides, and without one the second patch wins. Where one patch's path
 * encloses the other's — /user against /user/name — keeping both would
 * produce a patch that cannot apply, so the operation from `other` survives.
 *
 * This is for *concurrent* patches. Composing a *sequence* of patches with it
 * silently drops operations: an `add /a` followed by a later `replace /a/b`
 * collapses to the latter alone, which cannot apply to a state without /a. To
 * compose a sequence, apply them in order and diff the endpoints.
 */
export declare function merge(base: Patch, other: Patch, resolver?: ConflictResolver): Patch;
