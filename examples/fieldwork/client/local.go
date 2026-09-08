// Package client is the technician's side: a local copy of the assets that
// works with no network at all, and the sync round trip that reconciles it
// when a signal appears.
//
// The design has one idea in it. Every asset is stored twice — a shadow (the
// state the server last confirmed) and a working copy (what the technician
// has since done to it). There is no queue of pending operations to append
// to, replay, deduplicate or compact, because the queue is derived:
//
//	pending := deep.Diff(shadow, working)
//
// Edit the same field ten times offline and the diff still has one
// operation. Edit a field and undo it and the diff has none. Nothing to
// compact, nothing to garbage-collect, and the "what have I got outstanding"
// screen is that same call.
package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	deep "github.com/brunoga/deep/v6"

	"github.com/brunoga/deep/examples/fieldwork/model"
)

// ErrUnknownAsset reports an id the local store does not hold.
var ErrUnknownAsset = errors.New("asset not in local store")

// entry is one asset as the field device holds it.
type entry struct {
	// Shadow is the last state the server acknowledged, and the base every
	// local change is diffed against.
	Shadow model.Asset `json:"shadow"`
	// Working is what the technician sees and edits.
	Working model.Asset `json:"working"`
	// BaseVersion is the server version Shadow came from.
	BaseVersion int64 `json:"base_version"`
}

// Local is the on-device store.
type Local struct {
	mu      sync.Mutex
	dir     string
	entries map[string]*entry
}

// OpenLocal loads the device's store, creating it if this is a fresh device.
func OpenLocal(dir string) (*Local, error) {
	l := &Local{dir: dir, entries: map[string]*entry{}}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		var e entry
		if err := json.Unmarshal(data, &e); err != nil {
			return nil, fmt.Errorf("reading %s: %w", f, err)
		}
		l.entries[e.Working.ID] = &e
	}
	return l, nil
}

func (l *Local) path(id string) string { return filepath.Join(l.dir, id+".json") }

// saveLocked writes one entry through a temp file and a rename, so a device
// losing power mid-write keeps the previous state rather than a half one.
func (l *Local) saveLocked(e *entry) error {
	if !model.IDPattern.MatchString(e.Working.ID) {
		return fmt.Errorf("refusing to store asset with id %q", e.Working.ID)
	}
	data, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return err
	}
	tmp := l.path(e.Working.ID) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, l.path(e.Working.ID))
}

// Adopt records an authoritative state from the server: shadow and working
// both become it. This is what a sync result installs — the technician's
// device now agrees with the server, conflicts and all.
func (l *Local) Adopt(a model.Asset, version int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	e := &entry{Shadow: deep.Clone(a), Working: deep.Clone(a), BaseVersion: version}
	l.entries[a.ID] = e
	return l.saveLocked(e)
}

// Get returns the working copy — what the technician is looking at.
func (l *Local) Get(id string) (model.Asset, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.entries[id]
	if !ok {
		return model.Asset{}, fmt.Errorf("%w: %q", ErrUnknownAsset, id)
	}
	return deep.Clone(e.Working), nil
}

// List returns every working copy, ordered by id.
func (l *Local) List() []model.Asset {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]model.Asset, 0, len(l.entries))
	for _, e := range l.entries {
		out = append(out, deep.Clone(e.Working))
	}
	slices.SortFunc(out, func(a, b model.Asset) int { return strings.Compare(a.ID, b.ID) })
	return out
}

// Edit mutates the working copy in place — the only way local changes
// happen. The shadow is untouched, so the difference between them is exactly
// what this device owes the server.
func (l *Local) Edit(id string, fn func(*model.Asset)) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.entries[id]
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnknownAsset, id)
	}
	work := deep.Clone(e.Working)
	fn(&work)
	if err := work.Validate(); err != nil {
		return err
	}
	if work.ID != e.Working.ID {
		return fmt.Errorf("an edit may not change an asset's id")
	}
	e.Working = work
	return l.saveLocked(e)
}

// Pending is one asset's outstanding local work.
type Pending struct {
	ID          string
	BaseVersion int64
	Patch       deep.Patch[model.Asset]
}

// PendingFor returns one asset's outstanding changes.
func (l *Local) PendingFor(id string) (Pending, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.entries[id]
	if !ok {
		return Pending{}, fmt.Errorf("%w: %q", ErrUnknownAsset, id)
	}
	return l.pendingLocked(e)
}

func (l *Local) pendingLocked(e *entry) (Pending, error) {
	p, err := deep.Diff(e.Shadow, e.Working)
	if err != nil {
		return Pending{}, err
	}
	return Pending{ID: e.Working.ID, BaseVersion: e.BaseVersion, Patch: p}, nil
}

// Pending returns every asset with outstanding changes, ordered by id — the
// technician's outbox, derived rather than maintained.
func (l *Local) Pending() ([]Pending, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []Pending
	for _, e := range l.entries {
		p, err := l.pendingLocked(e)
		if err != nil {
			return nil, err
		}
		if !p.Patch.IsEmpty() {
			out = append(out, p)
		}
	}
	slices.SortFunc(out, func(a, b Pending) int { return strings.Compare(a.ID, b.ID) })
	return out, nil
}

// Known returns the version this device holds for every asset, which the
// server uses to decide what to send back.
func (l *Local) Known() map[string]int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make(map[string]int64, len(l.entries))
	for id, e := range l.entries {
		out[id] = e.BaseVersion
	}
	return out
}

// Revert throws away local changes to one asset — the working copy goes back
// to the shadow.
func (l *Local) Revert(id string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.entries[id]
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnknownAsset, id)
	}
	e.Working = deep.Clone(e.Shadow)
	return l.saveLocked(e)
}
