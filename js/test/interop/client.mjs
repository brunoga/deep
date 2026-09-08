// A live client, driven by the Go interop test.
//
// It joins a room served by deep/ws, applies whatever the hub sends, types
// its own text, and prints what it holds — so the Go side can compare the two
// implementations' idea of the same document.
import { connect } from '../../src/ws.ts';

const [url, node, ...edits] = process.argv.slice(2);

const room = await connect({ url, node });

for (const edit of edits) {
  const [op, a, b] = edit.split(':');
  if (op === 'insert') room.insert(Number(a), b);
  if (op === 'delete') room.delete(Number(a), Number(b));
  if (op === 'announce') room.announce({ name: a });
}

// Give the hub a moment to relay everything back before reporting.
await new Promise((r) => setTimeout(r, 400));

console.log(JSON.stringify({
  text: room.text,
  length: room.length,
  peers: [...room.awareness.states().keys()].sort(),
}));
room.close();
await new Promise((r) => setTimeout(r, 100));
