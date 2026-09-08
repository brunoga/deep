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
import { Document } from './crdt/document.ts';
import { Awareness } from './crdt/awareness.ts';
export interface ConnectOptions {
    /** The room's websocket URL, room parameter included. */
    url: string;
    /**
     * This client's identity. Reuse it across reconnects so its counter keeps
     * climbing from where it left off rather than starting a fresh space.
     */
    node: string;
    /** An existing document to resume from — offline edits and all. */
    document?: Document;
    /** How long a peer stays visible after its last announcement. */
    presenceTtlMs?: number;
    /** Injectable for tests; defaults to the platform WebSocket. */
    WebSocketImpl?: typeof WebSocket;
}
/** A joined room: a live document, and the peers editing it. */
export declare class Room<P = unknown> {
    readonly document: Document;
    readonly awareness: Awareness<P>;
    private socket;
    private published;
    private updateListeners;
    private closeListeners;
    constructor(socket: WebSocket, document: Document, awareness: Awareness<P>);
    /** The document's text. */
    get text(): string;
    /** Its length, in code points. */
    get length(): number;
    /** Inserts and sends what changed. */
    insert(pos: number, value: string): void;
    /** Deletes and sends what changed. */
    delete(pos: number, count: number): void;
    /**
     * Sends every local edit made since the last publish. Called for you by
     * insert and delete; call it yourself after editing the document directly.
     */
    publish(): void;
    /**
     * Announces this client's presence. It is also the heartbeat: call it
     * periodically, and on every cursor move, or peers stop drawing this client.
     */
    announce(state: P): void;
    /** Says goodbye and closes, so peers drop this client at once. */
    close(): void;
    /** Runs fn after a remote update lands in the document. */
    onUpdate(fn: () => void): () => void;
    /** Runs fn when the connection ends. */
    onClose(fn: (reason: string) => void): () => void;
    /** @internal */
    handleFrame(kind: number, payload: Uint8Array): void;
    /** @internal */
    handleClose(reason: string): void;
    /**
     * Marks everything currently held as published — what the handshake leaves
     * true, since the room has just been told about all of it.
     * @internal
     */
    publishedFromDocument(): void;
    /** @internal */
    send(kind: number, payload: Uint8Array): void;
}
/**
 * Joins a room and completes the sync handshake: whatever the room has that
 * this client does not arrives before the promise resolves, and whatever this
 * client has that the room does not — offline edits, on a reconnect — goes up.
 */
export declare function connect<P = unknown>(opts: ConnectOptions): Promise<Room<P>>;
