import type { Condition, KeySchema } from './types.ts';
import { resolve } from './path.ts';
import { equal, compareOrdered } from './equal.ts';

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
export function evaluate(
  root: unknown,
  c: Condition | undefined | null,
  keys?: KeySchema,
): boolean {
  if (!c) return true;

  switch (c.o) {
    case 'and':
      return (c.apply ?? []).every((sub) => evaluate(root, sub, keys));
    case 'or':
      return (c.apply ?? []).some((sub) => evaluate(root, sub, keys));
    case 'not': {
      const sub = c.apply?.[0];
      if (!sub) throw new Error('malformed not condition: missing sub-condition');
      return !evaluate(root, sub, keys);
    }
  }

  const at = resolve(root, c.p ?? '', keys);
  if (c.o === 'exists') return at.found && at.value !== undefined;
  if (!at.found) throw new Error(`condition path ${c.p} does not resolve`);
  const value = at.value;

  switch (c.o) {
    case '==':
      return equal(value, c.v);
    case '!=':
      return !equal(value, c.v);
    case '>':
    case '>=':
    case '<':
    case '<=':
      return compareOrdered(value, c.v, c.o);
    case 'in': {
      if (!Array.isArray(c.v)) throw new Error('in requires an array value');
      return c.v.some((candidate) => equal(value, candidate));
    }
    case 'matches': {
      if (typeof c.v !== 'string') throw new Error('matches requires a string pattern');
      return new RegExp(c.v).test(String(value));
    }
    case 'type':
      if (typeof c.v !== 'string') throw new Error('type requires a string value');
      return checkType(value, c.v);
  }
  throw new Error(`unknown condition operator ${c.o}`);
}

/** The `type` operator's vocabulary. */
export function checkType(v: unknown, typeName: string): boolean {
  switch (typeName) {
    case 'string':
      return typeof v === 'string';
    case 'number':
      return typeof v === 'number';
    case 'boolean':
      return typeof v === 'boolean';
    case 'object':
      return typeof v === 'object' && v !== null && !Array.isArray(v);
    case 'array':
      return Array.isArray(v);
    case 'null':
      return v === null || v === undefined;
  }
  return false;
}
