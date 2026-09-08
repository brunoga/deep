/**
 * JSON Pointer paths, as deep uses them. See docs/wire-patch.md.
 *
 * A path is a sequence of "/"-separated tokens. Within a token "~" is written
 * "~0" and "/" is written "~1"; unescaping does "~1" first so a literal "~1"
 * in a key survives the round trip.
 */
import type { KeySchema } from './types.ts';
export declare function escapeKey(key: string): string;
export declare function unescapeKey(token: string): string;
/** Splits a path into its unescaped tokens. The root path yields none. */
export declare function parsePath(path: string): string[];
/** Joins tokens into a path, escaping each. */
export declare function buildPath(tokens: string[]): string;
/** The path of the container holding `path`, or null for the root. */
export declare function parentPath(path: string): string | null;
/** The last token of a path, unescaped. */
export declare function lastToken(path: string): string;
export interface Resolved {
    found: boolean;
    value?: unknown;
}
/** Reads the value at `path`, reporting whether anything is there. */
export declare function resolve(root: unknown, path: string, keys?: KeySchema): Resolved;
/**
 * Writes `value` at `path`. `insert` distinguishes an `add` (which may extend
 * an array) from a `replace` (which may not).
 */
export declare function setAt(root: unknown, path: string, value: unknown, insert: boolean, keys?: KeySchema): void;
/** Deletes the value at `path`. */
export declare function removeAt(root: unknown, path: string, keys?: KeySchema): void;
/** True when `ancestor` covers `descendant` at a token boundary. */
export declare function encloses(ancestor: string, descendant: string): boolean;
