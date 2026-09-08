import { test } from 'node:test';
import assert from 'node:assert/strict';
import { changeBetween, shiftCursor, toCodePoints, toUTF16 } from '../textedit.js';

// A textarea reports its whole value, not what changed, so the change has to
// be recovered — and as one edit, or every keystroke would delete and
// reinsert the document under everybody else's cursor.
test('the change between two values is the smallest one', () => {
  assert.deepEqual(changeBetween('hello', 'hello world'), {
    at: 5,
    remove: 0,
    insert: ' world',
  });
  assert.deepEqual(changeBetween('hello world', 'hello'), { at: 5, remove: 6, insert: '' });
  assert.deepEqual(changeBetween('abc', 'axc'), { at: 1, remove: 1, insert: 'x' });
  assert.deepEqual(changeBetween('same', 'same'), { at: 4, remove: 0, insert: '' });
});

test('changes are measured in code points, not UTF-16 units', () => {
  // Typing after an emoji: the change starts at position 1, not 2.
  const change = changeBetween('🙂', '🙂!');
  assert.deepEqual(change, { at: 1, remove: 0, insert: '!' });
});

test('a cursor moves only when the change is above it', () => {
  const insertAbove = { at: 0, remove: 0, insert: 'xyz' };
  assert.equal(shiftCursor(5, insertAbove), 8);

  const insertBelow = { at: 10, remove: 0, insert: 'xyz' };
  assert.equal(shiftCursor(5, insertBelow), 5, 'an edit below must not move the cursor');

  const deleteAbove = { at: 0, remove: 3, insert: '' };
  assert.equal(shiftCursor(5, deleteAbove), 2);

  // A change spanning the cursor pins it to the end of the replacement.
  const spanning = { at: 2, remove: 6, insert: 'ab' };
  assert.equal(shiftCursor(5, spanning), 4);
});

// The cursor arithmetic is in code points while a textarea speaks UTF-16, so
// the conversions have to be exact or every emoji shifts the caret by one.
test('positions convert between code points and UTF-16 indices', () => {
  const text = 'a🙂b';
  assert.equal(text.length, 4, 'the emoji is two UTF-16 units');
  assert.equal([...text].length, 3, 'and one code point');

  assert.equal(toCodePoints(text, 0), 0);
  assert.equal(toCodePoints(text, 1), 1);
  assert.equal(toCodePoints(text, 3), 2, 'past the emoji');
  assert.equal(toCodePoints(text, 4), 3);

  assert.equal(toUTF16(text, 0), 0);
  assert.equal(toUTF16(text, 2), 3, 'past the emoji');
  assert.equal(toUTF16(text, 3), 4);

  for (let i = 0; i <= [...text].length; i++) {
    assert.equal(toCodePoints(text, toUTF16(text, i)), i, 'the conversions must round-trip');
  }
});
