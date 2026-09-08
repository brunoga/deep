import type { Patch } from './types.ts';
/**
 * Returns the patch that undoes this one.
 *
 * Operations are reversed in order as well as in kind, since they applied in
 * order. The result is exact only where the operations carry their `o`
 * values — which diff-produced patches always do.
 */
export declare function reverse(patch: Patch): Patch;
