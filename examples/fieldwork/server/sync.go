package server

import (
	"encoding/json"
	"fmt"
	"strings"

	deep "github.com/brunoga/deep/v6"

	"github.com/brunoga/deep/examples/fieldwork/model"
)

// The sync engine. A technician pushes, per asset, the diff between their
// shadow (the state the server last acknowledged to them) and their working
// copy, together with the version the shadow came from. Three cases:
//
//   - The record has not moved: the patch applies as-is. The common case,
//     and the cheap one.
//   - The record moved while the technician was offline: a three-way merge.
//     The server reconstructs its own changes since the shadow's version
//     (ChangesSince — a reverse-walk and one diff), and deep.Merge combines
//     the two concurrent patches. This is Merge in its designed role: two
//     patches against one base. Where both sides touched the same path, the
//     policy resolver decides — and every decision it makes is reported
//     back, so the technician sees exactly which of their edits gave way.
//   - The outcome fails validation: the push is rejected whole, the record
//     untouched, and the technician keeps their working copy to fix and
//     retry.

// Push is one asset's offline changes.
type Push struct {
	ID          string                  `json:"id"`
	BaseVersion int64                   `json:"base_version"`
	Patch       deep.Patch[model.Asset] `json:"patch"`
}

// Conflict is one path both sides changed, and how the policy settled it.
type Conflict struct {
	Path   string          `json:"path"`
	Mine   json.RawMessage `json:"mine,omitempty"`   // what the technician pushed
	Theirs json.RawMessage `json:"theirs,omitempty"` // what the server had
	Kept   string          `json:"kept"`             // "mine" | "theirs" | "combined"
}

// PushResult reports one asset's fate.
type PushResult struct {
	ID string `json:"id"`
	// Outcome is "applied" (clean), "merged" (three-way, possibly with
	// conflicts), or "rejected" (validation refused; nothing changed).
	Outcome   string      `json:"outcome"`
	Version   int64       `json:"version"`
	Asset     model.Asset `json:"asset"`
	Conflicts []Conflict  `json:"conflicts,omitempty"`
	Error     string      `json:"error,omitempty"`
}

// Sync applies a batch of pushes and reports, for each, the authoritative
// state the client should adopt as both shadow and working copy.
func (s *Store) Sync(author string, pushes []Push) []PushResult {
	out := make([]PushResult, 0, len(pushes))
	for _, push := range pushes {
		out = append(out, s.syncOne(author, push))
	}
	return out
}

func (s *Store) syncOne(author string, push Push) PushResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[push.ID]
	if !ok {
		return PushResult{ID: push.ID, Outcome: "rejected", Error: ErrNotFound.Error()}
	}
	res := PushResult{ID: push.ID}

	apply := func(base model.Asset, p deep.Patch[model.Asset]) (model.Asset, error) {
		work := deep.Clone(base)
		if err := deep.Apply(&work, p, deep.WithAllowedPaths(editablePaths...)); err != nil {
			return model.Asset{}, err
		}
		return work, nil
	}

	var work model.Asset
	var err error
	if push.BaseVersion == rec.version {
		// Fast path: nothing moved, the patch speaks for itself.
		res.Outcome = "applied"
		work, err = apply(*rec.asset, push.Patch)
	} else {
		// The record moved. Reconstruct the base both sides diverged from,
		// gather the server's side as one patch, and merge.
		res.Outcome = "merged"
		var base model.Asset
		base, err = s.stateAtLocked(rec, push.BaseVersion)
		if err == nil {
			var serverSince deep.Patch[model.Asset]
			serverSince, err = deep.Diff(base, *rec.asset)
			if err == nil {
				merged, conflicts := mergeWithPolicy(base, serverSince, push.Patch)
				res.Conflicts = conflicts
				work, err = apply(base, merged)
			}
		}
	}
	if err == nil {
		res.Version, err = s.commitLocked(rec, author, work)
	}
	if err != nil {
		return PushResult{ID: push.ID, Outcome: "rejected", Version: rec.version,
			Asset: deep.Clone(*rec.asset), Error: err.Error()}
	}
	res.Asset = deep.Clone(*rec.asset)
	return res
}

// mergeWithPolicy merges the server's and the technician's concurrent
// patches under the field policy, recording every collision the resolver
// settles. deep.Merge consults the resolver only where both sides wrote the
// same path; enclosing collisions keep the technician's operation (Merge's
// other-wins rule) and are reported as such.
//
// base is the state both sides diverged from, which the policy needs to tell
// what each side *added* from what each side merely carried along.
func mergeWithPolicy(base model.Asset, server, client deep.Patch[model.Asset]) (deep.Patch[model.Asset], []Conflict) {
	var conflicts []Conflict

	resolver := deep.ResolverFunc(func(path string, theirs, mine any) any {
		chosen, kept := policy(base, path, theirs, mine)
		conflicts = append(conflicts, Conflict{
			Path:   path,
			Mine:   toJSON(mine),
			Theirs: toJSON(theirs),
			Kept:   kept,
		})
		return chosen
	})
	merged := deep.Merge(server, client, resolver)

	// Enclosing collisions — one side wrote /checks, the other /checks/x/done
	// — are settled structurally by Merge (the client's operation survives);
	// find and report them so no overridden edit disappears in silence. One
	// report per overridden server path, however many client operations
	// happened to fall inside it.
	serverPaths := make([]string, 0, len(server.Operations))
	for _, op := range server.Operations {
		serverPaths = append(serverPaths, op.Path)
	}
	reported := map[string]bool{}
	for _, op := range client.Operations {
		for _, sp := range serverPaths {
			if reported[sp] || sp == op.Path {
				continue
			}
			if encloses(sp, op.Path) || encloses(op.Path, sp) {
				reported[sp] = true
				conflicts = append(conflicts, Conflict{Path: sp, Kept: "mine"})
			}
		}
	}
	return merged, conflicts
}

// policy is the field rulebook for head-on collisions.
func policy(base model.Asset, path string, theirs, mine any) (any, string) {
	switch {
	case strings.HasPrefix(path, "/readings/"):
		// Measurements come from whoever stood at the asset.
		return mine, "mine"
	case path == "/status":
		// The worse report wins: a fault seen by either side is a fault.
		w := model.Worse(asStatus(theirs), asStatus(mine))
		if w == asStatus(mine) {
			return mine, "mine"
		}
		return theirs, "theirs"
	case path == "/notes":
		// Nobody's field notes get thrown away — but neither side's copy of
		// what was already there gets duplicated. Notes are appended to on
		// both sides, so the merge keeps the shared text once and then each
		// side's addition to it.
		return mergeNotes(base.Notes, asString(theirs), asString(mine))
	default:
		// Everything else — assignment, naming, checklist edits — is the
		// office's call.
		return theirs, "theirs"
	}
}

// mergeNotes combines two edits of the same note field against the text they
// both started from. When both sides appended — the normal case — the result
// is the shared text plus each addition, once. When a side rewrote the text
// instead of appending, there is no shared prefix to preserve and both
// versions are kept whole rather than guessing.
func mergeNotes(base, theirs, mine string) (any, string) {
	switch {
	case theirs == mine:
		return mine, "mine"
	case mine == base:
		return theirs, "theirs"
	case theirs == base:
		return mine, "mine"
	}
	if strings.HasPrefix(theirs, base) && strings.HasPrefix(mine, base) {
		out := base
		for _, add := range []string{
			strings.TrimPrefix(theirs, base),
			strings.TrimPrefix(mine, base),
		} {
			add = strings.Trim(add, "\n")
			if add == "" {
				continue
			}
			if out != "" {
				out += "\n"
			}
			out += add
		}
		return out, "combined"
	}
	return theirs + "\n" + mine, "combined"
}

func toJSON(v any) json.RawMessage {
	if v == nil {
		return nil
	}
	if raw, ok := v.(deep.RawValue); ok {
		return json.RawMessage(raw.JSON)
	}
	data, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(fmt.Sprintf("%q", fmt.Sprintf("%v", v)))
	}
	return data
}

// asStatus and asString read merge values that may arrive wire-encoded.
func asStatus(v any) model.Status {
	if s, ok := deep.ValueAs[model.Status](v); ok {
		return s
	}
	if s, ok := deep.ValueAs[string](v); ok {
		return model.Status(s)
	}
	return ""
}

func asString(v any) string {
	if s, ok := deep.ValueAs[string](v); ok {
		return s
	}
	return ""
}

// encloses reports whether ancestor covers descendant at a segment boundary.
func encloses(ancestor, descendant string) bool {
	return strings.HasPrefix(descendant, ancestor+"/")
}
