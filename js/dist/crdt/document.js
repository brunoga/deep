/**
 * A collaborative text document: the replica a client holds, and the two
 * operations that keep it in step with its peers.
 *
 * `since(theirVector)` says what a peer is missing, and `apply(update)` folds
 * in what they sent. Nothing else is needed to converge — no central order,
 * no acknowledgements, and no cost proportional to the document when only a
 * word changed.
 */
import * as hlc from "./hlc.js";
import * as text from "./text.js";
/** How many of the run's leading characters a state vector already accounts for. */
function covers(sv, run) {
    const seen = sv.get(hlc.origin(run.id));
    if (seen === undefined)
        return 0;
    const covered = seen - run.id.l;
    if (covered < 0)
        return 0;
    const n = text.runeCount(run);
    return covered > n ? n : covered;
}
export class Document {
    runs = [];
    sv = new Map();
    clock;
    constructor(nodeID, wallTime) {
        this.clock = new hlc.Clock(nodeID, wallTime);
    }
    /** The visible text. */
    toString() {
        return text.toString(this.runs);
    }
    /** The number of visible characters, counted in code points. */
    get length() {
        return text.length(this.runs);
    }
    /** The runs, for inspection. */
    get text() {
        return this.runs;
    }
    /** Inserts at a visible position. */
    insert(pos, value) {
        this.runs = text.insert(this.runs, pos, value, this.clock);
        this.note();
    }
    /** Deletes a visible range. */
    delete(pos, count) {
        this.runs = text.remove(this.runs, pos, count);
        this.note();
    }
    /**
     * What this replica holds, as one bound per origin. It is what a peer needs
     * to work out what to send back, and it is small: one entry per writer, not
     * per character.
     */
    stateVector() {
        return new Map(this.sv);
    }
    /**
     * What a replica holding `sv` is missing. A run the peer holds entirely is
     * left out; one it holds part of is trimmed to the part it does not — which
     * is what makes syncing cost the size of the change rather than the size of
     * the document.
     */
    since(sv) {
        const runs = [];
        const deleted = [];
        for (const run of text.ordered(this.runs)) {
            const covered = covers(sv, run);
            if (covered === 0)
                runs.push(run);
            else if (covered < text.runeCount(run))
                runs.push(text.splitRun(run, covered)[1]);
            if (run.deleted)
                deleted.push({ id: run.id, n: text.runeCount(run) });
        }
        return { runs, deleted };
    }
    /** Folds in an update from a peer. Applying one twice changes nothing. */
    apply(u) {
        if (u.runs.length === 0 && u.deleted.length === 0)
            return;
        let runs = this.runs;
        if (u.runs.length > 0)
            runs = text.mergeRuns(runs, u.runs);
        if (u.deleted.length > 0)
            runs = text.markDeleted(runs, u.deleted);
        this.runs = runs;
        this.rebuild();
    }
    /** Replaces the contents wholesale — for adopting a snapshot. */
    reset(runs) {
        this.runs = text.normalize(runs);
        this.rebuild();
    }
    /** Records the origins of everything currently held. */
    rebuild() {
        this.sv = new Map();
        for (const run of this.runs)
            this.noteRun(run);
    }
    note() {
        for (const run of this.runs)
            this.noteRun(run);
    }
    noteRun(run) {
        const origin = hlc.origin(run.id);
        const end = run.id.l + text.runeCount(run);
        const seen = this.sv.get(origin);
        if (seen === undefined || end > seen)
            this.sv.set(origin, end);
    }
}
