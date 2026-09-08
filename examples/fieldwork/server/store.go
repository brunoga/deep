// Package server is fieldwork's authority: versioned asset records, a
// canonical patch log per record, and the sync engine that reconciles
// offline edits against them.
package server

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	deep "github.com/brunoga/deep/v6"

	"github.com/brunoga/deep/examples/fieldwork/model"
)

var (
	// ErrNotFound reports an asset the store does not hold.
	ErrNotFound = errors.New("asset not found")
	// ErrBadVersion reports a version the record's log cannot reach.
	ErrBadVersion = errors.New("no such version")
	// ErrRejected reports a change whose outcome broke validation; nothing
	// was stored.
	ErrRejected = errors.New("change rejected")
)

// VersionEntry is one accepted change: the canonical diff that took the
// record from version-1 to version. The log is what makes time travel work —
// any past state is a reverse-walk away, which is exactly what the sync
// engine needs to know what the office changed while a technician was in
// the field.
type VersionEntry struct {
	Version int64                    `json:"version"`
	Author  string                   `json:"author"`
	Time    time.Time                `json:"time"`
	Patch   deep.Patch[model.Asset] `json:"patch"`
}

type record struct {
	asset   *model.Asset
	version int64
	log     []VersionEntry
}

// Store holds every asset with its version history.
type Store struct {
	mu      sync.Mutex
	records map[string]*record

	// now is the clock, injectable for tests.
	now func() time.Time
}

// NewStore returns an empty store.
func NewStore() *Store {
	return &Store{records: map[string]*record{}, now: time.Now}
}

// Create registers a new asset at version 1, returning it with its version
// so a caller never has to re-read (and race) for the pair.
func (s *Store) Create(a model.Asset) (model.Asset, int64, error) {
	if err := a.Validate(); err != nil {
		return model.Asset{}, 0, fmt.Errorf("%w: %w", ErrRejected, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.records[a.ID]; ok {
		return model.Asset{}, 0, fmt.Errorf("%w: asset %q already exists", ErrRejected, a.ID)
	}
	clone := deep.Clone(a)
	s.records[a.ID] = &record{asset: &clone, version: 1}
	return deep.Clone(clone), 1, nil
}

// Get returns a copy of one asset and its version.
func (s *Store) Get(id string) (model.Asset, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[id]
	if !ok {
		return model.Asset{}, 0, fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	return deep.Clone(*rec.asset), rec.version, nil
}

// List returns copies of every asset with versions, ordered by ID.
func (s *Store) List() ([]model.Asset, map[string]int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	assets := make([]model.Asset, 0, len(s.records))
	versions := make(map[string]int64, len(s.records))
	for id, rec := range s.records {
		assets = append(assets, deep.Clone(*rec.asset))
		versions[id] = rec.version
	}
	slices.SortFunc(assets, func(a, b model.Asset) int { return strings.Compare(a.ID, b.ID) })
	return assets, versions
}

// History returns a record's version log, oldest first.
func (s *Store) History(id string) ([]VersionEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[id]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	// Deep copy: a shallow clone would hand out the operation slices the
	// authoritative log reverse-walks through, and a caller that edited one
	// would corrupt every future version reconstruction.
	return deep.Clone(rec.log), nil
}

// Change applies a patch directly — the online path dispatch uses. The patch
// runs on a clone; only a validated outcome is stored, as a canonical diff
// with a new version. The stored asset comes back with the version it was
// stored at, under the same lock, so the two always describe each other.
func (s *Store) Change(id, author string, p deep.Patch[model.Asset]) (model.Asset, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[id]
	if !ok {
		return model.Asset{}, 0, fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	work := deep.Clone(*rec.asset)
	if err := deep.Apply(&work, p, deep.WithAllowedPaths(editablePaths...)); err != nil {
		return model.Asset{}, 0, fmt.Errorf("%w: %w", ErrRejected, err)
	}
	version, err := s.commitLocked(rec, author, work)
	if err != nil {
		return model.Asset{}, 0, err
	}
	return deep.Clone(*rec.asset), version, nil
}

// editablePaths: /id is identity and never patched.
var editablePaths = []string{
	"/name", "/site", "/status", "/assignee", "/readings", "/checks", "/notes",
}

// commitLocked validates work, derives the canonical diff from the current
// state, and advances the record. Held lock required.
func (s *Store) commitLocked(rec *record, author string, work model.Asset) (int64, error) {
	if err := work.Validate(); err != nil {
		return 0, fmt.Errorf("%w: %w", ErrRejected, err)
	}
	canonical, err := deep.Diff(*rec.asset, work)
	if err != nil {
		return 0, err
	}
	if canonical.IsEmpty() {
		return rec.version, nil // a no-op change spends no version
	}
	rec.version++
	rec.log = append(rec.log, VersionEntry{
		Version: rec.version,
		Author:  author,
		Time:    s.now(),
		Patch:   canonical,
	})
	rec.asset = &work
	return rec.version, nil
}

// stateAtLocked reconstructs the record as it stood at version, by reversing
// canonical entries back from the current state.
func (s *Store) stateAtLocked(rec *record, version int64) (model.Asset, error) {
	if version > rec.version || version < rec.version-int64(len(rec.log)) {
		return model.Asset{}, fmt.Errorf("%w: %d (record is at %d with %d logged changes)",
			ErrBadVersion, version, rec.version, len(rec.log))
	}
	past := deep.Clone(*rec.asset)
	for i := len(rec.log) - 1; i >= 0 && rec.log[i].Version > version; i-- {
		if err := deep.Apply(&past, rec.log[i].Patch.Reverse()); err != nil {
			return model.Asset{}, fmt.Errorf("reversing to version %d: %w", version, err)
		}
	}
	return past, nil
}

// ChangesSince returns one patch describing everything that happened to the
// record after version — the boundary-state diff, which is exactly the
// "their side" of a three-way merge.
func (s *Store) ChangesSince(id string, version int64) (deep.Patch[model.Asset], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[id]
	if !ok {
		return deep.Patch[model.Asset]{}, fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	past, err := s.stateAtLocked(rec, version)
	if err != nil {
		return deep.Patch[model.Asset]{}, err
	}
	return deep.Diff(past, *rec.asset)
}
