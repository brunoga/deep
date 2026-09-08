import type { Condition, KeySchema } from './types.ts';
/**
 * Evaluates a condition against a value.
 *
 * A path that does not resolve is an error for every operator but `exists`,
 * which reports false — matching Go, where an unresolvable path means the
 * condition cannot be judged rather than that it failed.
 *
 * `keys` is needed for the same reason applying needs it: a condition may
 * read a keyed array element (/tasks/t3/done), and without the schema that
 * path does not resolve — turning a condition Go can answer into a failure
 * here, which is exactly the skip-versus-fail confusion conditional writes
 * exist to avoid.
 */
export declare function evaluate(root: unknown, c: Condition | undefined | null, keys?: KeySchema): boolean;
/** The `type` operator's vocabulary. */
export declare function checkType(v: unknown, typeName: string): boolean;
