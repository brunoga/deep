// commandpost in the browser.
//
// The page never sends an incident. It sends *patches* — the same JSON the Go
// CLI sends, carrying the same conditions — and applies the patches it gets
// back to its own copy. That is the whole point of the exercise: the server
// cannot tell the two clients apart.
import { applyPatch, diff } from '@brunoga/deep-patch';
import * as patches from './patches.js';
import { joinNotes } from './notes.js';

const api = {
  async get(path) {
    const res = await fetch(path, { headers: author() });
    if (!res.ok) throw new Error(`${res.status} ${(await res.text()).trim()}`);
    return res.json();
  },
  async post(path, body) {
    const res = await fetch(path, {
      method: 'POST',
      headers: { 'content-type': 'application/json', ...author() },
      body: JSON.stringify(body),
    });
    const text = await res.text();
    let parsed;
    try {
      parsed = JSON.parse(text);
    } catch {
      parsed = { error: text.trim() };
    }
    return { ok: res.ok, status: res.status, body: parsed };
  },
};

function author() {
  const headers = { 'X-Author': document.getElementById('who').value || 'web' };
  const token = document.getElementById('token').value;
  if (token) headers['Authorization'] = `Bearer ${token}`;
  return headers;
}

const state = {
  incidents: [],
  selected: null,
  /** The incident as the server last showed it to us. */
  shadow: null,
  /** The notes room for the selected incident, once joined. */
  notes: null,
};

// ── rendering ───────────────────────────────────────────────────────────────

const el = (id) => document.getElementById(id);

function renderList() {
  el('incidents').replaceChildren(
    ...state.incidents.map((inc) => {
      const li = document.createElement('li');
      li.setAttribute('aria-current', String(inc.id === state.selected));
      li.dataset.id = inc.id;
      const sev = document.createElement('span');
      sev.className = `sev sev${inc.severity}`;
      sev.textContent = `SEV${inc.severity}`;
      li.append(sev, document.createTextNode(inc.title));
      li.onclick = () => select(inc.id);
      return li;
    }),
  );
}

function renderDetail() {
  const inc = state.shadow;
  el('detail-pane').hidden = inc === null;
  if (!inc) return;

  el('title').textContent = inc.title;
  const bits = [`SEV${inc.severity}`, inc.status];
  if (inc.commander) bits.push(`IC ${inc.commander}`);
  el('summary').textContent = bits.join(' · ');
  el('tasks').replaceChildren(
    ...(inc.tasks ?? []).map((task) => {
      const li = document.createElement('li');
      const label = document.createElement('span');
      label.textContent = task.text;
      if (task.done) label.className = 'done';

      const claim = document.createElement('button');
      claim.textContent = task.owner ? '' : 'claim';
      claim.hidden = Boolean(task.owner);
      claim.onclick = () => send(patches.claimTask(task.id, author()['X-Author']));

      const done = document.createElement('button');
      done.textContent = 'done';
      done.hidden = task.done;
      done.onclick = () => send(patches.completeTask(task.id));

      li.append(label);
      if (task.owner) {
        const owner = document.createElement('span');
        owner.className = 'owner';
        owner.textContent = `@${task.owner}`;
        li.append(owner);
      }
      li.append(claim, done);
      return li;
    }),
  );
}

function showWire(patch, result) {
  el('wire').textContent = JSON.stringify(patch, null, 2);
  const out = el('outcome');
  if (!result) {
    out.textContent = '';
    return;
  }
  if (result.ok) {
    const { applied = 0, skipped = 0, failed = 0, seq } = result.body;
    // A skip is not a failure. It is the server saying the condition met
    // reality — somebody else got there first — which is a thing to show the
    // user, not an error to retry.
    const parts = [];
    if (applied) parts.push(`<span class="applied">${applied} applied</span>`);
    // A skip means the operation's condition met reality: the task was
    // already claimed, the severity was already worse. Worth showing, and not
    // an error.
    if (skipped) parts.push(`<span class="skipped">${skipped} skipped — condition not met</span>`);
    if (failed) parts.push(`<span class="failed">${failed} failed</span>`);
    if (!parts.length) parts.push('no change');
    out.innerHTML = parts.join(' · ') + (seq ? ` · audit entry #${seq}` : '');
    return;
  }
  out.innerHTML = `<span class="failed">${result.status}: ${result.body.error ?? 'refused'}</span>`;
}

// ── actions ─────────────────────────────────────────────────────────────────

async function send(patch) {
  if (!state.selected) return;
  const result = await api.post(`/incidents/${state.selected}/patch`, patch);
  showWire(patch, result);
  await refresh();
}

async function select(id) {
  if (state.notes) {
    state.notes.leave();
    state.notes = null;
  }
  state.selected = id;
  state.shadow = await api.get(`/incidents/${id}`);
  renderList();
  renderDetail();

  // The notes are not part of the incident record: they are a CRDT document
  // in a room of their own, which this page joins as a peer of the Go
  // clients rather than as a reader of the server's copy.
  const notes = el('notes');
  notes.disabled = true;
  notes.value = '';
  const token = el('token').value;
  const url =
    `${location.origin.replace(/^http/, 'ws')}/ws?room=${encodeURIComponent(id)}` +
    (token ? `&token=${encodeURIComponent(token)}` : '');
  try {
    state.notes = await joinNotes({
      url,
      node: author()['X-Author'],
      textarea: notes,
      onPeers: (peers) => {
        el('peers').textContent = peers.length > 1 ? `with ${peers.filter((p) => p !== author()['X-Author']).join(', ')}` : '';
      },
      onStatus: (text) => {
        el('status').textContent = text;
      },
    });
  } catch (err) {
    notes.placeholder = `notes unavailable: ${err.message}`;
  }
}

/**
 * Refresh by *patch*, not by replacement: fetch the server's copy, diff it
 * against ours, and apply the difference. The page ends up in the same place
 * either way — but this way the diff is visible, and it is the same
 * operation the Go clients perform on the same data.
 */
async function refresh() {
  const incidents = await api.get('/incidents');
  state.incidents = incidents;
  if (state.selected) {
    const fresh = incidents.find((i) => i.id === state.selected);
    if (fresh && state.shadow) {
      const change = diff(state.shadow, fresh, { keys: patches.keys });
      if (change.ops?.length) {
        applyPatch(state.shadow, change, { keys: patches.keys });
      }
    } else if (fresh) {
      state.shadow = fresh;
    }
  }
  renderList();
  renderDetail();
}

el('controls').onclick = (e) => {
  const button = e.target.closest('button');
  if (!button) return;
  const { act, arg } = button.dataset;
  if (act === 'escalate') send(patches.escalate(Number(arg)));
  if (act === 'status') send(patches.setStatus(arg));
  if (act === 'close') send(patches.close());
};

async function poll() {
  try {
    await refresh();
    el('status').textContent = `in sync · ${new Date().toLocaleTimeString()}`;
  } catch (err) {
    el('status').textContent = `offline: ${err.message}`;
  }
  setTimeout(poll, 2000);
}

poll();
