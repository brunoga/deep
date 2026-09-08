import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  bounds,
  changeBetween,
  lineSpan,
  lines,
  pointLength,
  shift,
  shiftSelection,
  toLineColumn,
  toOffset,
  toOffsetFromLineColumn,
  toUTF16,
} from '../src/positions.js';

// The three coordinate systems, and the conversions between them. Confusing
// them is the source of most editor bugs, and the emoji cases are where a
// wrong one shows.

test('code points and UTF-16 indices convert both ways', () => {
  const text = 'a🙂b';
  assert.equal(text.length, 4, 'the emoji is two UTF-16 units');
  assert.equal(pointLength(text), 3, 'and one code point');

  assert.equal(toOffset(text, 0), 0);
  assert.equal(toOffset(text, 1), 1);
  assert.equal(toOffset(text, 3), 2, 'past the emoji');
  assert.equal(toOffset(text, 4), 3);

  assert.equal(toUTF16(text, 2), 3);
  for (let i = 0; i <= pointLength(text); i++) {
    assert.equal(toOffset(text, toUTF16(text, i)), i, 'the conversions must round-trip');
  }
});

test('lines carry their starting offset', () => {
  const index = lines('one\ntwo\n\nfour');
  assert.deepEqual(
    index.map((l) => [l.start, l.value]),
    [
      [0, 'one'],
      [4, 'two'],
      [8, ''],
      [9, 'four'],
    ],
  );
});

test('lines are counted in code points, so an emoji does not shift them', () => {
  const index = lines('🙂🙂\nnext');
  assert.equal(index[1].start, 3, 'two emoji and a newline are three code points');
});

test('offsets map to line and column, and back', () => {
  const text = 'one\ntwo\nthree';
  const index = lines(text);

  assert.deepEqual(toLineColumn(index, 0), { line: 0, column: 0 });
  assert.deepEqual(toLineColumn(index, 3), { line: 0, column: 3 }, 'end of the first line');
  assert.deepEqual(toLineColumn(index, 4), { line: 1, column: 0 }, 'start of the second');
  assert.deepEqual(toLineColumn(index, 9), { line: 2, column: 1 });

  for (let offset = 0; offset <= pointLength(text); offset++) {
    const { line, column } = toLineColumn(index, offset);
    assert.equal(toOffsetFromLineColumn(index, line, column), offset, `round trip at ${offset}`);
  }
});

test('a line and column beyond the document clamps rather than throwing', () => {
  const index = lines('one\ntwo');
  assert.equal(toOffsetFromLineColumn(index, 99, 0), 4, 'past the last line');
  assert.equal(toOffsetFromLineColumn(index, 0, 99), 3, 'past the end of a line');
  assert.equal(toOffsetFromLineColumn(index, -1, -1), 0);
});

test('the change between two texts is the smallest one', () => {
  assert.deepEqual(changeBetween('hello', 'hello!'), { at: 5, remove: 0, insert: '!' });
  assert.deepEqual(changeBetween('hello', 'hell'), { at: 4, remove: 1, insert: '' });
  assert.deepEqual(changeBetween('abc', 'axc'), { at: 1, remove: 1, insert: 'x' });
  assert.deepEqual(changeBetween('🙂', '🙂!'), { at: 1, remove: 0, insert: '!' });
});

// A caret has to survive other people typing. This is the rule that makes a
// remote edit feel like somebody else typing rather than the document
// jumping under you.
test('a caret moves only when the change is above it', () => {
  const above = { at: 0, remove: 0, insert: 'xyz' };
  assert.equal(shift(10, above), 13);

  const below = { at: 20, remove: 0, insert: 'xyz' };
  assert.equal(shift(10, below), 10);

  const deletedAbove = { at: 0, remove: 4, insert: '' };
  assert.equal(shift(10, deletedAbove), 6);

  // A change that spans the caret pins it to the end of the replacement:
  // what it pointed at no longer exists.
  const spanning = { at: 5, remove: 10, insert: 'ab' };
  assert.equal(shift(10, spanning), 7);

  // A caret exactly at the edit point stays put, so typing in front of
  // somebody does not drag their caret along.
  assert.equal(shift(5, { at: 5, remove: 0, insert: 'zz' }), 5);
});

test('a selection moves as a pair, backwards or forwards', () => {
  const change = { at: 0, remove: 0, insert: 'ab' };
  assert.deepEqual(shiftSelection({ anchor: 3, head: 7 }, change), { anchor: 5, head: 9 });
  assert.deepEqual(shiftSelection({ anchor: 7, head: 3 }, change), { anchor: 9, head: 5 });
});

test('bounds order a selection however it was made', () => {
  assert.deepEqual(bounds({ anchor: 2, head: 8 }), { from: 2, to: 8 });
  assert.deepEqual(bounds({ anchor: 8, head: 2 }), { from: 2, to: 8 });
});

test('a selection is drawn per line, and marks where it continues', () => {
  const index = lines('one\ntwo\nthree');
  const selection = { anchor: 1, head: 10 }; // from "ne" through "th"

  assert.deepEqual(lineSpan(index, 0, selection), { from: 1, to: 3, trailing: true });
  assert.deepEqual(lineSpan(index, 1, selection), { from: 0, to: 3, trailing: true });
  assert.deepEqual(lineSpan(index, 2, selection), { from: 0, to: 2, trailing: false });

  assert.equal(lineSpan(index, 0, { anchor: 5, head: 5 }), null, 'an empty selection spans nothing');
  assert.equal(lineSpan(index, 0, { anchor: 5, head: 6 }), null, 'a line outside it is untouched');
});
