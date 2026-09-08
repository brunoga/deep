import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  applyPatch,
  clone,
  diff,
  encloses,
  equal,
  escapeKey,
  evaluate,
  merge,
  parsePath,
  reverse,
  unescapeKey,
  GuardNotMetError,
  PathNotAllowedError,
} from '../src/index.ts';

test('path tokens escape and unescape per RFC 6901', () => {
  assert.equal(escapeKey('a/b~c'), 'a~1b~0c');
  assert.equal(unescapeKey('a~1b~0c'), 'a/b~c');
  // "~1" unescapes before "~0", so a literal "~1" survives the round trip.
  assert.equal(unescapeKey(escapeKey('a~1b')), 'a~1b');
  assert.deepEqual(parsePath('/a~1b/2/c'), ['a/b', '2', 'c']);
  assert.deepEqual(parsePath('/'), []);
  assert.deepEqual(parsePath(''), []);
});

test('enclosure is judged at token boundaries', () => {
  assert.ok(encloses('/user', '/user/name'));
  assert.ok(!encloses('/user', '/username'));
  assert.ok(encloses('', '/anything'));
});

test('a diff applies to reach the target, and reverses back', () => {
  const a = { n: 1, o: { x: 'y' }, list: [1, 2], map: { k: 'v' } };
  const b = { n: 2, o: { x: 'z', added: true }, list: [1, 2, 3], map: {} };

  const p = diff(a, b);
  const forward = clone(a);
  const applied = applyPatch(forward, p);
  assert.equal(applied.failed, 0);
  assert.deepEqual(forward, b);

  const back = clone(forward);
  assert.equal(applyPatch(back, reverse(p)).failed, 0);
  assert.deepEqual(back, a);
});

test('a diff of equal values is empty', () => {
  assert.deepEqual(diff({ a: [1, { b: 2 }] }, { a: [1, { b: 2 }] }).ops, []);
});

test('object keys diff in sorted order, so patches are reproducible', () => {
  const p = diff({}, { z: 1, a: 2, m: 3 });
  assert.deepEqual(p.ops?.map((o) => o.p), ['/a', '/m', '/z']);
});

test('keyed arrays are addressed by identity, and reordering is not a change', () => {
  const keys = { '/items': 'id' };
  const a = { items: [{ id: 'x', n: 1 }, { id: 'y', n: 2 }] };
  const reordered = { items: [{ id: 'y', n: 2 }, { id: 'x', n: 1 }] };
  assert.deepEqual(diff(a, reordered, { keys }).ops, []);

  const changed = { items: [{ id: 'y', n: 9 }, { id: 'x', n: 1 }] };
  const p = diff(a, changed, { keys });
  assert.deepEqual(p.ops, [{ k: 'replace', p: '/items/y/n', o: 2, n: 9 }]);

  const target = clone(a);
  assert.equal(applyPatch(target, p, { keys }).failed, 0);
  assert.equal((target.items.find((i) => i.id === 'y') as { n: number }).n, 9);
});

test('an unkeyed array that differs is replaced whole', () => {
  const p = diff({ xs: [1, 2, 3] }, { xs: [1, 9, 3] });
  assert.deepEqual(p.ops, [{ k: 'replace', p: '/xs', o: [1, 2, 3], n: [1, 9, 3] }]);
});

test('a failing operation does not stop the ones after it', () => {
  const target = { a: 1, b: 2 };
  const result = applyPatch(target, {
    ops: [
      { k: 'replace', p: '/missing/deeper', n: 1 },
      { k: 'replace', p: '/b', n: 20 },
    ],
  });
  assert.equal(result.failed, 1);
  assert.equal(result.applied, 1);
  assert.equal(target.b, 20);
});

test('a skipped operation is not a failure', () => {
  const target = { owner: 'ana' };
  const result = applyPatch(target, {
    ops: [{ k: 'replace', p: '/owner', n: 'bo', if: { p: '/owner', o: '==', v: 'nobody' } }],
  });
  assert.equal(result.skipped, 1);
  assert.equal(result.failed, 0);
  assert.equal(result.errors.length, 0);
  assert.equal(target.owner, 'ana');
});

test('a guard that does not hold makes the whole patch a no-op', () => {
  const target = { status: 'open', n: 1 };
  const result = applyPatch(target, {
    cond: { p: '/status', o: '==', v: 'closed' },
    ops: [{ k: 'replace', p: '/n', n: 99 }],
  });
  assert.ok(result.errors[0] instanceof GuardNotMetError);
  assert.deepEqual(target, { status: 'open', n: 1 });
});

test('the allowlist refuses a patch before any of it runs', () => {
  const target = { mine: 1, yours: 2 };
  const result = applyPatch(
    target,
    { ops: [{ k: 'replace', p: '/mine', n: 10 }, { k: 'replace', p: '/yours', n: 20 }] },
    { allowedPaths: ['/mine'] },
  );
  assert.ok(result.errors[0] instanceof PathNotAllowedError);
  assert.deepEqual(target, { mine: 1, yours: 2 }, 'nothing may be applied when a path is refused');
});

test('strict mode refuses to write over state that has moved', () => {
  const target = { status: 'mitigated' };
  const result = applyPatch(target, {
    strict: true,
    ops: [{ k: 'replace', p: '/status', o: 'open', n: 'closed' }],
  });
  assert.equal(result.failed, 1);
  assert.equal(target.status, 'mitigated');
});

test('conditions cover the operator vocabulary', () => {
  const v = { n: 5, s: 'hello', arr: [1, 2], nothing: null };
  assert.ok(evaluate(v, { p: '/n', o: '>=', v: 5 }));
  assert.ok(!evaluate(v, { p: '/n', o: '<', v: 5 }));
  assert.ok(evaluate(v, { p: '/s', o: 'matches', v: '^hel' }));
  assert.ok(evaluate(v, { p: '/n', o: 'in', v: [3, 5, 7] }));
  assert.ok(evaluate(v, { p: '/arr', o: 'type', v: 'array' }));
  assert.ok(evaluate(v, { p: '/nothing', o: 'type', v: 'null' }));
  assert.ok(evaluate(v, { p: '/s', o: 'exists' }));
  assert.ok(!evaluate(v, { p: '/absent', o: 'exists' }), 'exists is false, not an error');
  assert.ok(evaluate(v, { o: 'and', apply: [{ p: '/n', o: '==', v: 5 }, { p: '/s', o: 'exists' }] }));
  assert.ok(evaluate(v, { o: 'or', apply: [{ p: '/n', o: '==', v: 0 }, { p: '/n', o: '==', v: 5 }] }));
  assert.ok(evaluate(v, { o: 'not', apply: [{ p: '/n', o: '==', v: 0 }] }));
  // Every operator but `exists` treats an unresolvable path as an error.
  assert.throws(() => evaluate(v, { p: '/absent', o: '==', v: 1 }));
});

test('merge resolves concurrent writes to the same path', () => {
  const base = { status: 'open', score: 1, note: 'a' };
  const theirs = diff(base, { ...base, status: 'closed', score: 2 });
  const mine = diff(base, { ...base, status: 'mitigated', note: 'b' });

  const seen: string[] = [];
  const merged = merge(theirs, mine, (path, t, o) => {
    seen.push(path);
    return path === '/status' ? t : o;
  });
  assert.deepEqual(seen, ['/status']);

  const target = clone(base);
  assert.equal(applyPatch(target, merged).failed, 0);
  assert.deepEqual(target, { status: 'closed', score: 2, note: 'b' });
});

test('merge keeps the second patch where paths enclose each other', () => {
  const theirs = { ops: [{ k: 'replace' as const, p: '/user', n: { name: 'x' } }] };
  const mine = { ops: [{ k: 'replace' as const, p: '/user/name', n: 'y' }] };
  const merged = merge(theirs, mine);
  assert.deepEqual(merged.ops, [{ k: 'replace', p: '/user/name', n: 'y' }]);
});

test('equality and cloning are structural and independent', () => {
  const v = { a: [1, { b: 2 }], c: null };
  const copy = clone(v);
  assert.ok(equal(v, copy));
  (copy.a[1] as { b: number }).b = 3;
  assert.ok(!equal(v, copy), 'the clone must not share structure');
});
