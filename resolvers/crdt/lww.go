package crdt

import (
	"reflect"

	"github.com/brunoga/deep/v2"
	"github.com/brunoga/deep/v2/crdt/hlc"
)

// LWWResolver implements deep.ConflictResolver using Last-Write-Wins logic
// for a single operation or delta with a fixed timestamp.
type LWWResolver struct {
	Clocks     map[string]hlc.HLC
	Tombstones map[string]hlc.HLC
	OpTime     hlc.HLC
}

func (r *LWWResolver) Resolve(path string, op deep.OpKind, key, prevKey any, val reflect.Value) bool {
	p := path
	if p == "" {
		p = "<root>"
	}

	lClock, _ := r.Clocks[p]
	lTomb, hasLT := r.Tombstones[p]
	lTime := lClock
	if hasLT && lTomb.After(lTime) {
		lTime = lTomb
	}

	if !r.OpTime.After(lTime) {
		return false
	}

	// Accepted. Update clocks for this path.
	if op == deep.OpRemove {
		r.Tombstones[p] = r.OpTime
	} else {
		r.Clocks[p] = r.OpTime
	}

	return true
}

// StateResolver implements deep.ConflictResolver for merging two full CRDT states.
// It compares clocks for each path dynamically.
type StateResolver struct {
	LocalClocks      map[string]hlc.HLC
	LocalTombstones  map[string]hlc.HLC
	RemoteClocks     map[string]hlc.HLC
	RemoteTombstones map[string]hlc.HLC
}

func (r *StateResolver) Resolve(path string, op deep.OpKind, key, prevKey any, val reflect.Value) bool {
	p := path
	if p == "" {
		p = "<root>"
	}

	// Local Time
	lClock, _ := r.LocalClocks[p]
	lTomb, hasLT := r.LocalTombstones[p]
	lTime := lClock
	if hasLT && lTomb.After(lTime) {
		lTime = lTomb
	}

	// Remote Time
	rClock, hasR := r.RemoteClocks[p]
	rTomb, hasRT := r.RemoteTombstones[p]
	if !hasR && !hasRT {
		return false
	}
	rTime := rClock
	if hasRT && rTomb.After(rTime) {
		rTime = rTomb
	}

	return rTime.After(lTime)
}
