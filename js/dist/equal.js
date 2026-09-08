/** Structural equality and the comparisons conditions need. */
/** Deep structural equality over JSON-shaped values. */
export function equal(a, b) {
    if (a === b)
        return true;
    if (typeof a === 'number' && typeof b === 'number') {
        // NaN is not equal to itself under ===, but two NaN readings are the same
        // value for diffing purposes.
        return Number.isNaN(a) && Number.isNaN(b);
    }
    if (a === null || b === null || typeof a !== 'object' || typeof b !== 'object')
        return false;
    if (Array.isArray(a) !== Array.isArray(b))
        return false;
    if (Array.isArray(a) && Array.isArray(b)) {
        if (a.length !== b.length)
            return false;
        return a.every((v, i) => equal(v, b[i]));
    }
    const ao = a;
    const bo = b;
    const ak = Object.keys(ao);
    const bk = Object.keys(bo);
    if (ak.length !== bk.length)
        return false;
    return ak.every((k) => k in bo && equal(ao[k], bo[k]));
}
/** A deep copy, for values that are JSON-shaped. */
export function clone(v) {
    if (v === null || typeof v !== 'object')
        return v;
    if (Array.isArray(v))
        return v.map((x) => clone(x));
    const out = {};
    for (const [k, val] of Object.entries(v))
        out[k] = clone(val);
    return out;
}
/**
 * Ordered comparison, as conditions define it: two numbers compare
 * numerically, two strings lexicographically, anything else is an error.
 */
export function compareOrdered(a, b, op) {
    const bothNumbers = typeof a === 'number' && typeof b === 'number';
    const bothStrings = typeof a === 'string' && typeof b === 'string';
    if (!bothNumbers && !bothStrings) {
        throw new Error(`unsupported comparison ${op} between ${typeof a} and ${typeof b}`);
    }
    switch (op) {
        case '>':
            return a > b;
        case '>=':
            return a >= b;
        case '<':
            return a < b;
        case '<=':
            return a <= b;
    }
    throw new Error(`unknown ordered operator ${op}`);
}
