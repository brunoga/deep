// Text arithmetic for the notes editor.
//
// A textarea reports its whole value and speaks UTF-16; the CRDT takes single
// edits and counts code points. Everything needed to translate between the
// two lives here, free of imports, so it can be tested on its own.

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
 *
 * Positions here are code-point counts, matching `changeBetween`. A textarea
 * speaks UTF-16, so callers convert on the way in and out; doing the
 * arithmetic in UTF-16 units instead would shift the cursor by one for every
 * emoji above it.
 */
export function shiftCursor(cursor, change) {
  if (cursor <= change.at) return cursor;
  const insertLength = [...change.insert].length;
  if (cursor >= change.at + change.remove) {
    return cursor - change.remove + insertLength;
  }
  return change.at + insertLength;
}

/** A UTF-16 index into `s`, as a count of code points. */
export function toCodePoints(s, index) {
  return [...s.slice(0, index)].length;
}

/** The inverse: a code-point count, as a UTF-16 index. */
export function toUTF16(s, count) {
  return [...s].slice(0, count).join('').length;
}
