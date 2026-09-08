import { encloses } from "./path.js";
/**
 * Combines two patches computed against **the same starting state**.
 *
 * Operations are matched by path; where both write the same path the resolver
 * decides, and without one the second patch wins. Where one patch's path
 * encloses the other's — /user against /user/name — keeping both would
 * produce a patch that cannot apply, so the operation from `other` survives.
 *
 * This is for *concurrent* patches. Composing a *sequence* of patches with it
 * silently drops operations: an `add /a` followed by a later `replace /a/b`
 * collapses to the latter alone, which cannot apply to a state without /a. To
 * compose a sequence, apply them in order and diff the endpoints.
 */
export function merge(base, other, resolver) {
    const latest = new Map();
    const take = (ops, isOther) => {
        for (const op of ops) {
            const existing = latest.get(op.p);
            if (!existing) {
                latest.set(op.p, { op, isOther });
                continue;
            }
            if (resolver) {
                latest.set(op.p, { op: { ...op, n: resolver(op.p, existing.op.n, op.n) }, isOther });
            }
            else if (isOther) {
                latest.set(op.p, { op, isOther });
            }
        }
    };
    take(base.ops ?? [], false);
    take(other.ops ?? [], true);
    // An operation enclosed by — or enclosing — one from the other side cannot
    // coexist with it. The one from `other` survives; the resolver is not
    // consulted, because there is no single path at which to ask.
    for (const [path, entry] of [...latest]) {
        if (entry.isOther)
            continue;
        for (const [otherPath, otherEntry] of latest) {
            if (path === otherPath || otherEntry.isOther === entry.isOther)
                continue;
            if (encloses(otherPath, path) || encloses(path, otherPath)) {
                latest.delete(path);
                break;
            }
        }
    }
    const ops = [...latest.values()].map((e) => e.op);
    // Sorted, so the result is deterministic and an ancestor precedes its
    // descendants.
    ops.sort((a, b) => (a.p < b.p ? -1 : a.p > b.p ? 1 : 0));
    return { ops };
}
