import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Editor, LocalDocument, commandFor } from '../src/editor.js';

function editorWith(text, selection = { anchor: 0, head: 0 }) {
  const ed = new Editor(new LocalDocument(text));
  ed.selection = selection;
  return ed;
}

test('typing replaces the selection and leaves the caret after it', () => {
  const ed = editorWith('hello world', { anchor: 6, head: 11 });
  ed.type('there');
  assert.equal(ed.text, 'hello there');
  assert.deepEqual(ed.selection, { anchor: 11, head: 11 });
});

test('backspace removes the character before the caret, or the selection', () => {
  const ed = editorWith('abc', { anchor: 3, head: 3 });
  ed.backspace();
  assert.equal(ed.text, 'ab');
  assert.deepEqual(ed.selection, { anchor: 2, head: 2 });

  ed.selection = { anchor: 0, head: 2 };
  ed.backspace();
  assert.equal(ed.text, '', 'a selection is deleted whole');

  ed.backspace();
  assert.equal(ed.text, '', 'backspace at the start does nothing');
});

test('delete removes forward, and stops at the end', () => {
  const ed = editorWith('abc', { anchor: 0, head: 0 });
  ed.deleteForward();
  assert.equal(ed.text, 'bc');
  ed.selection = { anchor: 2, head: 2 };
  ed.deleteForward();
  assert.equal(ed.text, 'bc');
});

test('an emoji is one character to the caret, not two', () => {
  const ed = editorWith('a🙂b', { anchor: 2, head: 2 });
  ed.backspace();
  assert.equal(ed.text, 'ab', 'one backspace removes the whole emoji');
  assert.deepEqual(ed.selection, { anchor: 1, head: 1 });
});

test('moving left from a selection goes to its edge, not one past it', () => {
  const ed = editorWith('hello', { anchor: 1, head: 4 });
  ed.moveBy(-1);
  assert.deepEqual(ed.selection, { anchor: 1, head: 1 }, 'collapses to the low edge');

  ed.selection = { anchor: 1, head: 4 };
  ed.moveBy(1);
  assert.deepEqual(ed.selection, { anchor: 4, head: 4 }, 'and to the high edge');
});

test('shift-arrow extends from the anchor', () => {
  const ed = editorWith('hello', { anchor: 2, head: 2 });
  ed.moveBy(1, true);
  ed.moveBy(1, true);
  assert.deepEqual(ed.selection, { anchor: 2, head: 4 });
});

test('vertical movement keeps its goal column across short lines', () => {
  // Offsets: "longest line" is 0-11, its newline 12, "x" is 13, its newline
  // 14, and the last line starts at 15.
  const ed = editorWith('longest line\nx\nanother long line');
  ed.moveTo(10); // column 10 of the first line
  ed.moveLine(1);
  assert.deepEqual(ed.selection, { anchor: 14, head: 14 }, 'clamped past "x"');
  ed.moveLine(1);
  // Back out to column 10 on a line long enough to have one: the goal
  // survived the short line, which is what makes cursoring through a file
  // feel right rather than drifting left.
  assert.equal(ed.selection.head, 15 + 10, 'column 10 of the last line');
});

test('home and end move within the line', () => {
  const ed = editorWith('one\ntwo three\nfour');
  ed.moveTo(8); // inside the middle line
  ed.moveToLineEdge('start');
  assert.equal(ed.selection.head, 4);
  ed.moveToLineEdge('end');
  assert.equal(ed.selection.head, 13);
});

// The heart of it: everyone's caret has to survive everyone else's typing.
test('a remote insertion above the caret moves it, below it does not', () => {
  const doc = new LocalDocument('hello world');
  const ed = new Editor(doc);
  ed.selection = { anchor: 6, head: 11 }; // "world"
  ed.peers.set('bo', { name: 'bo', color: '#fff', selection: { anchor: 0, head: 0 } });

  doc.insert(0, '>> '); // somebody typed at the start
  ed.remoteChanged();
  assert.deepEqual(ed.selection, { anchor: 9, head: 14 }, 'the local selection moved with the text');
  assert.deepEqual(
    ed.peers.get('bo').selection,
    { anchor: 0, head: 0 },
    "a peer's caret at the edit point stays put",
  );

  doc.insert(doc.text.length, '!'); // and at the very end
  ed.remoteChanged();
  assert.deepEqual(ed.selection, { anchor: 9, head: 14 }, 'an edit below leaves it alone');
});

test('a remote deletion spanning the caret pins it to the edit', () => {
  const doc = new LocalDocument('one two three');
  const ed = new Editor(doc);
  ed.selection = { anchor: 8, head: 8 };
  doc.delete(4, 6); // removes "two th", spanning the caret
  ed.remoteChanged();
  assert.deepEqual(ed.selection, { anchor: 4, head: 4 });
});

test('peers come and go with the room', () => {
  const ed = editorWith('text');
  ed.setPeer('ana', { name: 'Ana', selection: { anchor: 1, head: 2 } });
  ed.setPeer('bo', { name: 'Bo', selection: { anchor: 0, head: 0 } });
  assert.equal(ed.peers.size, 2);
  assert.ok(ed.peers.get('ana').color, 'a peer without a colour is given one');

  ed.keepPeers(new Set(['ana']));
  assert.deepEqual([...ed.peers.keys()], ['ana']);
});

test('keys map to commands', () => {
  const ed = editorWith('hello', { anchor: 5, head: 5 });

  commandFor({ key: 'ArrowLeft', shiftKey: false })(ed);
  assert.equal(ed.selection.head, 4);

  commandFor({ key: 'Home', shiftKey: true })(ed);
  assert.deepEqual(ed.selection, { anchor: 4, head: 0 }, 'shift-home selects to the line start');

  commandFor({ key: 'Enter', shiftKey: false })(ed);
  assert.equal(ed.text, '\no', 'enter replaced the selection with a newline');

  assert.equal(commandFor({ key: 'q', shiftKey: false }), null, 'ordinary keys are not commands');
  assert.ok(commandFor({ key: 'a', ctrlKey: true }), 'but ctrl-a is');
});

// Presence announcements hang off local changes only. Announcing on every
// change would mean a peer's announcement arriving, moving their caret,
// notifying, and announcing straight back at them — two clients talking to
// each other about nothing, faster and faster.
test('only local changes are announced', () => {
  const doc = new LocalDocument('hello');
  const ed = new Editor(doc);

  let announcements = 0;
  let renders = 0;
  ed.onLocalChange(() => announcements++);
  ed.onChange(() => renders++);

  ed.type('!');
  assert.equal(announcements, 1, 'typing is worth announcing');
  ed.moveTo(0);
  assert.equal(announcements, 2, 'so is moving the caret');

  const before = announcements;
  doc.insert(0, 'remote ');
  ed.remoteChanged();
  assert.equal(announcements, before, 'somebody else typing is not this editor announcing');

  ed.setPeer('ana', { name: 'Ana', selection: { anchor: 0, head: 0 } });
  ed.keepPeers(new Set());
  assert.equal(announcements, before, "nor is a peer's caret moving");

  assert.ok(renders > announcements, 'but all of it is worth redrawing');
});

test("typing moves everybody else's caret, not just your own", () => {
  const ed = editorWith('hello world', { anchor: 0, head: 0 });
  ed.setPeer('ana', { name: 'Ana', selection: { anchor: 6, head: 11 } });

  ed.type('>>> ');

  // Ana announced her selection over "world" before this was typed; she has
  // no idea it happened, so it is this editor that has to move her.
  assert.deepEqual(ed.peers.get('ana').selection, { anchor: 10, head: 15 });
  assert.equal(ed.text, '>>> hello world');

  ed.selection = { anchor: 0, head: 0 };
  ed.deleteForward();
  assert.deepEqual(ed.peers.get('ana').selection, { anchor: 9, head: 14 });

  ed.selection = { anchor: 3, head: 3 };
  ed.backspace();
  assert.deepEqual(ed.peers.get('ana').selection, { anchor: 8, head: 13 });
});

test('replacing text a peer is sitting in pins them to the end of it', () => {
  const ed = editorWith('hello world', { anchor: 0, head: 5 });
  ed.setPeer('bo', { name: 'Bo', selection: { anchor: 2, head: 4 } });
  ed.type('goodbye');
  assert.equal(ed.text, 'goodbye world');
  assert.deepEqual(ed.peers.get('bo').selection, { anchor: 7, head: 7 }, 'what they pointed at is gone');
});

test('connecting carries text typed before the socket was up', () => {
  const ed = editorWith('', { anchor: 0, head: 0 });
  ed.type('first sentence');
  ed.setPeer('ana', { name: 'Ana', selection: { anchor: 0, head: 0 } });

  const room = new LocalDocument('');
  ed.attach(room);

  assert.equal(room.text, 'first sentence', 'the room got what was typed into the page');
  assert.equal(ed.text, 'first sentence');
  assert.deepEqual(ed.selection, { anchor: 14, head: 14 }, 'the caret stays after it');
  assert.equal(ed.peers.size, 0, 'peers belong to the room that was left');

  // And the baseline moved with it: a remote change is measured from here.
  room.insert(0, 'X');
  ed.remoteChanged();
  assert.deepEqual(ed.selection, { anchor: 15, head: 15 });
});

test('connecting to a document that already has text leaves it alone', () => {
  const ed = editorWith('', { anchor: 0, head: 0 });
  const room = new LocalDocument('already here');
  ed.attach(room);
  assert.equal(ed.text, 'already here');
  assert.deepEqual(ed.selection, { anchor: 0, head: 0 });
});

test("a repeated announcement does not drag a peer back to where they were", () => {
  const ed = editorWith('hello world', { anchor: 0, head: 0 });
  const announced = { anchor: 6, head: 11 };
  ed.setPeer('ana', { name: 'Ana', selection: announced });

  ed.type('>>> ');
  assert.deepEqual(ed.peers.get('ana').selection, { anchor: 10, head: 15 });

  // Ana's heartbeat repeats what she last said. She has not moved, so what
  // she said has already been accounted for — re-applying it would undo the
  // shift and put her highlight back over the wrong words.
  ed.setPeer('ana', { name: 'Ana', selection: { ...announced } });
  assert.deepEqual(ed.peers.get('ana').selection, { anchor: 10, head: 15 });

  // A new announcement is believed, because it was measured against the
  // document she has now.
  ed.setPeer('ana', { name: 'Ana', selection: { anchor: 0, head: 3 } });
  assert.deepEqual(ed.peers.get('ana').selection, { anchor: 0, head: 3 });
});

test('a repeated announcement that changes nothing does not redraw', () => {
  const ed = editorWith('hello', { anchor: 0, head: 0 });
  let notifications = 0;
  ed.onChange(() => notifications++);

  ed.setPeer('bo', { name: 'Bo', color: '#fff', selection: { anchor: 1, head: 2 } });
  assert.equal(notifications, 1);
  ed.setPeer('bo', { name: 'Bo', color: '#fff', selection: { anchor: 1, head: 2 } });
  assert.equal(notifications, 1, 'the heartbeat alone is not news');
  ed.setPeer('bo', { name: 'Bo Jones', color: '#fff', selection: { anchor: 1, head: 2 } });
  assert.equal(notifications, 2, 'a rename is');
});
