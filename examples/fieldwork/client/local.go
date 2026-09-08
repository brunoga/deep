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
	// Damaged names records that could not be read at startup and were set
	// aside (as <id>.json.corrupt) so the rest of the device still works.
	// They come back on the next sync as ordinary downloads.
	Damaged []string

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
		id := strings.TrimSuffix(filepath.Base(f), ".json")
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		var e entry
		// A record this device cannot read is set aside rather than allowed
		// to stop the whole device from starting: a technician with one
		// damaged file still has every other asset. The file name is the
		// authority on identity, so a record whose contents disagree with it
		// is damaged too — storing it under the embedded id would leave the
		// original file behind to reappear at the next start.
		if err := json.Unmarshal(data, &e); err != nil || e.Working.ID != id {
			quarantine := f + ".corrupt"
			if renameErr := os.Rename(f, quarantine); renameErr != nil {
				return nil, fmt.Errorf("unreadable record %s (and it could not be set aside: %w)", f, renameErr)
			}
			l.Damaged = append(l.Damaged, id)
			continue
		}
		l.entries[id] = &e
	}
	return l, nil
}

func (l *Local) path(id string) string { return filepath.Join(l.dir, id+".json") }

// saveLocked writes one entry durably: a temp file, flushed to the device
// before it is renamed into place, and the directory flushed after — so a
// battery pulled at any moment leaves either the previous record or the new
// one, never half of either. This is a field device; it will happen.
func (l *Local) saveLocked(e *entry) error {
	if !model.IDPattern.MatchString(e.Working.ID) {
		return fmt.Errorf("refusing to store asset with id %q", e.Working.ID)
	}
	data, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return err
	}
	tmp := l.path(e.Working.ID) + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, l.path(e.Working.ID)); err != nil {
		return err
	}
	dir, err := os.Open(l.dir)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

// Adopt records an authoritative state from the server: shadow and working
// both become it. This is what a sync result installs — the technician's
// device now agrees with the server, conflicts and all.
func (l *Local) Adopt(a model.Asset, version int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	e := &entry{Shadow: deep.Clone(a), Working: deep.Clone(a), BaseVersion: version}
	// Disk first: a failed write must not leave memory claiming a state the
	// device would not have after a restart.
	if err := l.saveLocked(e); err != nil {
		return err
	}
	l.entries[a.ID] = e
	return nil
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
	next := &entry{Shadow: e.Shadow, Working: work, BaseVersion: e.BaseVersion}
	if err := l.saveLocked(next); err != nil {
		return err
	}
	*e = *next
	return nil
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
	next := &entry{Shadow: e.Shadow, Working: deep.Clone(e.Shadow), BaseVersion: e.BaseVersion}
	if err := l.saveLocked(next); err != nil {
		return err
	}
	*e = *next
	return nil
}
