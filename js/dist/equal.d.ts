/** Structural equality and the comparisons conditions need. */
/** Deep structural equality over JSON-shaped values. */
export declare function equal(a: unknown, b: unknown): boolean;
/** A deep copy, for values that are JSON-shaped. */
export declare function clone<T>(v: T): T;
/**
 * Ordered comparison, as conditions define it: two numbers compare
 * numerically, two strings lexicographically, anything else is an error.
 */
export declare function compareOrdered(a: unknown, b: unknown, op: string): boolean;
