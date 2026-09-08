# editor

A collaborative text editor in the browser: several people in one document,
each other's carets and selections visible, no server arbitration.

```
   browser                      editd                       browser
   ┌────────────────┐      ┌──────────────┐      ┌────────────────┐
   │ view  ← editor │      │  room per    │      │ editor → view  │
   │        ↕       │◀════▶│  document    │◀════▶│       ↕        │
   │   CRDT replica │  ws  │  + presence  │  ws  │ CRDT replica   │
   └────────────────┘      │  + a folder  │      └────────────────┘
     carets transformed    └──────────────┘        carets transformed
     through every edit                            through every edit
```

## What it is

A working editor core — line rendering, a gutter, selection, click and drag,
keyboard movement with a goal column, copy and paste — built so that the
collaborative part is three small things rather than a rewrite:

1. **The room is the document.** `editor.doc` is a CRDT replica; `insert` and
   `delete` publish as they go.
2. **`remoteChanged()` moves every caret** — yours and each peer's — through
   an edit that arrived, so text appearing above your cursor does not drag it
   along.
3. **Presence carries a selection**, which is what draws somebody else's
   caret and highlight in your window.

Everything else in `web/src/` is editor, not collaboration. That is the point:
the hard part of a collaborative editor — that two people typing in the same
place converge — is a property of the document, not something the editor has
to arrange.

## Running it

```bash
go run ./cmd/editd
```

Open the printed address in two windows, put a different name in each, and
type in both. Or point a Go client at the same room:

```go
c, _ := deepws.Dial[any](ctx, "ws://localhost:8100/ws?room=welcome", "terminal")
c.Edit(func(d *crdt.Document) { d.Insert(0, "from Go\n") })
c.Publish(ctx)
```

The browser is a peer of that client, not a viewer of a server's copy.

## The parts worth reading

| File | What it holds |
| :--- | :--- |
| `web/src/positions.js` | The three coordinate systems — code points (what the CRDT counts), UTF-16 units (what JavaScript strings and DOM ranges use), and line/column (what a person sees) — and the conversions between them. Confusing them is the source of most editor bugs; an emoji is one of the first, two of the second. |
| `web/src/editor.js` | Selection, commands, and `remoteChanged` — the caret transformation that makes somebody else's typing feel like typing rather than the document jumping. No DOM, so it is tested directly. |
| `web/src/view.js` | Lines, gutter, carets and selection bands. Text is drawn by hand and everything else positioned over it, which is what lets a *peer's* caret appear inside the text — a textarea cannot do that at all. |
| `web/app.js` | The wiring, including the two rules learned the hard way (below). |
| `server/` | Rooms, the document listing, and a folder. Under 250 lines, because the interesting behaviour is not here. |

## Two things that only show up when you run it

**Announce on local changes only.** Presence updates arrive, move a peer's
caret, and notify the editor. If announcements hang off *every* editor change,
that notification announces straight back — and two clients end up talking to
each other about nothing, faster and faster. `onLocalChange` exists to
separate "I did something" from "something happened".

**Presence timeouts have to survive background tabs.** Browsers throttle
timers in hidden tabs to roughly once a minute, so a peer reading in another
window gets declared gone by a thirty-second timeout. The room uses ninety
seconds and re-announces when a tab becomes visible again.

## Where it would grow

The seams are deliberate. Syntax highlighting is a pass over `renderText`;
incremental rendering means touching only the lines that changed rather than
replacing them all; undo/redo has a natural shape here already, since the
CRDT's own history is what `crdt_undo_redo` builds on. Virtual scrolling
needs the line index this already keeps. None of them touch the
collaboration.

This is its own Go module, and the JavaScript comes from
[`@brunoga/deep-patch`](../../js) — the same package the incident example
uses, which is the same wire format `deep/ws` serves.
