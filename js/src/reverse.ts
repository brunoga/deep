import type { Operation, Patch } from './types.ts';
import { parentPath } from './path.ts';

/**
 * Returns the patch that undoes this one.
 *
 * Operations are reversed in order as well as in kind, since they applied in
 * order. The result is exact only where the operations carry their `o`
 * values — which diff-produced patches always do.
 */
export function reverse(patch: Patch): Patch {
  const out: Operation[] = [];
  const ops = patch.ops ?? [];
  for (let i = ops.length - 1; i >= 0; i--) {
    const op = ops[i]!;
    // A log operation changes nothing, so its inverse is nothing.
    if (op.k === 'log') continue;
    out.push(reverseOne(op));
  }
  restoreSiblingAddOrder(out);
  return { ops: out, strict: patch.strict };
}

function reverseOne(op: Operation): Operation {
  switch (op.k) {
    case 'add':
      return { k: 'remove', p: op.p, o: op.n };
    case 'remove':
      return { k: 'add', p: op.p, n: op.o };
    case 'replace':
      return { k: 'replace', p: op.p, o: op.n, n: op.o };
    case 'move': {
      const rev: Operation = { k: 'move', p: op.f ?? '', f: op.p };
      // Restore whatever the move displaced at its destination; without this
      // the reversal strands that value.
      if (op.o !== undefined) rev.n = op.o;
      return rev;
    }
    case 'copy':
    case 'alias':
      // Reversing a copy or an alias never needs to be one: the prior
      // destination value is an ordinary value to put back, and where there
      // was none, removing suffices.
      if (op.o !== undefined) return { k: 'replace', p: op.p, o: op.n, n: op.o };
      return { k: 'remove', p: op.p, o: op.n };
  }
  return { k: op.k, p: op.p };
}

/**
 * Reversing turns a run of sibling adds back to front, which for an array
 * means they insert in the wrong order. Flipping each such run restores it.
 */
function restoreSiblingAddOrder(ops: Operation[]): void {
  for (let i = 0; i < ops.length; ) {
    const parent = parentPath(ops[i]!.p);
    if (parent === null || ops[i]!.k !== 'add') {
      i++;
      continue;
    }
    let j = i + 1;
    while (j < ops.length && ops[j]!.k === 'add' && parentPath(ops[j]!.p) === parent) j++;
    for (let l = i, r = j - 1; l < r; l++, r--) {
      const tmp = ops[l]!;
      ops[l] = ops[r]!;
      ops[r] = tmp;
    }
    i = j;
  }
}
