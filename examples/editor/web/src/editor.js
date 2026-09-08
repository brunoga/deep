// The editor core: a selection over a document, and the commands that move
// it. No DOM here — it takes any object with `text`, `insert(at, value)` and
// `delete(at, count)`, which in the browser is a CRDT room and in tests is a
// string in a box. That is what makes the interesting behaviour testable
// without a browser.
import {
  bounds,
  changeBetween,
  isEmpty,
  lines,
  pointLength,
  shiftSelection,
  toLineColumn,
  toOffsetFromLineColumn,
} from './positions.js';

export class Editor {
  /**
   * @param doc something with `text`, `insert(at, value)` and
   *   `delete(at, count)`, counted in code points.
   */
  constructor(doc) {
    this.doc = doc;
    this.selection = { anchor: 0, head: 0 };
    /** Peers, by node id: their selection, name and colour. */
    this.peers = new Map();
    /** The text as of the last time this editor looked, for change detection. */
    this.lastText = doc.text;
    this.listeners = new Set();
    /**
     * Listeners for changes this editor made itself, as opposed to changes it
     * received. Presence announcements hang off these — announcing on *every*
     * change would mean a peer's announcement arriving, updating their caret,
     * notifying, and announcing back at them, forever.
     */
    this.localListeners = new Set();
    /**
     * The column a vertical movement is trying to keep. Moving down through a
     * short line and back up should return to where it started, which means
     * remembering the column across the short line rather than taking it from
     * where the caret landed.
     */
    this.goalColumn = null;
  }

  get text() {
    return this.doc.text;
  }

  get lineIndex() {
    return lines(this.doc.text);
  }

  /** Runs fn whenever anything changes: this editor's doing or somebody else's. */
  onChange(fn) {
    this.listeners.add(fn);
    return () => this.listeners.delete(fn);
  }

  /**
   * Runs fn only for changes this editor made — an edit or a movement.
   *
   * The distinction matters: a peer's caret moving is not news to announce
   * back to them, and treating it as news makes two clients talk to each
   * other about nothing at increasing speed.
   */
  onLocalChange(fn) {
    this.localListeners.add(fn);
    return () => this.localListeners.delete(fn);
  }

  changed() {
    for (const fn of this.listeners) fn();
  }

  /** Notifies both channels: something local happened. */
  localChanged() {
    for (const fn of this.localListeners) fn();
    this.changed();
  }

  // ── editing ───────────────────────────────────────────────────────────

  /** Replaces the selection with value; an empty selection is an insertion. */
  type(value) {
    const { from, to } = bounds(this.selection);
    if (to > from) this.doc.delete(from, to - from);
    if (value !== '') this.doc.insert(from, value);
    const at = from + pointLength(value);
    this.selection = { anchor: at, head: at };
    this.lastText = this.doc.text;
    this.goalColumn = null;
    this.localChanged();
  }

  /** Deletes the selection, or the character before the caret. */
  backspace() {
    const { from, to } = bounds(this.selection);
    if (to > from) {
      this.type('');
      return;
    }
    if (from === 0) return;
    this.doc.delete(from - 1, 1);
    this.selection = { anchor: from - 1, head: from - 1 };
    this.lastText = this.doc.text;
    this.goalColumn = null;
    this.localChanged();
  }

  /** Deletes the selection, or the character after the caret. */
  deleteForward() {
    const { from, to } = bounds(this.selection);
    if (to > from) {
      this.type('');
      return;
    }
    if (from >= pointLength(this.doc.text)) return;
    this.doc.delete(from, 1);
    this.selection = { anchor: from, head: from };
    this.lastText = this.doc.text;
    this.goalColumn = null;
    this.localChanged();
  }

  // ── moving ────────────────────────────────────────────────────────────

  /** Puts the caret at an offset; extend keeps the anchor, as shift does. */
  moveTo(offset, extend = false) {
    const at = Math.max(0, Math.min(offset, pointLength(this.doc.text)));
    this.selection = { anchor: extend ? this.selection.anchor : at, head: at };
    this.goalColumn = null;
    this.localChanged();
  }

  /**
   * Moves the caret by characters. With no selection to collapse, a plain
   * left or right at the edge of a selection moves to that edge rather than
   * one character past it — what every editor does, and what people expect
   * without being able to say so.
   */
  moveBy(delta, extend = false) {
    if (!extend && !isEmpty(this.selection)) {
      const { from, to } = bounds(this.selection);
      this.moveTo(delta < 0 ? from : to);
      return;
    }
    this.moveTo(this.selection.head + delta, extend);
  }

  /** Moves the caret a line at a time, keeping the goal column. */
  moveLine(delta, extend = false) {
    const index = this.lineIndex;
    const here = toLineColumn(index, this.selection.head);
    const column = this.goalColumn ?? here.column;
    const line = Math.max(0, Math.min(here.line + delta, index.length - 1));
    const at = toOffsetFromLineColumn(index, line, column);
    this.selection = { anchor: extend ? this.selection.anchor : at, head: at };
    this.goalColumn = column;
    this.localChanged();
  }

  /** Moves to the start or end of the current line. */
  moveToLineEdge(edge, extend = false) {
    const index = this.lineIndex;
    const { line } = toLineColumn(index, this.selection.head);
    const column = edge === 'start' ? 0 : pointLength(index[line].value);
    this.moveTo(toOffsetFromLineColumn(index, line, column), extend);
  }

  selectAll() {
    this.selection = { anchor: 0, head: pointLength(this.doc.text) };
    this.localChanged();
  }

  /** The text the selection covers. */
  selectedText() {
    const { from, to } = bounds(this.selection);
    return [...this.doc.text].slice(from, to).join('');
  }

  // ── other people ──────────────────────────────────────────────────────

  /**
   * Folds in a change made elsewhere.
   *
   * Everybody's caret has to move with the text: this editor's own, and every
   * peer's, because presence arrives asynchronously and their last announced
   * position was measured against the document as it was. Without this a
   * remote insertion above your caret drags it forward a character at a time
   * while you watch.
   */
  remoteChanged() {
    const before = this.lastText;
    const after = this.doc.text;
    if (before === after) return;
    const change = changeBetween(before, after);
    this.lastText = after;
    this.selection = shiftSelection(this.selection, change);
    for (const peer of this.peers.values()) {
      peer.selection = shiftSelection(peer.selection, change);
    }
    this.changed();
  }

  /** Records where a peer says they are. */
  setPeer(node, { name, color, selection }) {
    if (!selection) return;
    this.peers.set(node, {
      name: name ?? node,
      color: color ?? colorFor(node),
      selection,
    });
    this.changed();
  }

  /** Forgets peers no longer present, keeping the view in step with the room. */
  keepPeers(present) {
    let changed = false;
    for (const node of [...this.peers.keys()]) {
      if (!present.has(node)) {
        this.peers.delete(node);
        changed = true;
      }
    }
    if (changed) this.changed();
  }

  /** What to announce: where this editor's caret and selection are. */
  presence() {
    return { selection: { ...this.selection } };
  }
}

/** A stable colour per peer, so somebody keeps the same one all session. */
export function colorFor(node) {
  let hash = 0;
  for (const ch of node) hash = (hash * 31 + ch.codePointAt(0)) >>> 0;
  return `hsl(${hash % 360} 70% 62%)`;
}

/**
 * A document that lives only in memory, for tests and for the editor working
 * before a connection is up. It is the same shape a room presents.
 */
export class LocalDocument {
  constructor(text = '') {
    this.text = text;
  }

  insert(at, value) {
    const p = [...this.text];
    this.text = [...p.slice(0, at), ...[...value], ...p.slice(at)].join('');
  }

  delete(at, count) {
    const p = [...this.text];
    this.text = [...p.slice(0, at), ...p.slice(at + count)].join('');
  }
}

/** Maps a key event to a command. Kept apart from the DOM so it can be tested. */
export function commandFor(event) {
  const extend = event.shiftKey;
  switch (event.key) {
    case 'ArrowLeft':
      return (ed) => ed.moveBy(-1, extend);
    case 'ArrowRight':
      return (ed) => ed.moveBy(1, extend);
    case 'ArrowUp':
      return (ed) => ed.moveLine(-1, extend);
    case 'ArrowDown':
      return (ed) => ed.moveLine(1, extend);
    case 'Home':
      return (ed) => ed.moveToLineEdge('start', extend);
    case 'End':
      return (ed) => ed.moveToLineEdge('end', extend);
    case 'Backspace':
      return (ed) => ed.backspace();
    case 'Delete':
      return (ed) => ed.deleteForward();
    case 'Enter':
      return (ed) => ed.type('\n');
    case 'Tab':
      return (ed) => ed.type('  ');
    case 'a':
      if (event.ctrlKey || event.metaKey) return (ed) => ed.selectAll();
      return null;
    default:
      return null;
  }
}
