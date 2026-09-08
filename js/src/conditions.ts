import type { Condition } from './types.ts';
import { resolve } from './path.ts';
import { equal, compareOrdered } from './equal.ts';

/**
 * Evaluates a condition against a value.
 *
 * A path that does not resolve is an error for every operator but `exists`,
 * which reports false — matching Go, where an unresolvable path means the
 * condition cannot be judged rather than that it failed.
 */
export function evaluate(root: unknown, c: Condition | undefined | null): boolean {
  if (!c) return true;

  switch (c.o) {
    case 'and':
      return (c.apply ?? []).every((sub) => evaluate(root, sub));
    case 'or':
      return (c.apply ?? []).some((sub) => evaluate(root, sub));
    case 'not': {
      const sub = c.apply?.[0];
      if (!sub) throw new Error('malformed not condition: missing sub-condition');
      return !evaluate(root, sub);
    }
  }

  const at = resolve(root, c.p ?? '');
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
