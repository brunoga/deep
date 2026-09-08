import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readdirSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { Document } from '../src/crdt/document.ts';
import {
  decodeStateVector,
  decodeUpdate,
  encodeStateVector,
  encodeUpdate,
  fromHex,
  toHex,
} from '../src/crdt/binary.ts';
import { origin as originOf } from '../src/crdt/hlc.ts';

/**
 * The CRDT corpus, replayed.
 *
 * A patch either applies or it does not, so a patch case can be judged on its
 * own. Two replicas that order text differently produce no error at all —
 * they simply hold different documents, forever. So these cases replay real
 * exchanges recorded from the Go implementation and require this one to end
 * up where that one did, byte for byte where bytes are involved.
 *
 * Regenerate with: go test ./internal/conformance/crdtcase
 */
interface Step {
  node: string;
  op: 'insert' | 'delete' | 'sync';
  pos?: number;
  value?: string;
  count?: number;
  to?: string;
}

interface Case {
  name: string;
  nodes: string[];
  steps: Step[];
  /** Null rather than empty when a scenario has a single replica. */
  exchanges: string[] | null;
  text: Record<string, string>;
  full: Record<string, string>;
  state_vectors: Record<string, string>;
  /** The wall time each replica's clock was pinned to. */
  walls: Record<string, number>;
}

const dir = join(import.meta.dirname, '..', '..', 'testdata', 'conformance', 'crdt');
const files = readdirSync(dir).filter((f) => f.endsWith('.json'));

test('the CRDT corpus is present', () => {
  assert.ok(files.length > 0, `no cases in ${dir}`);
});

for (const file of files) {
  const c: Case = JSON.parse(readFileSync(join(dir, file), 'utf8'));

  // Every recorded payload must decode, and re-encode to the same bytes. A
  // codec that reads Go's output but writes something subtly different would
  // pass a text comparison and still be unusable.
  test(`crdt codec: ${c.name}`, () => {
    for (const hex of [...(c.exchanges ?? []), ...Object.values(c.full)]) {
      const update = decodeUpdate(fromHex(hex));
      assert.equal(toHex(encodeUpdate(update)), hex, 'update did not re-encode identically');
    }
    for (const hex of Object.values(c.state_vectors)) {
      const sv = decodeStateVector(fromHex(hex));
      assert.equal(toHex(encodeStateVector(sv)), hex, 'state vector did not re-encode identically');
    }
  });

  // A replica built only from what Go sent must hold what Go holds. This is
  // the ordering algorithm under test: concurrent insertions at one position,
  // deletions inside runs, anchors pointing into the middle of a run.
  test(`crdt convergence: ${c.name}`, () => {
    for (const node of c.nodes) {
      const replica = new Document('observer');
      replica.apply(decodeUpdate(fromHex(c.full[node]!)));
      assert.equal(
        replica.toString(),
        c.text[node],
        `a replica built from ${node}'s state holds different text`,
      );
    }
  });

  // The strict test: replay the whole scenario, generating this
  // implementation's *own* identifiers for its own edits, with the clocks
  // pinned where Go pinned them. Matching text is not enough here — the
  // replicas must hold the same state down to the encoded bytes, which is
  // what proves the edits themselves are identical and not merely
  // compatible.
  test(`crdt replay: ${c.name}`, () => {
    const replicas = new Map<string, Document>();
    for (const node of c.nodes) {
      replicas.set(node, new Document(node, BigInt(c.walls[node]!)));
    }
    let exchange = 0;
    for (const step of c.steps) {
      const doc = replicas.get(step.node)!;
      switch (step.op) {
        case 'insert':
          doc.insert(step.pos ?? 0, step.value ?? '');
          break;
        case 'delete':
          doc.delete(step.pos ?? 0, step.count ?? 0);
          break;
        case 'sync': {
          // The recorded bytes are replayed rather than recomputed: what is
          // under test is that this implementation *applies* what Go sent the
          // way Go's peer did.
          const update = decodeUpdate(fromHex(c.exchanges![exchange++]!));
          replicas.get(step.to!)!.apply(update);
          break;
        }
      }
    }
    for (const node of c.nodes) {
      const doc = replicas.get(node)!;
      assert.equal(doc.toString(), c.text[node], `replica ${node} holds different text`);
      assert.equal(
        toHex(encodeStateVector(doc.stateVector())),
        c.state_vectors[node],
        `replica ${node} has a different state vector`,
      );
      assert.equal(
        toHex(encodeUpdate(doc.since(new Map()))),
        c.full[node],
        `replica ${node} encodes its state differently`,
      );
    }
  });
}

// The decoder is network-facing, so it refuses what it cannot trust rather
// than doing its best with it — the same guards the Go decoder carries, one
// of which was added there after a seven-byte payload could exhaust memory.
test('the decoder refuses malformed payloads', () => {
  assert.throws(() => decodeUpdate(new Uint8Array()), /empty payload/);
  assert.throws(() => decodeUpdate(Uint8Array.from([9, 0])), /unsupported wire format/);

  // A count larger than the bytes that remain cannot be honest, and honouring
  // it would allocate for entries that are not there.
  assert.throws(() => decodeUpdate(Uint8Array.from([1, 0, 0xff, 0xff, 0xff, 0x7f])), /claims/);
  assert.throws(() => decodeStateVector(Uint8Array.from([1, 0, 0xff, 0xff, 0xff, 0x7f])), /claims/);
  // Likewise a node table promising more entries than the payload can hold.
  assert.throws(() => decodeUpdate(Uint8Array.from([1, 0xff, 0xff, 0xff, 0x7f])), /node table/);

  // Trailing bytes mean the sender and receiver disagree about the frame.
  const valid = encodeUpdate({ runs: [], deleted: [] });
  const extra = new Uint8Array(valid.length + 1);
  extra.set(valid);
  assert.throws(() => decodeUpdate(extra), /left over/);
});

// Two replicas that share an origin allocate the same identifiers for
// different characters, and the merge silently collapses them. The clock
// makes that vanishingly unlikely even for two tabs started together under
// one name.
test('clocks started together still get distinct origins', () => {
  const origins = new Set<string>();
  for (let i = 0; i < 200; i++) {
    const doc = new Document('same-name');
    doc.insert(0, 'x');
    origins.add(originOf(doc.text[0]!.id));
  }
  assert.equal(origins.size, 200, 'origins collided');
});
