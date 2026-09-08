import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { applyPatch, clone, equal, reverse } from '../src/index.ts';
import type { KeySchema, Patch } from '../src/index.ts';

/**
 * The cross-language corpus: cases produced by the Go implementation, with
 * the result it produces. Passing them is what makes "compatible" a checkable
 * claim rather than a hope — divergence between two implementations of a sync
 * protocol is silent, and silent divergence is the whole failure mode worth
 * defending against.
 *
 * Regenerate with: go test ./internal/conformance
 */
interface Case {
  name: string;
  keys?: KeySchema;
  before: unknown;
  patch: Patch;
  after: unknown;
  applied: number;
  skipped: number;
  failed: number;
  reversible: boolean;
}

const dir = join(import.meta.dirname, '..', '..', 'testdata', 'conformance', 'patch');
const files = readdirSync(dir).filter((f) => f.endsWith('.json'));

test('the corpus is present', () => {
  assert.ok(files.length > 0, `no conformance cases in ${dir}`);
});

for (const file of files) {
  const c: Case = JSON.parse(readFileSync(join(dir, file), 'utf8'));

  test(`conformance: ${c.name}`, () => {
    const opts = { keys: c.keys };

    // Applying the patch must reach the state Go reached, operation for
    // operation — the same counts of applied, skipped and failed.
    const got = clone(c.before);
    const result = applyPatch(got, c.patch, opts);

    assert.deepEqual(got, c.after, 'resulting document differs from Go');
    assert.equal(result.applied, c.applied, 'applied count differs');
    assert.equal(result.skipped, c.skipped, 'skipped count differs');
    assert.equal(result.failed, c.failed, 'failed count differs');

    // And where Go says the patch reverses exactly, it must reverse here too.
    if (c.reversible) {
      const back = clone(c.after);
      const undone = applyPatch(back, reverse(c.patch), opts);
      assert.equal(undone.failed, 0, `reverse failed: ${undone.errors.map(String).join('; ')}`);
      assert.ok(equal(back, c.before), 'reverse did not return to the starting document');
    }
  });
}
