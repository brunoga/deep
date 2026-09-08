// The notes pane: a textarea bound to a CRDT document.
//
// The record on the left and the notes here are the two halves of the
// library, side by side. The record needs one authoritative answer, so it
// goes through the server as patches. The notes need everybody typing at
// once, so they are a CRDT — and this page is a peer of the Go clients, not a
// viewer of them: what you type here reaches `incident open` and the reverse,
// with no server arbitration in between.
import { connect } from '@brunoga/deep-patch';

/**
 * The smallest edit that turns `before` into `after`.
 *
 * A textarea reports its whole value, not what changed, so the change has to
 * be recovered — and it must be recovered as *one* edit rather than a
 * wholesale replacement, or every keystroke would delete and reinsert the
 * document under everybody else's cursor.
 */
export function changeBetween(before, after) {
  const a = [...before];
  const b = [...after];
  let prefix = 0;
  while (prefix < a.length && prefix < b.length && a[prefix] === b[prefix]) prefix++;
  let suffix = 0;
  while (
    suffix < a.length - prefix &&
    suffix < b.length - prefix &&
    a[a.length - 1 - suffix] === b[b.length - 1 - suffix]
  ) {
    suffix++;
  }
  return {
    at: prefix,
    remove: a.length - prefix - suffix,
    insert: b.slice(prefix, b.length - suffix).join(''),
  };
}

/**
 * Maps a cursor across a change made by somebody else, so a remote edit above
 * the cursor does not drag it — the same rule the Go terminal client uses.
 */
export function shiftCursor(cursor, change) {
  if (cursor <= change.at) return cursor;
  const insertLength = [...change.insert].length;
  if (cursor >= change.at + change.remove) {
    return cursor - change.remove + insertLength;
  }
  return change.at + insertLength;
}

/**
 * Joins the room and keeps `textarea` and the document in step.
 *
 * Returns a handle with the peers currently editing and a way to leave.
 */
export async function joinNotes({ url, node, textarea, onPeers, onStatus }) {
  const room = await connect({ url, node });

  let applying = false;
  const render = () => {
    applying = true;
    const previous = textarea.value;
    const next = room.text;
    if (previous !== next) {
      const change = changeBetween(previous, next);
      const start = shiftCursor(textarea.selectionStart, change);
      const end = shiftCursor(textarea.selectionEnd, change);
      textarea.value = next;
      // Restoring the selection is what makes a remote edit feel like
      // somebody else typing rather than the page resetting under you.
      if (document.activeElement === textarea) textarea.setSelectionRange(start, end);
    }
    applying = false;
  };

  textarea.disabled = false;
  textarea.placeholder = 'shared notes — everyone in this incident types here';
  render();

  room.onUpdate(() => {
    render();
    onPeers?.(peers());
  });

  const peers = () => [...room.awareness.states().entries()].map(([id, s]) => s?.name ?? id).sort();
  room.awareness.onChange(() => onPeers?.(peers()));
  room.onClose((reason) => {
    textarea.disabled = true;
    onStatus?.(`notes offline: ${reason}`);
  });

  textarea.addEventListener('input', () => {
    if (applying) return;
    const change = changeBetween(room.text, textarea.value);
    if (change.remove > 0) room.delete(change.at, change.remove);
    if (change.insert !== '') room.insert(change.at, change.insert);
  });

  // Presence doubles as the heartbeat: a client that stops announcing stops
  // being drawn by its peers.
  const announce = () => room.announce({ name: node, pos: textarea.selectionStart });
  announce();
  textarea.addEventListener('keyup', announce);
  textarea.addEventListener('click', announce);
  const beat = setInterval(announce, 10_000);

  onPeers?.(peers());
  return {
    room,
    leave() {
      clearInterval(beat);
      room.close();
    },
  };
}
