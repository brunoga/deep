import type { KeySchema, Patch } from './types.ts';
import { buildPath } from './path.ts';
export interface DiffOptions {
    /**
     * Which arrays are keyed, and by which field. See [KeySchema]. Without a
     * schema an array that differs is replaced whole, which is what Go's
     * reflection engine does for a slice with no `deep:"key"` tag.
     */
    keys?: KeySchema;
}
/**
 * Computes the operations that turn `a` into `b`.
 *
 * Objects are compared per key, in sorted order so that the same pair of
 * values always produces the same patch — a patch whose operation order
 * varies cannot be logged, cached, compared or signed.
 *
 * Arrays are replaced whole unless a key schema names their identity field,
 * in which case elements are matched by key: reordering then produces no
 * operations at all, and two writers editing different elements do not
 * collide.
 */
export declare function diff(a: unknown, b: unknown, opts?: DiffOptions): Patch;
export { buildPath };
