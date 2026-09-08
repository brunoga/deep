# The patch wire format

A `deep` patch is a flat, self-describing list of operations. This document
specifies its JSON encoding precisely enough to produce and consume patches
from another language, and is the reference the TypeScript client is written
against.

The format is stable within a major version of the library. Everything here
describes deep v6.

## Patch

```json
{
  "cond": { "…condition…" },
  "ops":  [ { "…operation…" } ],
  "strict": true
}
```

| Field | JSON | Meaning |
| :--- | :--- | :--- |
| Guard | `cond` | Optional. A condition that must hold before *any* operation applies. If it does not, the whole patch is a no-op. |
| Operations | `ops` | The ordered list. May be `null` or absent for an empty patch. |
| Strict | `strict` | Optional, default false. When true, every `replace` and `remove` verifies the current value against `o` before writing. |

Operations apply **in order**, and each one sees the state the previous ones
left. An implementation must not reorder them.

## Operation

```json
{ "k": "replace", "p": "/status", "o": "open", "n": "closed" }
```

| Field | JSON | Meaning |
| :--- | :--- | :--- |
| Kind | `k` | One of the kinds below. |
| Path | `p` | The target, as a JSON Pointer (see *Paths*). |
| From | `f` | Source path, for `move` and `copy` only. |
| Old | `o` | The value before, when known. Omitted when absent. |
| New | `n` | The value after. Omitted when absent. |
| If | `if` | Optional condition; the operation is skipped when it does not hold. |
| Unless | `un` | Optional condition; the operation is skipped when it *does* hold. |

A skipped operation is **not** an error. This distinction is load-bearing:
conditional writes rely on "your condition met reality and stood down" being
reportable separately from "this failed".

### Kinds

| `k` | Uses | Semantics |
| :--- | :--- | :--- |
| `add` | `p`, `n` | Create the value at `p`. For a map/object key or an absent field this is an insert; for an array it appends or inserts at the index. |
| `remove` | `p`, `o` | Delete the value at `p`. `o` carries what was there, which is what makes the operation reversible. |
| `replace` | `p`, `o`, `n` | Overwrite the value at `p`. |
| `move` | `p`, `f`, `o` | Take the value at `f`, remove it there, put it at `p`. `o` is the displaced value at `p`, when there was one. |
| `copy` | `p`, `f`, `o` | Same, but `f` keeps its value. The copy is independent (a deep copy). |
| `alias` | `p`, `f` | Make `p` refer to *the same* value `f` resolves to, rather than a copy. Emitted by `Diff` for the second and later routes to a value reachable more than once, so applying the patch rebuilds that sharing. Languages without reference semantics for the value in question may treat this as `copy`, at the cost of losing the sharing. |
| `log` | `p`, `n` | Emit `n` as a log line, scoped to `p`. Changes nothing. |

### Values are not decoded eagerly

`o` and `n` hold arbitrary JSON. A Go implementation deliberately keeps them
**encoded** until the operation reaches its target field, then decodes against
that field's real type — which is why an `int` field receives an int rather
than the float64 an untyped decode produces. An implementation in a
dynamically-typed language has no such problem and can decode immediately.

The consequence for interop is that a patch produced anywhere applies
correctly in Go: values are typed on arrival at the field, not on arrival at
the process.

## Paths

Paths are [RFC 6901](https://datatracker.ietf.org/doc/html/rfc6901) JSON
Pointers, with two conventions worth stating:

- A path is a sequence of `/`-separated tokens: `/players/ana/score`.
- Within a token, `~` is escaped as `~0` and `/` as `~1`. **Unescape `~1`
  before `~0`**, so that a literal `~1` in a key survives the round trip.
- A token that parses as a non-negative integer addresses an **array index**;
  anything else addresses a field or map key. A map whose keys are numeric
  strings is therefore indistinguishable from an array at the path level —
  the target's shape resolves it.
- The empty path (`""` or `"/"`) addresses the root.

### Keyed collections

Go structs may tag a field as the identity of a slice's elements:

```go
type Task struct {
    ID   string `deep:"key" json:"id"`
    Done bool   `json:"done"`
}
```

Diffs of such a slice address elements **by key, not by index** —
`/tasks/t3/done`, never `/tasks/2/done` — so reordering produces no operations
and concurrent edits to different elements never collide.

This matters for cross-language producers: the key field is a Go struct tag,
invisible in the JSON. A non-Go implementation that diffs an array cannot know
to use keyed semantics unless it is told. Producers should be given a schema
descriptor naming the key field per array path; the TypeScript client takes one
as an option. A patch that addresses keyed elements positionally will still
*apply*, but it will conflict where a keyed one would not.

## Two rules for models that cross the language boundary

Both of these were found by running the conformance corpus against a second
implementation, and both are invisible until you do.

### Paths use JSON names only when the type has generated code

The reflection engine names struct fields by their **Go** names:
`Diff` on a type with no generated code emits `/Status`, `/Meta/Level`. The
generated fast path names them by their **JSON** tags: `/status`,
`/meta/level`. Both *apply* correctly in Go — the appliers accept either — but
only the JSON form means anything to a receiver that holds the document as
JSON.

So: run `deep-gen` for the types you sync across languages, or name the Go
fields exactly as the JSON does. The conformance corpus is generated through
generated code for this reason.

### Do not use `omitempty` on fields a patch addresses

`json:"done,omitempty"` makes a zero value and an absent field the same bytes.
A patch that sets such a field back to its zero then produces a document that
a JavaScript receiver reconstructs as `{"done": false}` while Go marshals it
as `{}` — the two replicas hold the same *value* and disagree about the
*document*, having applied identical operations. Comparisons, hashes and
further diffs then diverge.

Leave `omitempty` off synced models. The cost is a few bytes; the alternative
is a class of disagreement that only shows up between languages.

## Conditions

```json
{ "p": "/severity", "o": ">", "v": 2 }
{ "o": "and", "apply": [ {…}, {…} ] }
```

| Field | JSON | Meaning |
| :--- | :--- | :--- |
| Path | `p` | What to read. Omitted for logical operators. |
| Op | `o` | The operator. |
| Value | `v` | The comparison value. Omitted for `exists` and logical operators. |
| Sub | `apply` | Sub-conditions, for `and`, `or` and `not`. |

| `o` | Meaning |
| :--- | :--- |
| `==`, `!=` | Equality. Numbers compare across representations (`5` equals `5.0`); a conversion that would not round-trip is not equal. |
| `>`, `>=`, `<`, `<=` | Ordered comparison. Two numbers compare as doubles; two strings compare lexicographically; anything else is an error. |
| `exists` | True when the path resolves to anything. A path that does not resolve makes this false rather than an error — the only operator for which an unresolvable path is not an error. |
| `in` | True when the value at `p` equals any element of the array `v`. |
| `matches` | True when the value at `p`, rendered as text, matches the regular expression `v`. Go's RE2 syntax; implementations should document divergence. |
| `type` | True when the value at `p` has type `v`: one of `string`, `number`, `boolean`, `object`, `array`, `null`. |
| `and`, `or` | All / any of `apply` hold. Empty `apply` is vacuously true for `and`, false for `or`. |
| `not` | The negation of `apply[0]`. Missing sub-condition is an error. |

`and` short-circuits on the first failure and `or` on the first success, so an
error from a later sub-condition may not surface. Do not rely on evaluation of
every branch.

## Reversal

`Reverse` turns a patch into one that undoes it: `add` becomes `remove`,
`remove` becomes `add`, `replace` swaps `o` and `n`, `move` swaps `p` and `f`.
The operations are also reversed in order, since they applied in order.

Reversal is exact only when the operations carry their `o` values — which
diff-produced patches always do, and hand-built ones may not.

## Merging

`Merge(base, other, resolver)` combines two patches **against the same
starting state**. Operations are matched by path; where both write the same
path, the resolver decides (and without one, `other` wins). Where one patch's
path *encloses* the other's — `/user` against `/user/name` — keeping both
would produce a patch that cannot apply, so the operation from `other`
survives and the enclosed or enclosing one is dropped.

Merge is for **concurrent** patches. Composing a *sequence* of patches with it
is a misuse that silently drops operations: an `add /a` followed by a later
`replace /a/b` collapses to just the latter, and the result cannot apply. To
compose a sequence, apply the patches in order and diff the endpoints.

## Interop checklist

An implementation that satisfies these is compatible:

1. Emits and accepts the field names above, omitting empty optional fields.
2. Applies operations in order, each seeing the previous ones' effects.
3. Treats a false condition as a skip, not an error; reports skips separately.
4. Escapes and unescapes path tokens per RFC 6901, `~1` before `~0`.
5. Compares numbers across representations for `==`/`!=`, and as doubles for
   ordered operators.
6. Passes the conformance corpus in `testdata/conformance/` (see
   [wire-crdt.md](wire-crdt.md) for the CRDT counterpart).
