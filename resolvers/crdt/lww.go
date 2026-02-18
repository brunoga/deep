package crdt

import (
	"reflect"

	"github.com/brunoga/deep/v4"
	"github.com/brunoga/deep/v4/crdt/hlc"
)

// LWWResolver implements deep.ConflictResolver using Last-Write-Wins logic
// for a single operation or delta with a fixed timestamp.
type LWWResolver struct {
	Clocks     map[string]hlc.HLC
	Tombstones map[string]hlc.HLC
	OpTime     hlc.HLC
}

func (r *LWWResolver) Resolve(path string, op deep.OpKind, key, prevKey any, current, proposed reflect.Value) (reflect.Value, bool) {
	lClock := r.Clocks[path]
	lTomb, hasLT := r.Tombstones[path]
	lTime := lClock
	if hasLT && lTomb.After(lTime) {
		lTime = lTomb
	}

	if !r.OpTime.After(lTime) {
		return reflect.Value{}, false
	}

	// Accepted. Update clocks for this path.
	if op == deep.OpRemove {
		r.Tombstones[path] = r.OpTime
	} else {
		r.Clocks[path] = r.OpTime
	}

	return proposed, true
}

// StateResolver implements deep.ConflictResolver for merging two full CRDT states.
// It compares clocks for each path dynamically.
type StateResolver struct {
	LocalClocks      map[string]hlc.HLC
	LocalTombstones  map[string]hlc.HLC
	RemoteClocks     map[string]hlc.HLC
	RemoteTombstones map[string]hlc.HLC
}

func (r *StateResolver) Resolve(path string, op deep.OpKind, key, prevKey any, current, proposed reflect.Value) (reflect.Value, bool) {
	// Local Time
	lClock := r.LocalClocks[path]
	lTomb, hasLT := r.LocalTombstones[path]
	lTime := lClock
	if hasLT && lTomb.After(lTime) {
		lTime = lTomb
	}

	// Remote Time
	rClock, hasR := r.RemoteClocks[path]
	rTomb, hasRT := r.RemoteTombstones[path]
	if !hasR && !hasRT {
		return reflect.Value{}, false
	}
	rTime := rClock
	if hasRT && rTomb.After(rTime) {
		rTime = rTomb
	}

	if rTime.After(lTime) {
		return proposed, true
	}
	return reflect.Value{}, false
}
