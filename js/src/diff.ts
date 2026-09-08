import type { KeySchema, Operation, Patch } from './types.ts';
import { buildPath, escapeKey } from './path.ts';
import { clone, equal } from './equal.ts';

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
export function diff(a: unknown, b: unknown, opts: DiffOptions = {}): Patch {
  const ops: Operation[] = [];
  diffValue('', a, b, opts, ops);
  return { ops };
}

function diffValue(path: string, a: unknown, b: unknown, opts: DiffOptions, ops: Operation[]): void {
  if (equal(a, b)) return;

  const bothObjects =
    a !== null && b !== null &&
    typeof a === 'object' && typeof b === 'object' &&
    !Array.isArray(a) && !Array.isArray(b);

  if (bothObjects) {
    diffObject(path, a as Record<string, unknown>, b as Record<string, unknown>, opts, ops);
    return;
  }

  if (Array.isArray(a) && Array.isArray(b)) {
    const keyField = opts.keys?.[path === '' ? '/' : path];
    if (keyField !== undefined) {
      diffKeyed(path, a, b, keyField, opts, ops);
      return;
    }
  }

  ops.push({ k: 'replace', p: path === '' ? '' : path, o: clone(a), n: clone(b) });
}

function diffObject(
  path: string,
  a: Record<string, unknown>,
  b: Record<string, unknown>,
  opts: DiffOptions,
  ops: Operation[],
): void {
  const keys = [...new Set([...Object.keys(a), ...Object.keys(b)])].sort();
  for (const key of keys) {
    const child = path + '/' + escapeKey(key);
    const inA = key in a;
    const inB = key in b;
    if (inA && !inB) {
      ops.push({ k: 'remove', p: child, o: clone(a[key]) });
      continue;
    }
    if (!inA && inB) {
      ops.push({ k: 'add', p: child, n: clone(b[key]) });
      continue;
    }
    diffValue(child, a[key], b[key], opts, ops);
  }
}

/**
 * Diffs an array whose elements have an identity: elements are matched by
 * key rather than by position, so a reorder is not a change and an element's
 * own path stays stable as the array moves around it.
 */
function diffKeyed(
  path: string,
  a: unknown[],
  b: unknown[],
  keyField: string,
  opts: DiffOptions,
  ops: Operation[],
): void {
  const keyOf = (el: unknown): string | undefined => {
    if (el === null || typeof el !== 'object') return undefined;
    const raw = (el as Record<string, unknown>)[keyField];
    return raw === undefined ? undefined : String(raw);
  };

  const aByKey = new Map<string, unknown>();
  for (const el of a) {
    const k = keyOf(el);
    if (k === undefined) {
      // An element without the key cannot be addressed; fall back to
      // replacing the array rather than emitting paths that cannot apply.
      ops.push({ k: 'replace', p: path, o: clone(a), n: clone(b) });
      return;
    }
    aByKey.set(k, el);
  }
  const bByKey = new Map<string, unknown>();
  for (const el of b) {
    const k = keyOf(el);
    if (k === undefined) {
      ops.push({ k: 'replace', p: path, o: clone(a), n: clone(b) });
      return;
    }
    bByKey.set(k, el);
  }

  for (const key of [...aByKey.keys()].sort()) {
    if (!bByKey.has(key)) {
      ops.push({ k: 'remove', p: path + '/' + escapeKey(key), o: clone(aByKey.get(key)) });
    }
  }
  for (const key of [...bByKey.keys()].sort()) {
    const child = path + '/' + escapeKey(key);
    if (!aByKey.has(key)) {
      ops.push({ k: 'add', p: child, n: clone(bByKey.get(key)) });
      continue;
    }
    diffValue(child, aByKey.get(key), bByKey.get(key), opts, ops);
  }
}

export { buildPath };
