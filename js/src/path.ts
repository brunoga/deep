/**
 * JSON Pointer paths, as deep uses them. See docs/wire-patch.md.
 *
 * A path is a sequence of "/"-separated tokens. Within a token "~" is written
 * "~0" and "/" is written "~1"; unescaping does "~1" first so a literal "~1"
 * in a key survives the round trip.
 */

import type { KeySchema } from './types.ts';

export function escapeKey(key: string): string {
  return key.replaceAll('~', '~0').replaceAll('/', '~1');
}

export function unescapeKey(token: string): string {
  return token.replaceAll('~1', '/').replaceAll('~0', '~');
}

/** Splits a path into its unescaped tokens. The root path yields none. */
export function parsePath(path: string): string[] {
  if (path === '' || path === '/') return [];
  const tokens = path.startsWith('/') ? path.slice(1).split('/') : path.split('/');
  return tokens.map(unescapeKey);
}

/** Joins tokens into a path, escaping each. */
export function buildPath(tokens: string[]): string {
  return tokens.map((t) => '/' + escapeKey(t)).join('');
}

/** The path of the container holding `path`, or null for the root. */
export function parentPath(path: string): string | null {
  const i = path.lastIndexOf('/');
  if (i < 0) return null;
  return i === 0 ? '' : path.slice(0, i);
}

/** The last token of a path, unescaped. */
export function lastToken(path: string): string {
  const parts = parsePath(path);
  return parts.length === 0 ? '' : parts[parts.length - 1]!;
}

function isArrayIndex(token: string): boolean {
  return /^(0|[1-9][0-9]*)$/.test(token);
}

/**
 * Finds an element of a keyed array by its identity.
 *
 * Keyed arrays are addressed as /tasks/<id>, not /tasks/<index>. Go reads the
 * identity field from a `deep:"key"` struct tag; JavaScript has to be told,
 * which is what the schema is for. Returns -1 when the array is not keyed or
 * holds no such element.
 */
function keyedIndex(
  arr: unknown[],
  containerPath: string,
  token: string,
  keys: KeySchema | undefined,
): number {
  const field = keys?.[containerPath === '' ? '/' : containerPath];
  if (field === undefined) return -1;
  return arr.findIndex(
    (el) =>
      el !== null &&
      typeof el === 'object' &&
      String((el as Record<string, unknown>)[field]) === token,
  );
}

export interface Resolved {
  found: boolean;
  value?: unknown;
}

/** Reads the value at `path`, reporting whether anything is there. */
export function resolve(root: unknown, path: string, keys?: KeySchema): Resolved {
  let cur: unknown = root;
  let here = '';
  for (const token of parsePath(path)) {
    if (cur === null || cur === undefined) return { found: false };
    if (Array.isArray(cur)) {
      let i = isArrayIndex(token) ? Number(token) : keyedIndex(cur, here, token, keys);
      if (i < 0 || i >= cur.length) return { found: false };
      cur = cur[i];
      here += '/' + escapeKey(token);
      continue;
    }
    if (typeof cur !== 'object') return { found: false };
    const obj = cur as Record<string, unknown>;
    if (!(token in obj)) return { found: false };
    cur = obj[token];
    here += '/' + escapeKey(token);
  }
  return { found: true, value: cur };
}

/**
 * Walks to the container of `path`, creating nothing.
 *
 * Returns the container and the final token, or null when the route does not
 * exist — an operation addressing a path through a missing container fails
 * rather than conjuring one, matching Go.
 */
function locate(
  root: unknown,
  path: string,
  keys?: KeySchema,
): { parent: unknown; token: string; containerPath: string } | null {
  const parts = parsePath(path);
  if (parts.length === 0) return null;
  let cur: unknown = root;
  let here = '';
  for (let i = 0; i < parts.length - 1; i++) {
    const token = parts[i]!;
    if (cur === null || cur === undefined) return null;
    if (Array.isArray(cur)) {
      const idx = isArrayIndex(token) ? Number(token) : keyedIndex(cur, here, token, keys);
      if (idx < 0 || idx >= cur.length) return null;
      cur = cur[idx];
      here += '/' + escapeKey(token);
      continue;
    }
    if (typeof cur !== 'object') return null;
    const obj = cur as Record<string, unknown>;
    if (!(token in obj)) return null;
    cur = obj[token];
    here += '/' + escapeKey(token);
  }
  return { parent: cur, token: parts[parts.length - 1]!, containerPath: here };
}

/**
 * Writes `value` at `path`. `insert` distinguishes an `add` (which may extend
 * an array) from a `replace` (which may not).
 */
export function setAt(
  root: unknown,
  path: string,
  value: unknown,
  insert: boolean,
  keys?: KeySchema,
): void {
  const at = locate(root, path, keys);
  if (at === null) throw new Error(`path ${path} does not resolve`);
  const { parent, token, containerPath } = at;
  if (Array.isArray(parent)) {
    if (token === '-') {
      if (!insert) throw new Error(`path ${path} does not resolve`);
      parent.push(value);
      return;
    }
    if (!isArrayIndex(token)) {
      // A keyed element: present means replace it where it sits, absent means
      // append. Position carries no meaning in a keyed array, so appending is
      // not a choice about order.
      const found = keyedIndex(parent, containerPath, token, keys);
      if (found >= 0) parent[found] = value;
      else if (keys?.[containerPath === '' ? '/' : containerPath] !== undefined) parent.push(value);
      else throw new Error(`array index expected at ${path}, got ${token}`);
      return;
    }
    const i = Number(token);
    if (i > parent.length || (i === parent.length && !insert)) {
      throw new Error(`index ${i} out of range at ${path}`);
    }
    if (insert && i === parent.length) parent.push(value);
    else if (insert) parent.splice(i, 0, value);
    else parent[i] = value;
    return;
  }
  if (parent === null || typeof parent !== 'object') {
    throw new Error(`cannot write ${path}: container is not an object`);
  }
  (parent as Record<string, unknown>)[token] = value;
}

/** Deletes the value at `path`. */
export function removeAt(root: unknown, path: string, keys?: KeySchema): void {
  const at = locate(root, path, keys);
  if (at === null) throw new Error(`path ${path} does not resolve`);
  const { parent, token, containerPath } = at;
  if (Array.isArray(parent)) {
    const i = isArrayIndex(token) ? Number(token) : keyedIndex(parent, containerPath, token, keys);
    if (i < 0) throw new Error(`no element ${token} at ${path}`);
    if (i >= parent.length) throw new Error(`index ${i} out of range at ${path}`);
    parent.splice(i, 1);
    return;
  }
  if (parent === null || typeof parent !== 'object') {
    throw new Error(`cannot remove ${path}: container is not an object`);
  }
  const obj = parent as Record<string, unknown>;
  if (!(token in obj)) throw new Error(`key ${token} not found at ${path}`);
  delete obj[token];
}

/** True when `ancestor` covers `descendant` at a token boundary. */
export function encloses(ancestor: string, descendant: string): boolean {
  if (ancestor === descendant) return true;
  if (ancestor === '' || ancestor === '/') return true;
  return descendant.startsWith(ancestor + '/');
}
