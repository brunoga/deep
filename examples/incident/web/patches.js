// The patch vocabulary, in the browser. Deliberately free of imports, so it
// is equally usable from a page, from a test, and from anything else.
//
// This is the same set of operations cmd/incident builds in Go — the same
// paths, the same conditions — because it is the same wire format. A patch
// built here is indistinguishable from one the CLI sends, which is the point
// of the exercise: the server has no idea which one it is talking to.
/** Escapes a path token, as RFC 6901 requires. */
function key(id) {
  return String(id).replaceAll('~', '~0').replaceAll('/', '~1');
}

/**
 * Claim a task — but only if nobody holds it.
 *
 * Two responders clicking at once resolve on the server: one patch applies,
 * the other *skips*, and skipping is not an error. No locking, no retry.
 */
export function claimTask(taskID, who) {
  const owner = `/tasks/${key(taskID)}/owner`;
  return {
    ops: [{ k: 'replace', p: owner, n: who, if: { p: owner, o: '==', v: '' } }],
  };
}

/** Mark a task done. */
export function completeTask(taskID) {
  return { ops: [{ k: 'replace', p: `/tasks/${key(taskID)}/done`, n: true }] };
}

/**
 * Raise severity — but never lower it. Applied twice, or after somebody
 * escalated further, this skips rather than undoing their work.
 */
export function escalate(severity) {
  return {
    ops: [{ k: 'replace', p: '/severity', n: severity, if: { p: '/severity', o: '>', v: severity } }],
  };
}

/** Move the incident's status. The server judges whether the result is legal. */
export function setStatus(status) {
  return { ops: [{ k: 'replace', p: '/status', n: status }] };
}

/**
 * Close the incident, under a guard: unless it is resolved, the whole patch
 * is refused rather than half-applied.
 */
export function close() {
  return {
    cond: { p: '/status', o: '==', v: 'resolved' },
    ops: [{ k: 'replace', p: '/status', n: 'closed' }],
  };
}

/**
 * The key schema for this model: Go marks the identity of a task with a
 * `deep:"key"` struct tag, which is invisible in JSON, so a JavaScript peer
 * has to be told. With it, `diff` addresses tasks as /tasks/<id> exactly as
 * the Go server does.
 */
export const keys = { '/tasks': 'id' };
