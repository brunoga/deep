// Wiring: a document from the room, an editor over it, a view under it.
//
// Everything specific to collaboration is in three places — the room supplies
// the document, `remoteChanged` moves everybody's caret when text arrives,
// and presence carries selections both ways. The rest is an editor.
import { connect } from '@brunoga/deep-patch';
import { Editor, LocalDocument, colorFor, commandFor } from './src/editor.js';
import { View } from './src/view.js';

const el = (id) => document.getElementById(id);

/**
 * This tab's identity in a room.
 *
 * The display name is whatever the user typed; the node id has to be unique,
 * because presence is keyed by it and so are the characters the CRDT
 * allocates. Two tabs sharing one would show as a single peer and write
 * identifiers that collide.
 */
const nodeID = `${Math.random().toString(36).slice(2, 10)}`;

const state = {
  name: null,
  session: null,
  editor: null,
  view: null,
  // Which document opening is the current one. Opening is asynchronous — a
  // socket has to come up — and a click on another document while the first
  // is still connecting must not end with two live sessions, one of them
  // unreachable and still announcing into a room nobody is looking at.
  generation: 0,
};

el('who').value = `guest-${nodeID.slice(0, 3)}`;

// ── documents ───────────────────────────────────────────────────────────

async function listDocuments() {
  const res = await fetch('/documents');
  if (!res.ok) return [];
  return res.json();
}

async function renderDocuments() {
  const docs = await listDocuments();
  el('documents').replaceChildren(
    ...docs.map((doc) => {
      const li = document.createElement('li');
      li.setAttribute('aria-current', String(doc.name === state.name));
      li.append(doc.name);
      const meta = document.createElement('span');
      meta.className = doc.live ? 'meta live' : 'meta';
      meta.textContent = `${doc.lines}L`;
      li.append(meta);
      li.onclick = () => open(doc.name);
      return li;
    }),
  );
}

/**
 * The listing, as it changes.
 *
 * A document somebody else creates should appear here without a reload, and
 * one that somebody opens should show as live. The stream carries no data —
 * it says "look again" — so there is one path for what documents exist rather
 * than two that can disagree. EventSource reconnects on its own.
 */
function watchDocuments() {
  const events = new EventSource('/documents/events');
  events.onmessage = () => renderDocuments();
}

el('new-doc').onsubmit = async (e) => {
  e.preventDefault();
  const name = el('new-name').value.trim();
  if (!name) return;
  const res = await fetch('/documents', {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ name }),
  });
  if (!res.ok) {
    el('status').textContent = (await res.text()).trim();
    return;
  }
  el('new-name').value = '';
  await renderDocuments();
  open(name);
};

// ── the editing session ─────────────────────────────────────────────────

async function open(name) {
  const generation = ++state.generation;
  state.session?.leave();
  state.session = null;
  state.name = name;
  el('doc-name').textContent = name;
  el('status').textContent = 'connecting';
  history.replaceState(null, '', `?doc=${encodeURIComponent(name)}`);

  // The editor starts on a local document, so the page is usable — and
  // visibly *not* connected — before the socket is up. Whatever is typed in
  // the meantime goes into the room when it arrives.
  const editor = new Editor(new LocalDocument(''));
  state.editor = editor;
  state.view = new View(el('editor'), editor);
  state.view.measure();
  const view = state.view;
  editor.onChange(() => view.render());
  // Scrolling follows *your* caret only. Hanging it off every change would
  // mean a peer's heartbeat yanking the page back while you read.
  editor.onLocalChange(() => view.revealCaret(editor.lineIndex));
  view.render();

  let session = null;
  try {
    session = await joinRoom(name, editor);
  } catch (err) {
    if (generation === state.generation) el('status').textContent = `offline: ${err.message}`;
  }
  if (generation !== state.generation) {
    // Another document was opened while this one was connecting. This
    // session is nobody's now: close it rather than leave it announcing.
    session?.leave();
    return;
  }
  if (session) {
    state.session = session;
    el('status').textContent = 'connected';
  }
  await renderDocuments();
}

/** Connects the editor to a room, in both directions. */
async function joinRoom(name, editor) {
  const url = `${location.origin.replace(/^http/, 'ws')}/ws?room=${encodeURIComponent(name)}`;
  // A generous presence timeout on purpose: browsers throttle timers in
  // background tabs to about once a minute, so a peer reading another window
  // would otherwise be declared gone while they sit there watching.
  const room = await connect({ url, node: nodeID, presenceTtlMs: 90_000 });

  let stale = false;

  // The room becomes the editor's document, carrying anything typed while it
  // was connecting. `insert` and `delete` publish as they go, so nothing else
  // has to remember to send.
  editor.attach(room);

  room.onUpdate(() => {
    if (stale) return;
    // Somebody else typed: move every caret — this editor's and each peer's
    // — through the change before drawing.
    editor.remoteChanged();
  });

  room.awareness.onChange(() => {
    if (stale) return;
    const present = room.awareness.states();
    for (const [node, presence] of present) {
      if (node === nodeID) continue;
      editor.setPeer(node, {
        name: presence?.name ?? node,
        color: colorFor(node),
        selection: presence?.selection,
      });
    }
    editor.keepPeers(new Set([...present.keys()]));
    renderPeers(editor);
  });

  room.onClose((reason) => {
    if (stale) return;
    el('status').textContent = `disconnected: ${reason}`;
  });

  const announce = () => {
    if (stale) return;
    try {
      room.announce({ name: el('who').value || nodeID, ...editor.presence() });
    } catch {
      // The socket went away between connecting and saying hello, or between
      // heartbeats. onClose has already reported it; there is nobody to tell.
    }
  };
  // Presence is also the heartbeat: a client that stops announcing stops
  // being drawn by everyone else.
  const beat = setInterval(announce, 8_000);
  // Only *local* changes announce. Announcing on every change would mean a
  // peer's announcement arriving, moving their caret, notifying, and
  // announcing straight back at them — two clients talking about nothing.
  const unsubscribe = editor.onLocalChange(announce);
  // A tab returning to the foreground has had its timers throttled and may
  // look absent to everyone else; say hello again immediately.
  const onVisible = () => {
    if (document.visibilityState === 'visible') announce();
  };
  document.addEventListener('visibilitychange', onVisible);
  announce();

  return {
    room,
    announce,
    leave() {
      stale = true;
      clearInterval(beat);
      unsubscribe();
      document.removeEventListener('visibilitychange', onVisible);
      room.close();
    },
  };
}

function renderPeers(editor) {
  const names = [...editor.peers.values()].map((p) => p.name).sort();
  el('peers').textContent = names.length ? `with ${names.join(', ')}` : '';
}

// ── input ───────────────────────────────────────────────────────────────

const input = el('input');

// Focus follows clicks anywhere in the editor, and the hidden textarea is
// what actually receives them — the browser's own input machinery (IME,
// dictation, mobile keyboards) keeps working while the visible text is drawn
// by hand.
el('editor').addEventListener('mousedown', (e) => {
  if (!state.editor) return;
  const offset = state.view.offsetAt(state.editor.lineIndex, e.clientX, e.clientY);
  state.editor.moveTo(offset, e.shiftKey);
  input.focus();
  e.preventDefault();

  const onMove = (move) => {
    const to = state.view.offsetAt(state.editor.lineIndex, move.clientX, move.clientY);
    state.editor.moveTo(to, true);
  };
  const onUp = () => {
    window.removeEventListener('mousemove', onMove);
    window.removeEventListener('mouseup', onUp);
  };
  window.addEventListener('mousemove', onMove);
  window.addEventListener('mouseup', onUp);
});

input.addEventListener('keydown', (e) => {
  if (!state.editor) return;
  const command = commandFor(e);
  if (command) {
    command(state.editor);
    e.preventDefault();
  }
});

// Ordinary typing arrives as input events rather than keydowns, which is what
// makes composed characters and pasted text work without special cases.
//
// Composition is the case that needs care. A Japanese IME, or dictation, or a
// phone's autocorrect, fires an input event for every intermediate state of a
// word that is still being composed; committing each of them types
// half-formed text into the document *and* destroys the composition. So input
// is ignored while one is in flight, and the finished text is taken when it
// ends — Chrome and Safari deliver a trailing input event, Firefox does not,
// which is why compositionend takes what is there rather than trusting one.
let composing = false;

input.addEventListener('compositionstart', () => {
  composing = true;
});

input.addEventListener('compositionend', () => {
  composing = false;
  takeInput();
});

input.addEventListener('input', (e) => {
  if (composing || e.isComposing) return;
  takeInput(e.inputType);
});

function takeInput(inputType) {
  if (!state.editor) return;
  const typed = input.value;
  input.value = '';
  if (typed !== '') {
    state.editor.type(typed);
    return;
  }
  // Some soft keyboards send deletions as input events with nothing in them
  // rather than as key events; without this, backspace does nothing at all on
  // an Android phone.
  if (inputType === 'deleteContentBackward') state.editor.backspace();
  else if (inputType === 'deleteContentForward') state.editor.deleteForward();
}

input.addEventListener('paste', (e) => {
  if (!state.editor) return;
  const text = e.clipboardData?.getData('text/plain');
  if (text) {
    state.editor.type(text);
    e.preventDefault();
  }
});

input.addEventListener('copy', (e) => {
  if (!state.editor) return;
  e.clipboardData?.setData('text/plain', state.editor.selectedText());
  e.preventDefault();
});

el('who').addEventListener('input', () => {
  // A rename is a presence change, not a document one — and one worth sending
  // straight away rather than at the next heartbeat, since the point of
  // typing your name is that other people see it.
  state.editor?.changed();
  state.session?.announce();
});

// ── start ───────────────────────────────────────────────────────────────

const wanted = new URLSearchParams(location.search).get('doc');
await renderDocuments();
watchDocuments();
open(wanted && /^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$/.test(wanted) ? wanted : 'welcome');
