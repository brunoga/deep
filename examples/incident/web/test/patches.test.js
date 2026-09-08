import { test } from 'node:test';
import assert from 'node:assert/strict';
import * as patches from '../patches.js';

// The browser's patch vocabulary must match what the Go CLI sends for the
// same action: the server distinguishes clients only by what they send, and
// the point of this client is that it sends the same thing.

test('claiming a task is conditional on nobody holding it', () => {
  const p = patches.claimTask('t1', 'web');
  assert.deepEqual(p, {
    ops: [
      {
        k: 'replace',
        p: '/tasks/t1/owner',
        n: 'web',
        if: { p: '/tasks/t1/owner', o: '==', v: '' },
      },
    ],
  });
});

test('escalation can never lower a severity', () => {
  const p = patches.escalate(2);
  const op = p.ops[0];
  assert.equal(op.p, '/severity');
  // The condition is the whole point: applied twice, or after somebody
  // escalated further, this skips instead of undoing their work.
  assert.deepEqual(op.if, { p: '/severity', o: '>', v: 2 });
});

test('closing is guarded rather than conditional per operation', () => {
  const p = patches.close();
  assert.deepEqual(p.cond, { p: '/status', o: '==', v: 'resolved' });
  assert.equal(p.ops.length, 1);
});

test('task ids are escaped into paths', () => {
  assert.equal(patches.completeTask('a/b').ops[0].p, '/tasks/a~1b/done');
  assert.equal(patches.claimTask('c~d', 'x').ops[0].p, '/tasks/c~0d/owner');
});

test('the key schema names the identity of the task list', () => {
  assert.deepEqual(patches.keys, { '/tasks': 'id' });
});
