/**
 * The client half of deep's websocket sync protocol, for browsers.
 *
 * It speaks what `deep/ws` serves: one byte of frame kind, then a payload —
 * the compact binary encoding for documents and state vectors, JSON for
 * presence. See docs/wire-crdt.md.
 *
 * ```ts
 * const room = await connect({ url: 'ws://host/ws?room=inc-1', node: 'ana' });
 * room.onUpdate(() => render(room.text));
 * room.insert(0, 'typing');   // sent immediately
 * ```
 */
import { Document } from "./crdt/document.js";
import { Awareness } from "./crdt/awareness.js";
import { decodeStateVector, decodeUpdate, encodeStateVector, encodeUpdate, } from "./crdt/binary.js";
const FRAME_STATE_VECTOR = 1;
const FRAME_UPDATE = 2;
const FRAME_PRESENCE = 3;
/** A joined room: a live document, and the peers editing it. */
export class Room {
    document;
    awareness;
    socket;
    published = new Map();
    updateListeners = new Set();
    closeListeners = new Set();
    constructor(socket, document, awareness) {
        this.socket = socket;
        this.document = document;
        this.awareness = awareness;
    }
    /** The document's text. */
    get text() {
        return this.document.toString();
    }
    /** Its length, in code points. */
    get length() {
        return this.document.length;
    }
    /** Inserts and sends what changed. */
    insert(pos, value) {
        this.document.insert(pos, value);
        this.publish();
    }
    /** Deletes and sends what changed. */
    delete(pos, count) {
        this.document.delete(pos, count);
        this.publish();
    }
    /**
     * Sends every local edit made since the last publish. Called for you by
     * insert and delete; call it yourself after editing the document directly.
     */
    publish() {
        const pending = this.document.since(this.published);
        if (pending.runs.length === 0 && pending.deleted.length === 0)
            return;
        // Marked published only once the socket has actually taken the bytes. A
        // browser silently discards a send on a closing socket, and recording the
        // edit as sent beforehand would mean `since` never offers it again — the
        // edit would be lost to every peer while still sitting on screen.
        this.send(FRAME_UPDATE, encodeUpdate(pending));
        this.published = this.document.stateVector();
    }
    /**
     * Announces this client's presence. It is also the heartbeat: call it
     * periodically, and on every cursor move, or peers stop drawing this client.
     */
    announce(state) {
        const update = this.awareness.setLocal(state);
        this.send(FRAME_PRESENCE, new TextEncoder().encode(JSON.stringify(update)));
    }
    /** Says goodbye and closes, so peers drop this client at once. */
    close() {
        try {
            const leave = this.awareness.leave();
            this.send(FRAME_PRESENCE, new TextEncoder().encode(JSON.stringify(leave)));
        }
        catch {
            // The socket may already be gone; leaving quietly is fine.
        }
        this.socket.close(1000, 'bye');
    }
    /** Runs fn after a remote update lands in the document. */
    onUpdate(fn) {
        this.updateListeners.add(fn);
        return () => this.updateListeners.delete(fn);
    }
    /** Runs fn when the connection ends. */
    onClose(fn) {
        this.closeListeners.add(fn);
        return () => this.closeListeners.delete(fn);
    }
    /** @internal */
    handleFrame(kind, payload) {
        switch (kind) {
            case FRAME_UPDATE: {
                const before = this.document.stateVector();
                this.document.apply(decodeUpdate(payload));
                // What arrived is now seen — without this the next publish would echo
                // the room's own changes back at it. But only origins with no
                // unpublished local edits advance: taking the whole vector would mark
                // local edits published without ever having sent them.
                for (const [origin, n] of this.document.stateVector()) {
                    if ((before.get(origin) ?? 0) === (this.published.get(origin) ?? 0)) {
                        this.published.set(origin, n);
                    }
                }
                for (const fn of this.updateListeners)
                    fn();
                break;
            }
            case FRAME_PRESENCE: {
                try {
                    const update = JSON.parse(new TextDecoder().decode(payload));
                    this.awareness.apply(update);
                }
                catch {
                    // A peer with a different presence shape; not ours to read.
                }
                break;
            }
            case FRAME_STATE_VECTOR:
                // Informational mid-session; nothing to do.
                break;
        }
    }
    /** @internal */
    handleClose(reason) {
        for (const fn of this.closeListeners)
            fn(reason);
    }
    /**
     * Marks everything currently held as published — what the handshake leaves
     * true, since the room has just been told about all of it.
     * @internal
     */
    publishedFromDocument() {
        this.published = this.document.stateVector();
    }
    /** @internal */
    send(kind, payload) {
        if (this.socket.readyState !== 1 /* OPEN */) {
            throw new Error('deepws: the connection is not open');
        }
        const frame = new Uint8Array(payload.length + 1);
        frame[0] = kind;
        frame.set(payload, 1);
        this.socket.send(frame);
    }
}
function frameOf(data) {
    const bytes = new Uint8Array(data);
    if (bytes.length === 0)
        throw new Error('deepws: empty frame');
    return { kind: bytes[0], payload: bytes.subarray(1) };
}
/**
 * Joins a room and completes the sync handshake: whatever the room has that
 * this client does not arrives before the promise resolves, and whatever this
 * client has that the room does not — offline edits, on a reconnect — goes up.
 */
export function connect(opts) {
    const Impl = opts.WebSocketImpl ?? WebSocket;
    const socket = new Impl(opts.url);
    socket.binaryType = 'arraybuffer';
    const document = opts.document ?? new Document(opts.node);
    const awareness = new Awareness(opts.node, { ttlMs: opts.presenceTtlMs });
    const room = new Room(socket, document, awareness);
    return new Promise((resolve, reject) => {
        let handshakeDone = false;
        // Updates that arrive during the handshake are buffered rather than
        // applied: the handshake can still fail, and with a resumed document the
        // caller's own text must not be left half-merged if it does.
        const pending = [];
        socket.onopen = () => {
            room.send(FRAME_STATE_VECTOR, encodeStateVector(document.stateVector()));
        };
        socket.onerror = () => {
            if (!handshakeDone)
                reject(new Error(`deepws: could not connect to ${opts.url}`));
        };
        socket.onclose = (event) => {
            if (!handshakeDone) {
                reject(new Error(`deepws: connection closed during handshake (${event.code})`));
                return;
            }
            room.handleClose(event.reason || `closed (${event.code})`);
        };
        socket.onmessage = (event) => {
            let frame;
            try {
                frame = frameOf(event.data);
            }
            catch (err) {
                if (!handshakeDone)
                    reject(err);
                return;
            }
            if (handshakeDone) {
                room.handleFrame(frame.kind, frame.payload);
                return;
            }
            switch (frame.kind) {
                case FRAME_UPDATE:
                    pending.push(frame.payload);
                    break;
                case FRAME_STATE_VECTOR: {
                    // The room's own vector: send it what it is missing, then adopt
                    // everything it sent us. Order matters — the update we send is
                    // computed against the document as it was.
                    try {
                        const theirs = decodeStateVector(frame.payload);
                        const missing = document.since(theirs);
                        if (missing.runs.length > 0 || missing.deleted.length > 0) {
                            room.send(FRAME_UPDATE, encodeUpdate(missing));
                        }
                        for (const payload of pending)
                            document.apply(decodeUpdate(payload));
                        handshakeDone = true;
                        room.publishedFromDocument();
                        resolve(room);
                    }
                    catch (err) {
                        reject(err);
                    }
                    break;
                }
                case FRAME_PRESENCE:
                    room.handleFrame(frame.kind, frame.payload);
                    break;
            }
        };
    });
}
