// The notes pane: a textarea bound to a CRDT document.
//
// The record on the left and the notes here are the two halves of the
// library, side by side. The record needs one authoritative answer, so it
// goes through the server as patches. The notes need everybody typing at
// once, so they are a CRDT — and this page is a peer of the Go clients, not a
// viewer of them: what you type here reaches `incident open` and the reverse,
// with no server arbitration in between.
import { connect } from '@brunoga/deep-patch';
import { changeBetween, shiftCursor, toCodePoints, toUTF16 } from './textedit.js';

/**
 * Joins the room and keeps `textarea` and the document in step.
 *
 * Returns a handle with the peers currently editing and a way to leave.
 */
export async function joinNotes({ url, node, textarea, onPeers, onStatus }) {
  const room = await connect({ url, node });

  // Set when this room is left, so its callbacks stop touching a textarea
  // that now belongs to another room. Without it, the old socket's close
  // event — which arrives after the new room has joined — disables the pane
  // that is working perfectly well.
  let stale = false;

  let applying = false;
  const render = () => {
    if (stale) return;
    applying = true;
    const previous = textarea.value;
    const next = room.text;
    if (previous !== next) {
      const change = changeBetween(previous, next);
      const start = shiftCursor(toCodePoints(previous, textarea.selectionStart), change);
      const end = shiftCursor(toCodePoints(previous, textarea.selectionEnd), change);
      textarea.value = next;
      // Restoring the selection is what makes a remote edit feel like
      // somebody else typing rather than the page resetting under you.
      if (document.activeElement === textarea) {
        textarea.setSelectionRange(toUTF16(next, start), toUTF16(next, end));
      }
    }
    applying = false;
  };

  textarea.disabled = false;
  textarea.placeholder = 'shared notes — everyone in this incident types here';
  render();

  const peers = () => [...room.awareness.states().entries()].map(([id, s]) => s?.name ?? id).sort();

  room.onUpdate(() => {
    render();
    if (!stale) onPeers?.(peers());
  });
  room.awareness.onChange(() => {
    if (!stale) onPeers?.(peers());
  });
  room.onClose((reason) => {
    if (stale) return;
    textarea.disabled = true;
    onStatus?.(`notes offline: ${reason}`);
  });

  const onInput = () => {
    if (applying || stale) return;
    const change = changeBetween(room.text, textarea.value);
    if (change.remove > 0) room.delete(change.at, change.remove);
    if (change.insert !== '') room.insert(change.at, change.insert);
  };

  // Presence doubles as the heartbeat: a client that stops announcing stops
  // being drawn by its peers.
  const announce = () => {
    if (stale) return;
    room.announce({ name: node, pos: toCodePoints(textarea.value, textarea.selectionStart) });
  };

  textarea.addEventListener('input', onInput);
  textarea.addEventListener('keyup', announce);
  textarea.addEventListener('click', announce);
  const beat = setInterval(announce, 10_000);
  announce();

  onPeers?.(peers());
  return {
    room,
    leave() {
      // Everything this room attached to the shared textarea comes off with
      // it. The element outlives the room — the page reuses one for every
      // incident — so a listener left behind would send the next incident's
      // keystrokes to this incident's document.
      stale = true;
      clearInterval(beat);
      textarea.removeEventListener('input', onInput);
      textarea.removeEventListener('keyup', announce);
      textarea.removeEventListener('click', announce);
      room.close();
    },
  };
}
