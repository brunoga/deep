/** The kinds of operation a patch can carry. See docs/wire-patch.md. */
export type OpKind = 'add' | 'remove' | 'replace' | 'move' | 'copy' | 'alias' | 'log';

/** A condition, as carried inside a patch or an operation. */
export interface Condition {
  /** Path to read. Absent for the logical operators. */
  p?: string;
  /** Operator: "==", "!=", ">", ">=", "<", "<=", exists, in, matches, type, and, or, not. */
  o: string;
  /** Comparison value. Absent for `exists` and the logical operators. */
  v?: unknown;
  /** Sub-conditions, for `and`, `or` and `not`. */
  apply?: Condition[];
}

/** One change. Which fields are meaningful depends on `k`. */
export interface Operation {
  k: OpKind;
  p: string;
  /** Source path, for `move` and `copy`. */
  f?: string;
  /** The value before, where known — what makes the operation reversible. */
  o?: unknown;
  /** The value after. */
  n?: unknown;
  /** Apply only if this holds. */
  if?: Condition;
  /** Apply only if this does not hold. */
  un?: Condition;
}

/** A patch: an ordered list of operations, optionally guarded. */
export interface Patch {
  /** A guard: when it does not hold, the whole patch is a no-op. */
  cond?: Condition;
  ops: Operation[] | null;
  /** Verify each replace/remove against its recorded `o` before writing. */
  strict?: boolean;
}

/** What became of one operation. */
export type OutcomeStatus = 'applied' | 'skipped' | 'failed';

export interface Outcome {
  index: number;
  path: string;
  kind: OpKind;
  status: OutcomeStatus;
  error?: Error;
}

/**
 * The result of applying a patch.
 *
 * `value` is the patched value: usually the same object, mutated in place,
 * but a new value when the patch replaced the root.
 */
export interface ApplyResult<T> {
  value: T;
  outcomes: Outcome[];
  applied: number;
  skipped: number;
  failed: number;
  /** The failures, if any. An all-skipped patch is not a failure. */
  errors: Error[];
}

/**
 * Names the identity field of the elements of an array, per path.
 *
 * Go models mark this with a `deep:"key"` struct tag, which is invisible in
 * JSON — so a JavaScript producer has to be told. Keys are the array's own
 * path: `{"/tasks": "id"}` makes a diff of `/tasks` address elements as
 * `/tasks/<id>` rather than `/tasks/<index>`, matching what Go emits for the
 * same model. Applying does not need the schema; only diffing does.
 */
export type KeySchema = Record<string, string>;
