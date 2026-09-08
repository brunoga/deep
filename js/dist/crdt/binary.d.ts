import type { StateVector, Update } from './types.ts';
/** Encodes an update. */
export declare function encodeUpdate(u: Update): Uint8Array;
/** Decodes an update. */
export declare function decodeUpdate(data: Uint8Array): Update;
/**
 * Encodes a state vector. Entries are sorted by origin, so the same vector
 * always encodes to the same bytes — which is what lets one be compared or
 * used as a cache key.
 */
export declare function encodeStateVector(sv: StateVector): Uint8Array;
/** Decodes a state vector. */
export declare function decodeStateVector(data: Uint8Array): StateVector;
/** Hex helpers, for fixtures and debugging. */
export declare function toHex(data: Uint8Array): string;
export declare function fromHex(hex: string): Uint8Array;
