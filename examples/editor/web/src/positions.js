// Positions.
//
// An editor juggles three coordinate systems at once, and confusing them is
// the source of most editor bugs:
//
//   - **code points**, which is what the CRDT counts. Go counts runes and so
//     does the document, so this is the only system in which an offset means
//     the same thing on both sides of the wire.
//   - **UTF-16 units**, which is what JavaScript strings and DOM ranges use.
//     An emoji is one code point and two units.
//   - **line and column**, which is what a person sees and what a caret is
//     drawn at.
//
// Everything here is pure and converts between them, so it can be tested
// without a browser — and so the rest of the editor never has to guess which
// kind of number it is holding.

/** A document as an array of code points, which is how offsets are counted. */
export function points(text) {
  return [...text];
}

/** The number of code points in text. */
export function pointLength(text) {
  return points(text).length;
}

/** A UTF-16 index into text, as a code-point offset. */
export function toOffset(text, utf16Index) {
  return points(text.slice(0, utf16Index)).length;
}

/** A code-point offset, as a UTF-16 index. */
export function toUTF16(text, offset) {
  return points(text).slice(0, offset).join('').length;
}

/**
 * Splits text into lines, keeping enough to map between offsets and
 * line/column without re-scanning the document for every caret.
 *
 * `start` is the offset of the line's first character; a line's length
 * excludes its newline, so the caret at the end of a line and the caret at
 * the start of the next are different positions.
 */
export function lines(text) {
  const out = [];
  let start = 0;
  let value = '';
  for (const ch of points(text)) {
    if (ch === '\n') {
      out.push({ start, value });
      start += pointLength(value) + 1;
      value = '';
      continue;
    }
    value += ch;
  }
  out.push({ start, value });
  return out;
}

/** The line and column a code-point offset falls on, both zero-based. */
export function toLineColumn(lineIndex, offset) {
  // A binary search, because a caret moving through a large document should
  // not cost a walk of it.
  let lo = 0;
  let hi = lineIndex.length - 1;
  while (lo < hi) {
    const mid = (lo + hi + 1) >> 1;
    if (lineIndex[mid].start <= offset) lo = mid;
    else hi = mid - 1;
  }
  const line = lineIndex[lo];
  return { line: lo, column: Math.max(0, offset - line.start) };
}

/** The offset of a line and column, clamped to the document. */
export function toOffsetFromLineColumn(lineIndex, line, column) {
  const at = Math.max(0, Math.min(line, lineIndex.length - 1));
  const target = lineIndex[at];
  return target.start + Math.max(0, Math.min(column, pointLength(target.value)));
}

/**
 * A change to the document, in code points: `remove` characters at `at`, then
 * insert `insert`.
 */
export function changeBetween(before, after) {
  const a = points(before);
  const b = points(after);
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
 * Moves an offset across a change somebody else made.
 *
 * This is what keeps a caret — yours or a peer's — pointing at the same place
 * in the text while the text moves underneath it. An edit above the caret
 * shifts it, an edit below leaves it alone, and an edit that spans it pins it
 * to the end of the replacement, since what it pointed at is gone.
 *
 * A peer's caret needs this as much as your own: presence arrives
 * asynchronously, so between one announcement and the next, everything
 * anybody types has to be applied to the position they last told you about.
 */
export function shift(offset, change) {
  if (offset <= change.at) return offset;
  const inserted = pointLength(change.insert);
  if (offset >= change.at + change.remove) return offset - change.remove + inserted;
  return change.at + inserted;
}

/** Moves a selection across a change. */
export function shiftSelection(selection, change) {
  return { anchor: shift(selection.anchor, change), head: shift(selection.head, change) };
}

/** A selection's bounds, low first — a selection may run backwards. */
export function bounds(selection) {
  const { anchor, head } = selection;
  return anchor <= head ? { from: anchor, to: head } : { from: head, to: anchor };
}

export function isEmpty(selection) {
  return selection.anchor === selection.head;
}

/**
 * The visible span of one line covered by a selection, or null when the line
 * is untouched. Used to draw a peer's selection band across the lines it
 * spans.
 */
export function lineSpan(lineIndex, line, selection) {
  const { from, to } = bounds(selection);
  if (from === to) return null;
  const info = lineIndex[line];
  if (!info) return null;
  const lineStart = info.start;
  const lineEnd = lineStart + pointLength(info.value);
  if (to < lineStart || from > lineEnd) return null;
  const span = {
    from: Math.max(from, lineStart) - lineStart,
    to: Math.min(to, lineEnd) - lineStart,
    // Whether the selection continues past this line, so the band can be
    // drawn through the newline rather than stopping at the last character.
    trailing: to > lineEnd,
  };
  // A selection ending exactly where a line begins touches that line without
  // covering any of it; returning a zero-width span there paints a sliver at
  // its left edge. The trailing case is the one exception — a band drawn
  // through a swallowed newline is meant to have no characters under it.
  if (span.from === span.to && !span.trailing) return null;
  return span;
}
