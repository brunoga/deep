import type { ApplyResult, KeySchema, Patch } from './types.ts';
/** Thrown when a patch's guard does not hold. Nothing was applied. */
export declare class GuardNotMetError extends Error {
    constructor();
}
/** Thrown when an operation addresses a path outside the allowlist. */
export declare class PathNotAllowedError extends Error {
    constructor(path: string);
}
export interface ApplyOptions {
    /**
     * When set, an operation may only address these path prefixes. A patch
     * naming anything else is refused whole, before any of it runs — the same
     * blast-radius limit Go's WithAllowedPaths gives a server applying patches
     * it did not write.
     */
    allowedPaths?: string[];
    /** Receives `log` operations. Defaults to doing nothing. */
    log?: (path: string, message: unknown) => void;
    /**
     * Which arrays are keyed, and by which field. Needed to resolve paths that
     * address array elements by identity (/tasks/t3) rather than by position;
     * Go reads the same information from a `deep:"key"` struct tag.
     */
    keys?: KeySchema;
}
/**
 * Applies a patch.
 *
 * Operations run in order and each sees what the ones before it left. An
 * operation whose condition does not hold is *skipped*, which is not a
 * failure: conditional writes depend on telling "someone got there first"
 * apart from "this broke". A failing operation does not stop the ones after
 * it; every failure is collected.
 *
 * The value is patched in place where it can be, so applying a stream of
 * patches to a replica does not copy the world each time. Pass a copy —
 * `applyPatch(structuredClone(v), p)` — when the original must not move.
 */
export declare function applyPatch<T>(target: T, patch: Patch, opts?: ApplyOptions): ApplyResult<T>;
