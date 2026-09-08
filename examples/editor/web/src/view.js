// The view: text, carets and selections, drawn from the editor's state.
//
// The text is laid out as one element per line and everything else is
// absolutely positioned over it — carets, selection bands, the labels naming
// whoever owns them. That is roughly how a real editor does it, and it is why
// this can show *other people's* cursors inside the text, which a textarea
// cannot do at all.
//
// Positions arrive as code-point offsets and leave as pixels. The measuring
// assumes a monospace font, which keeps this file short; a proportional font
// would need per-character measurement, and nothing else here would change.
import { bounds, lineSpan, pointLength, toLineColumn } from './positions.js';

export class View {
  constructor(root, editor) {
    this.root = root;
    this.editor = editor;

    this.linesEl = root.querySelector('[data-lines]');
    this.gutterEl = root.querySelector('[data-gutter]');
    this.overlayEl = root.querySelector('[data-overlay]');
    this.metrics = { width: 8, height: 20 };
  }

  /** Measures one character, so offsets can become pixels. */
  measure() {
    const probe = document.createElement('span');
    probe.className = 'measure';
    probe.textContent = 'M';
    this.linesEl.append(probe);
    const rect = probe.getBoundingClientRect();
    if (rect.width > 0) this.metrics = { width: rect.width, height: rect.height };
    probe.remove();
  }

  /** Where a code-point offset sits, in pixels within the text area. */
  positionOf(lineIndex, offset) {
    const { line, column } = toLineColumn(lineIndex, offset);
    return { x: column * this.metrics.width, y: line * this.metrics.height, line, column };
  }

  /** The offset nearest a point, for clicking into the text. */
  offsetAt(lineIndex, clientX, clientY) {
    const rect = this.linesEl.getBoundingClientRect();
    const line = Math.max(
      0,
      Math.min(Math.floor((clientY - rect.top) / this.metrics.height), lineIndex.length - 1),
    );
    const info = lineIndex[line];
    const column = Math.max(
      0,
      Math.min(Math.round((clientX - rect.left) / this.metrics.width), pointLength(info.value)),
    );
    return info.start + column;
  }

  render() {
    const editor = this.editor;
    const lineIndex = editor.lineIndex;

    this.renderText(lineIndex);
    this.renderGutter(lineIndex);
    this.renderOverlay(lineIndex);
  }

  renderText(lineIndex) {
    // One element per line, replaced wholesale. A real editor would touch
    // only the lines that changed; this stays legible instead, and the
    // seam to improve is exactly here.
    this.linesEl.replaceChildren(
      ...lineIndex.map((line) => {
        const el = document.createElement('div');
        el.className = 'line';
        // textContent, never innerHTML: this is somebody else's typing.
        el.textContent = line.value === '' ? '​' : line.value;
        return el;
      }),
    );
  }

  renderGutter(lineIndex) {
    this.gutterEl.replaceChildren(
      ...lineIndex.map((_, i) => {
        const el = document.createElement('div');
        el.className = 'line-number';
        el.textContent = String(i + 1);
        return el;
      }),
    );
  }

  renderOverlay(lineIndex) {
    const parts = [];

    // Everyone else first, so the local caret draws on top of them.
    for (const [node, peer] of this.editor.peers) {
      parts.push(...this.selectionBands(lineIndex, peer.selection, peer.color, 0.25));
      parts.push(this.caret(lineIndex, peer.selection.head, peer.color, peer.name, node));
    }

    parts.push(...this.selectionBands(lineIndex, this.editor.selection, 'var(--select)', 1));
    const own = this.caret(lineIndex, this.editor.selection.head, 'var(--caret)', null, 'me');
    own.classList.add('own');
    parts.push(own);

    this.overlayEl.replaceChildren(...parts);
  }

  /** One band per line the selection covers. */
  selectionBands(lineIndex, selection, color, opacity) {
    const { from, to } = bounds(selection);
    if (from === to) return [];
    const first = toLineColumn(lineIndex, from).line;
    const last = toLineColumn(lineIndex, to).line;

    const bands = [];
    for (let line = first; line <= last; line++) {
      const span = lineSpan(lineIndex, line, selection);
      if (!span) continue;
      const el = document.createElement('div');
      el.className = 'band';
      el.style.left = `${span.from * this.metrics.width}px`;
      // A selection running past the end of a line is drawn a little wider,
      // so the newline it swallowed is visible rather than the band stopping
      // dead at the last character.
      const width = (span.to - span.from) * this.metrics.width + (span.trailing ? this.metrics.width * 0.6 : 0);
      el.style.width = `${Math.max(width, 2)}px`;
      el.style.top = `${line * this.metrics.height}px`;
      el.style.height = `${this.metrics.height}px`;
      el.style.background = color;
      el.style.opacity = String(opacity);
      bands.push(el);
    }
    return bands;
  }

  caret(lineIndex, offset, color, name, key) {
    const { x, y } = this.positionOf(lineIndex, offset);
    const el = document.createElement('div');
    el.className = 'caret';
    el.dataset.peer = key;
    el.style.left = `${x}px`;
    el.style.top = `${y}px`;
    el.style.height = `${this.metrics.height}px`;
    el.style.background = color;
    if (name) {
      const label = document.createElement('span');
      label.className = 'caret-name';
      label.textContent = name;
      label.style.background = color;
      el.append(label);
    }
    return el;
  }

  /** Scrolls the local caret into view after a movement. */
  revealCaret(lineIndex) {
    const view = this.root.querySelector('[data-scroll]');
    if (!view) return;
    // positionOf measures from the top of the text; scrollTop measures from
    // the top of the scrolling box, and there is padding between the two.
    // Reading it rather than assuming it keeps this honest if the stylesheet
    // changes.
    const inset =
      this.linesEl.getBoundingClientRect().top - view.getBoundingClientRect().top + view.scrollTop;
    const y = inset + this.positionOf(lineIndex, this.editor.selection.head).y;
    const top = view.scrollTop;
    const height = view.clientHeight;
    if (y < top) view.scrollTop = y;
    else if (y + this.metrics.height > top + height) {
      view.scrollTop = y + this.metrics.height - height;
    }
  }
}
