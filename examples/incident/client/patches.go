package client

import (
	deep "github.com/brunoga/deep/v6"

	"github.com/brunoga/deep/examples/incident/model"
)

// The patch vocabulary: every write commandpost performs, as a named patch
// constructor. Conditions ride inside the patches, so the concurrency story
// needs no locks anywhere — the server evaluates each condition against the
// state the patch actually meets.

func statusPath() deep.Path[model.Incident, model.Status] {
	return deep.PathString[model.Incident, model.Status]("/status")
}

func severityPath() deep.Path[model.Incident, model.Severity] {
	return deep.PathString[model.Incident, model.Severity]("/severity")
}

func taskField(taskID, field string) string {
	return "/tasks/" + deep.EscapePathKey(taskID) + "/" + field
}

// SetTitle renames the incident.
func SetTitle(title string) deep.Patch[model.Incident] {
	return deep.NewPatch[model.Incident]().With(
		deep.Set(deep.PathString[model.Incident, string]("/title"), title),
	).Build()
}

// SetStatus moves the incident to a status, unconditionally; the server still
// validates the outcome (closed with open tasks is refused there).
func SetStatus(s model.Status) deep.Patch[model.Incident] {
	return deep.NewPatch[model.Incident]().With(deep.Set(statusPath(), s)).Build()
}

// Escalate raises severity to sev — but only If the incident is currently
// less severe. Applied twice, or after someone escalated further, it skips:
// an escalation can never de-escalate, no matter how late it arrives.
func Escalate(sev model.Severity) deep.Patch[model.Incident] {
	return deep.NewPatch[model.Incident]().With(
		deep.Set(severityPath(), sev).If(deep.Gt(severityPath(), sev)),
	).Build()
}

// SetSeverity sets severity outright — the unconditional counterpart, for
// deliberate downgrades.
func SetSeverity(sev model.Severity) deep.Patch[model.Incident] {
	return deep.NewPatch[model.Incident]().With(deep.Set(severityPath(), sev)).Build()
}

// ClaimTask takes a task — If nobody holds it. Two responders racing to
// claim resolve atomically on the server: one applies, one skips.
func ClaimTask(taskID, who string) deep.Patch[model.Incident] {
	owner := deep.PathString[model.Incident, string](taskField(taskID, "owner"))
	return deep.NewPatch[model.Incident]().With(
		deep.Set(owner, who).If(deep.Eq(owner, "")),
	).Build()
}

// CompleteTask marks a task done.
func CompleteTask(taskID string) deep.Patch[model.Incident] {
	return deep.NewPatch[model.Incident]().With(
		deep.Set(deep.PathString[model.Incident, bool](taskField(taskID, "done")), true),
	).Build()
}

// AddTask appends a task to the checklist, addressed by its key — the keyed
// list makes /tasks/<id> a stable name whatever position it lands in.
func AddTask(t model.Task) deep.Patch[model.Incident] {
	return deep.Patch[model.Incident]{Operations: []deep.Operation{{
		Kind: deep.OpAdd,
		Path: "/tasks/" + deep.EscapePathKey(t.ID),
		New:  t,
	}}}
}

// RemoveTask deletes a task by key.
func RemoveTask(taskID string) deep.Patch[model.Incident] {
	return deep.Patch[model.Incident]{Operations: []deep.Operation{{
		Kind: deep.OpRemove,
		Path: "/tasks/" + deep.EscapePathKey(taskID),
	}}}
}

// Close closes the incident — under a Guard that it is resolved first. A
// not-yet-resolved incident rejects the whole patch with a conflict rather
// than skipping: closing is not something to half-do.
func Close() deep.Patch[model.Incident] {
	return deep.NewPatch[model.Incident]().
		Guard(deep.Eq(statusPath(), model.StatusResolved)).
		With(deep.Set(statusPath(), model.StatusClosed)).
		Build()
}

// Handoff transfers command, strictly: the patch carries who the sender
// believes is in command, and if someone else took over in the meantime the
// strict Old check refuses instead of silently overriding them.
func Handoff(from, to string) deep.Patch[model.Incident] {
	return deep.Patch[model.Incident]{
		Strict: true,
		Operations: []deep.Operation{{
			Kind: deep.OpReplace,
			Path: "/commander",
			Old:  from,
			New:  to,
		}},
	}
}
