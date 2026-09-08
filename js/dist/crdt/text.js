/**
 * The sequence CRDT: runs of text, ordered by what they follow.
 *
 * This is a port of the Go implementation's algorithm, and it has to be an
 * exact one. Two replicas that order runs differently produce no error — they
 * simply hold different text, forever — so every rule here (the anchor
 * traversal, the tie-break by identifier, where a merge splits a run) matches
 * `crdt/text.go` deliberately, and the conformance corpus is what proves it.
 */
import * as hlc from "./hlc.js";
/** How long a single run may grow before edits stop being merged into it. */
const MAX_RUN_RUNES = 4096;
/**
 * Characters are counted in **code points**, not UTF-16 units: an astral
 * character is one character, and a replica that counted it as two would
 * place every later edit in the wrong position.
 */
export function runes(s) {
    return [...s];
}
export function runeCount(run) {
    return run.n !== 0 ? run.n : runes(run.value).length;
}
function runeSlice(s, from, to) {
    return runes(s).slice(from, to).join('');
}
function withValue(run, value) {
    const out = { id: run.id, value, n: runes(value).length };
    if (run.prev !== undefined)
        out.prev = run.prev;
    if (run.deleted)
        out.deleted = true;
    return out;
}
/** Splits a run at an offset, giving the tail the identifier it must have. */
export function splitRun(run, offset) {
    const all = runes(run.value);
    const left = withValue(run, all.slice(0, offset).join(''));
    const right = withValue(run, all.slice(offset).join(''));
    right.id = hlc.plus(run.id, offset);
    right.prev = hlc.plus(run.id, offset - 1);
    return [left, right];
}
/** The text of a document, skipping tombstones. */
export function toString(text) {
    let out = '';
    for (const run of text)
        if (!run.deleted)
            out += run.value;
    return out;
}
/** The number of visible characters. */
export function length(text) {
    let n = 0;
    for (const run of text)
        if (!run.deleted)
            n += runeCount(run);
    return n;
}
/**
 * Puts runs in document order.
 *
 * Runs are grouped by the character they follow; within a group the newest
 * identifier comes first, which is the tie-break that decides between two
 * people typing at the same place. The walk then visits a run and, before
 * moving on, everything anchored to each of its characters in turn.
 */
export function ordered(text) {
    if (text.length <= 1)
        return text;
    if (inSequence(text))
        return text;
    const children = new Map();
    for (const run of text) {
        const anchor = hlc.key(run.prev ?? hlc.ZERO);
        const group = children.get(anchor);
        if (group)
            group.push(run);
        else
            children.set(anchor, [run]);
    }
    for (const group of children.values()) {
        group.sort((a, b) => hlc.compare(b.id, a.id));
    }
    const result = [];
    const seen = new Set();
    const stack = [];
    const push = (anchor) => {
        const group = children.get(hlc.key(anchor));
        if (!group)
            return;
        // Pushed in reverse so the first of the group is popped first.
        for (let i = group.length - 1; i >= 0; i--)
            stack.push(group[i]);
    };
    push(hlc.ZERO);
    while (stack.length > 0) {
        const run = stack.pop();
        const id = hlc.key(run.id);
        if (seen.has(id))
            continue;
        seen.add(id);
        result.push(run);
        // Only characters something is anchored to are worth probing; walking
        // every character of every run costs a lookup per character of the
        // document, which dominates once runs are long.
        const n = runeCount(run);
        for (let i = n - 1; i >= 0; i--) {
            const charID = hlc.plus(run.id, i);
            if (children.has(hlc.key(charID)))
                push(charID);
        }
    }
    // A run whose anchor never arrived would otherwise be dropped.
    if (result.length < text.length) {
        for (const run of text) {
            const id = hlc.key(run.id);
            if (!seen.has(id)) {
                seen.add(id);
                result.push(run);
            }
        }
    }
    return result;
}
/** Reports whether the runs are already in document order — the common case. */
function inSequence(text) {
    // The virtual root: one character, the zero identifier, which the first run
    // anchors to.
    const stack = [{ base: hlc.ZERO, n: 1, cursor: 0 }];
    for (const run of text) {
        for (;;) {
            const top = stack[stack.length - 1];
            const prev = run.prev ?? hlc.ZERO;
            const hosts = prev.n === top.base.n && prev.w === top.base.w
                ? prev.l - top.base.l
                : -1;
            const within = hosts >= 0 && hosts < top.n;
            if (within && hosts >= top.cursor) {
                if (hosts > top.cursor) {
                    top.cursor = hosts;
                    top.lastSib = undefined;
                }
                if (top.lastSib !== undefined && !hlc.after(top.lastSib, run.id)) {
                    return false; // siblings out of order
                }
                top.lastSib = run.id;
                break;
            }
            if (stack.length === 1)
                return false; // anchored somewhere the walk would not be
            stack.pop();
        }
        stack.push({ base: run.id, n: runeCount(run), cursor: 0 });
    }
    return true;
}
function canMerge(last, curr) {
    if (Boolean(curr.deleted) !== Boolean(last.deleted))
        return false;
    if (runeCount(last) + runeCount(curr) > MAX_RUN_RUNES)
        return false;
    const expectedID = hlc.plus(last.id, runeCount(last));
    const expectedPrev = hlc.plus(last.id, runeCount(last) - 1);
    return (hlc.equal(curr.id, expectedID) &&
        curr.prev !== undefined &&
        hlc.equal(curr.prev, expectedPrev));
}
/** Joins runs that are consecutive in both identifier and position. */
export function mergeAdjacent(text) {
    if (text.length <= 1)
        return text;
    const result = [{ ...text[0] }];
    for (let i = 1; i < text.length; i++) {
        const last = result[result.length - 1];
        const curr = text[i];
        if (canMerge(last, curr)) {
            // Counts are taken before the values are joined: afterwards the cached
            // count on `last` no longer describes what it holds.
            const joined = runeCount(last) + runeCount(curr);
            last.value += curr.value;
            last.n = joined;
        }
        else {
            result.push({ ...curr });
        }
    }
    return result;
}
export function normalize(text) {
    return mergeAdjacent(ordered(text));
}
/** The identifier of the character at a visible position. */
function idAt(text, pos) {
    if (pos < 0)
        return hlc.ZERO;
    let at = 0;
    for (const run of text) {
        if (run.deleted)
            continue;
        const n = runeCount(run);
        if (pos >= at && pos < at + n)
            return hlc.plus(run.id, pos - at);
        at += n;
    }
    return hlc.ZERO;
}
/** Divides whichever run straddles a visible position. */
function splitAt(text, pos) {
    if (pos <= 0)
        return text;
    let at = 0;
    for (let i = 0; i < text.length; i++) {
        const run = text[i];
        if (run.deleted)
            continue;
        const n = runeCount(run);
        if (pos > at && pos < at + n) {
            const [left, right] = splitRun(run, pos - at);
            return [...text.slice(0, i), left, right, ...text.slice(i + 1)];
        }
        at += n;
    }
    return text;
}
/** Inserts text at a visible position, allocating identifiers from clock. */
export function insert(text, pos, value, clock) {
    if (value === '')
        return text;
    // Ordered once and reused: locating the anchor and splitting both need
    // document order, and deriving it twice per keystroke is what makes typing
    // cost more as the document grows.
    const inOrder = ordered(text);
    const prevID = idAt(inOrder, pos - 1);
    const result = [...splitAt(inOrder, pos)];
    const chars = runes(value);
    const run = {
        id: clock.reserveSequence(chars.length),
        value,
        n: chars.length,
    };
    if (!hlc.isZero(prevID))
        run.prev = prevID;
    // Place the run where it belongs rather than appending it: it has the
    // newest identifier, so among everything anchored to prevID it sorts first,
    // which is exactly where this splices it. Keeping document order lets the
    // ordering pass take its fast path.
    let at = result.length;
    if (hlc.isZero(prevID)) {
        at = 0;
    }
    else {
        for (let i = 0; i < result.length; i++) {
            const last = hlc.plus(result[i].id, runeCount(result[i]) - 1);
            if (hlc.equal(last, prevID)) {
                at = i + 1;
                break;
            }
        }
    }
    result.splice(at, 0, run);
    return mergeAdjacent(result);
}
/** Marks a visible range deleted, dividing runs at its edges. */
export function remove(text, pos, count) {
    if (count <= 0)
        return text;
    const inOrder = [...splitAt(splitAt(ordered(text), pos), pos + count)].map((r) => ({ ...r }));
    let at = 0;
    for (const run of inOrder) {
        if (run.deleted)
            continue;
        const n = runeCount(run);
        if (at >= pos && at + n <= pos + count)
            run.deleted = true;
        at += n;
    }
    // Only flags changed, so the order still holds.
    return mergeAdjacent(inOrder);
}
/**
 * Combines two sets of runs.
 *
 * Runs from either side may cover overlapping stretches of the same origin,
 * so both are cut at every boundary either side knows about, and the pieces
 * are then keyed by identifier. A run anchored partway into another forces a
 * cut there too: without it the anchor sits inside a run, the run is emitted
 * whole, and everything anchored to a character in its middle lands after its
 * end instead — at which point the two replicas order the document
 * differently.
 */
export function mergeRuns(a, b) {
    const all = [...a, ...b];
    const splits = new Map();
    const baseKey = (id) => `${id.n} ${id.w}`;
    const addSplit = (id, at) => {
        const k = baseKey(id);
        const set = splits.get(k);
        if (set)
            set.add(at);
        else
            splits.set(k, new Set([at]));
    };
    for (const run of all) {
        addSplit(run.id, run.id.l);
        addSplit(run.id, run.id.l + runeCount(run));
        if (run.prev !== undefined && !hlc.isZero(run.prev)) {
            addSplit(run.prev, run.prev.l + 1);
        }
    }
    const combined = new Map();
    const take = (id, candidate) => {
        const k = hlc.key(id);
        const existing = combined.get(k);
        if (existing) {
            // The same characters seen twice: keep one, but a deletion anywhere is
            // a deletion everywhere.
            if (candidate.deleted)
                existing.deleted = true;
            return;
        }
        combined.set(k, candidate);
    };
    for (const run of all) {
        const relevant = [...(splits.get(baseKey(run.id)) ?? [])]
            .filter((s) => s > run.id.l && s < run.id.l + runeCount(run))
            .sort((x, y) => x - y);
        let currentLogical = run.id.l;
        let currentValue = run.value;
        let currentPrev = run.prev;
        for (const s of relevant) {
            const offset = s - currentLogical;
            const id = { w: run.id.w, l: currentLogical, n: run.id.n };
            const piece = withValue(run, runeSlice(currentValue, 0, offset));
            piece.id = id;
            if (currentPrev !== undefined)
                piece.prev = currentPrev;
            else
                delete piece.prev;
            take(id, piece);
            currentPrev = hlc.plus(id, offset - 1);
            currentValue = runeSlice(currentValue, offset, runes(currentValue).length);
            currentLogical = s;
        }
        const id = { w: run.id.w, l: currentLogical, n: run.id.n };
        const piece = withValue(run, currentValue);
        piece.id = id;
        if (currentPrev !== undefined)
            piece.prev = currentPrev;
        else
            delete piece.prev;
        take(id, piece);
    }
    return normalize([...combined.values()]);
}
/** Applies deletion ranges, dividing a run when only part of it was deleted. */
export function markDeleted(text, ranges) {
    if (ranges.length === 0)
        return text;
    const contains = (r, id) => {
        if (id.n !== r.id.n || id.w !== r.id.w)
            return false;
        const off = id.l - r.id.l;
        return off >= 0 && off < r.n;
    };
    const out = [];
    for (const run of text) {
        if (run.deleted) {
            out.push(run);
            continue;
        }
        const n = runeCount(run);
        const deleted = [];
        let touched = false;
        for (let i = 0; i < n; i++) {
            const id = hlc.plus(run.id, i);
            const hit = ranges.some((r) => contains(r, id));
            deleted.push(hit);
            if (hit)
                touched = true;
        }
        if (!touched) {
            out.push(run);
            continue;
        }
        let start = 0;
        for (let i = 1; i <= n; i++) {
            if (i === n || deleted[i] !== deleted[start]) {
                let piece = run;
                if (start > 0)
                    piece = splitRun(run, start)[1];
                if (i < n)
                    piece = splitRun(piece, i - start)[0];
                piece = { ...piece };
                if (deleted[start])
                    piece.deleted = true;
                else
                    delete piece.deleted;
                out.push(piece);
                start = i;
            }
        }
    }
    return mergeAdjacent(out);
}
