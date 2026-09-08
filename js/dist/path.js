/**
 * JSON Pointer paths, as deep uses them. See docs/wire-patch.md.
 *
 * A path is a sequence of "/"-separated tokens. Within a token "~" is written
 * "~0" and "/" is written "~1"; unescaping does "~1" first so a literal "~1"
 * in a key survives the round trip.
 */
export function escapeKey(key) {
    return key.replaceAll('~', '~0').replaceAll('/', '~1');
}
export function unescapeKey(token) {
    return token.replaceAll('~1', '/').replaceAll('~0', '~');
}
/** Splits a path into its unescaped tokens. The root path yields none. */
export function parsePath(path) {
    if (path === '' || path === '/')
        return [];
    const tokens = path.startsWith('/') ? path.slice(1).split('/') : path.split('/');
    return tokens.map(unescapeKey);
}
/** Joins tokens into a path, escaping each. */
export function buildPath(tokens) {
    return tokens.map((t) => '/' + escapeKey(t)).join('');
}
/** The path of the container holding `path`, or null for the root. */
export function parentPath(path) {
    const i = path.lastIndexOf('/');
    if (i < 0)
        return null;
    return i === 0 ? '' : path.slice(0, i);
}
/** The last token of a path, unescaped. */
export function lastToken(path) {
    const parts = parsePath(path);
    return parts.length === 0 ? '' : parts[parts.length - 1];
}
function isArrayIndex(token) {
    return /^(0|[1-9][0-9]*)$/.test(token);
}
/** The identity field of the array at `containerPath`, if it has one. */
function keyFieldFor(containerPath, keys) {
    return keys?.[containerPath === '' ? '/' : containerPath];
}
/**
 * Resolves one token against an array.
 *
 * A keyed array is addressed by identity — /tasks/t3, not /tasks/0 — and the
 * key is consulted *first*, whether or not the token looks like an index.
 * Entity ids are commonly numeric, and treating "/items/7" as a position when
 * the array is keyed silently writes to the wrong element: the one that
 * happens to sit at index 7 rather than the one whose id is 7. Go makes the
 * same choice, and for the same reason.
 *
 * Returns -1 when nothing matches.
 */
function elementIndex(arr, containerPath, token, keys) {
    const field = keyFieldFor(containerPath, keys);
    if (field !== undefined) {
        return arr.findIndex((el) => el !== null &&
            typeof el === 'object' &&
            String(el[field]) === token);
    }
    return isArrayIndex(token) ? Number(token) : -1;
}
/** Reads the value at `path`, reporting whether anything is there. */
export function resolve(root, path, keys) {
    let cur = root;
    let here = '';
    for (const token of parsePath(path)) {
        if (cur === null || cur === undefined)
            return { found: false };
        if (Array.isArray(cur)) {
            const i = elementIndex(cur, here, token, keys);
            if (i < 0 || i >= cur.length)
                return { found: false };
            cur = cur[i];
            here += '/' + escapeKey(token);
            continue;
        }
        if (typeof cur !== 'object')
            return { found: false };
        const obj = cur;
        if (!(token in obj))
            return { found: false };
        cur = obj[token];
        here += '/' + escapeKey(token);
    }
    return { found: true, value: cur };
}
/**
 * Walks to the container of `path`, creating nothing.
 *
 * Returns the container and the final token, or null when the route does not
 * exist — an operation addressing a path through a missing container fails
 * rather than conjuring one, matching Go.
 */
function locate(root, path, keys) {
    const parts = parsePath(path);
    if (parts.length === 0)
        return null;
    let cur = root;
    let here = '';
    for (let i = 0; i < parts.length - 1; i++) {
        const token = parts[i];
        if (cur === null || cur === undefined)
            return null;
        if (Array.isArray(cur)) {
            const idx = elementIndex(cur, here, token, keys);
            if (idx < 0 || idx >= cur.length)
                return null;
            cur = cur[idx];
            here += '/' + escapeKey(token);
            continue;
        }
        if (typeof cur !== 'object')
            return null;
        const obj = cur;
        if (!(token in obj))
            return null;
        cur = obj[token];
        here += '/' + escapeKey(token);
    }
    return { parent: cur, token: parts[parts.length - 1], containerPath: here };
}
/**
 * Writes `value` at `path`. `insert` distinguishes an `add` (which may extend
 * an array) from a `replace` (which may not).
 */
export function setAt(root, path, value, insert, keys) {
    const at = locate(root, path, keys);
    if (at === null)
        throw new Error(`path ${path} does not resolve`);
    const { parent, token, containerPath } = at;
    if (Array.isArray(parent)) {
        if (token === '-') {
            if (!insert)
                throw new Error(`path ${path} does not resolve`);
            parent.push(value);
            return;
        }
        if (keyFieldFor(containerPath, keys) !== undefined) {
            // A keyed element: present means replace it where it sits, absent means
            // append. Position carries no meaning in a keyed array, so appending is
            // not a choice about order.
            const found = elementIndex(parent, containerPath, token, keys);
            if (found >= 0)
                parent[found] = value;
            else
                parent.push(value);
            return;
        }
        if (!isArrayIndex(token)) {
            throw new Error(`array index expected at ${path}, got ${token}`);
        }
        const i = Number(token);
        if (i > parent.length || (i === parent.length && !insert)) {
            throw new Error(`index ${i} out of range at ${path}`);
        }
        if (insert && i === parent.length)
            parent.push(value);
        else if (insert)
            parent.splice(i, 0, value);
        else
            parent[i] = value;
        return;
    }
    if (parent === null || typeof parent !== 'object') {
        throw new Error(`cannot write ${path}: container is not an object`);
    }
    parent[token] = value;
}
/** Deletes the value at `path`. */
export function removeAt(root, path, keys) {
    const at = locate(root, path, keys);
    if (at === null)
        throw new Error(`path ${path} does not resolve`);
    const { parent, token, containerPath } = at;
    if (Array.isArray(parent)) {
        const i = elementIndex(parent, containerPath, token, keys);
        if (i < 0)
            throw new Error(`no element ${token} at ${path}`);
        if (i >= parent.length)
            throw new Error(`index ${i} out of range at ${path}`);
        parent.splice(i, 1);
        return;
    }
    if (parent === null || typeof parent !== 'object') {
        throw new Error(`cannot remove ${path}: container is not an object`);
    }
    const obj = parent;
    if (!(token in obj))
        throw new Error(`key ${token} not found at ${path}`);
    delete obj[token];
}
/** True when `ancestor` covers `descendant` at a token boundary. */
export function encloses(ancestor, descendant) {
    if (ancestor === descendant)
        return true;
    if (ancestor === '' || ancestor === '/')
        return true;
    // A trailing slash on the ancestor is tolerated, as Go does: "/user/" and
    // "/user" name the same subtree, and an allowlist written either way should
    // admit the same operations.
    return descendant.startsWith(ancestor.replace(/\/$/, '') + '/');
}
