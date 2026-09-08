import type { ApplyResult, KeySchema, Operation, Outcome, Patch } from './types.ts';
import { encloses, removeAt, resolve, setAt } from './path.ts';
import { clone, equal } from './equal.ts';
import { evaluate } from './conditions.ts';

/** Thrown when a patch's guard does not hold. Nothing was applied. */
export class GuardNotMetError extends Error {
  constructor() {
    super('patch guard not met');
    this.name = 'GuardNotMetError';
  }
}

/** Thrown when an operation addresses a path outside the allowlist. */
export class PathNotAllowedError extends Error {
  constructor(path: string) {
    super(`path not allowed: ${path}`);
    this.name = 'PathNotAllowedError';
  }
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
export function applyPatch<T>(target: T, patch: Patch, opts: ApplyOptions = {}): ApplyResult<T> {
  const ops = patch.ops ?? [];
  const result: ApplyResult<T> = {
    value: target,
    outcomes: [],
    applied: 0,
    skipped: 0,
    failed: 0,
    errors: [],
  };

  if (opts.allowedPaths) {
    for (const op of ops) {
      for (const p of [op.p, op.f]) {
        if (p === undefined) continue;
        if (!opts.allowedPaths.some((prefix) => encloses(prefix, p))) {
          const err = new PathNotAllowedError(p);
          result.errors.push(err);
          return result;
        }
      }
    }
  }

  if (patch.cond && !evaluate(result.value, patch.cond, opts.keys)) {
    result.errors.push(new GuardNotMetError());
    return result;
  }

  ops.forEach((op, index) => {
    const outcome: Outcome = { index, path: op.p, kind: op.k, status: 'applied' };
    try {
      if (op.if && !evaluate(result.value, op.if, opts.keys)) {
        outcome.status = 'skipped';
        result.skipped++;
        result.outcomes.push(outcome);
        return;
      }
      if (op.un && evaluate(result.value, op.un, opts.keys)) {
        outcome.status = 'skipped';
        result.skipped++;
        result.outcomes.push(outcome);
        return;
      }
      result.value = applyOne(result.value, op, patch.strict === true, opts);
      result.applied++;
    } catch (err) {
      outcome.status = 'failed';
      outcome.error = err instanceof Error ? err : new Error(String(err));
      result.failed++;
      result.errors.push(outcome.error);
    }
    result.outcomes.push(outcome);
  });

  return result;
}

function isRoot(path: string): boolean {
  return path === '' || path === '/';
}

function applyOne<T>(root: T, op: Operation, strict: boolean, opts: ApplyOptions): T {
  if (strict && (op.k === 'replace' || op.k === 'remove') && op.o !== undefined) {
    const at = resolve(root, op.p, opts.keys);
    if (!at.found || !equal(at.value, op.o)) {
      throw new Error(`strict check failed at ${op.p}`);
    }
  }

  switch (op.k) {
    case 'add':
      if (isRoot(op.p)) return op.n as T;
      setAt(root, op.p, op.n, true, opts.keys);
      return root;

    case 'replace':
      if (isRoot(op.p)) return op.n as T;
      setAt(root, op.p, op.n, false, opts.keys);
      return root;

    case 'remove':
      if (isRoot(op.p)) throw new Error('cannot remove the root');
      removeAt(root, op.p, opts.keys);
      return root;

    case 'move': {
      if (op.f === undefined) throw new Error(`move at ${op.p}: missing source path`);
      const src = resolve(root, op.f, opts.keys);
      if (!src.found) throw new Error(`move source ${op.f} does not resolve`);
      removeAt(root, op.f, opts.keys);
      if (isRoot(op.p)) return src.value as T;
      setAt(root, op.p, src.value, true, opts.keys);
      return root;
    }

    case 'copy': {
      if (op.f === undefined) throw new Error(`copy at ${op.p}: missing source path`);
      const src = resolve(root, op.f, opts.keys);
      if (!src.found) throw new Error(`copy source ${op.f} does not resolve`);
      const copied = clone(src.value);
      if (isRoot(op.p)) return copied as T;
      setAt(root, op.p, copied, true, opts.keys);
      return root;
    }

    case 'alias': {
      // The point of an alias is that both paths reach the *same* value, so
      // unlike copy it deliberately does not clone.
      if (op.f === undefined) throw new Error(`alias at ${op.p}: missing source path`);
      const src = resolve(root, op.f, opts.keys);
      if (!src.found) throw new Error(`alias source ${op.f} does not resolve`);
      if (isRoot(op.p)) return src.value as T;
      setAt(root, op.p, src.value, true, opts.keys);
      return root;
    }

    case 'log':
      opts.log?.(op.p, op.n);
      return root;
  }
  throw new Error(`unknown operation kind ${op.k}`);
}
