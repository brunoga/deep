// Package model is fieldwork's record: the asset a technician inspects in
// the field and dispatch manages from the office. Both sides edit copies of
// it, offline and online, and the sync engine's whole job is reconciling
// those copies — so the shapes here are chosen to give every kind of
// concurrent edit somewhere to happen: scalar fields for head-on conflicts,
// a map for per-entry independence, a keyed slice for identity-addressed
// checklist items.
package model

//go:generate go run github.com/brunoga/deep/v6/cmd/deep-gen -type=Asset,Reading,Check -output model_deep.go .

import (
	"fmt"
	"regexp"
	"strings"
)

// Status ranks an asset's health. The order matters: when the field and the
// office disagree, the sync policy keeps the worse one — a fault seen by
// either is a fault.
type Status string

const (
	StatusOK        Status = "ok"
	StatusAttention Status = "attention"
	StatusFault     Status = "fault"
	StatusOffline   Status = "offline"
)

// severity orders statuses worst-last for comparison; unknown is worst of
// all, so garbage never wins a merge.
var severity = map[Status]int{
	StatusOK: 0, StatusAttention: 1, StatusFault: 2, StatusOffline: 3,
}

// Valid reports whether s is a defined status.
func (s Status) Valid() bool {
	_, ok := severity[s]
	return ok
}

// Worse returns the more severe of two statuses. An unknown status ranks
// worst — never silently outranked by data we do understand.
func Worse(a, b Status) Status {
	sa, aok := severity[a]
	sb, bok := severity[b]
	switch {
	case !aok:
		return a
	case !bok:
		return b
	case sa >= sb:
		return a
	}
	return b
}

// Reading is one measurement taken at the asset. Readings live in a map by
// sensor name, so two technicians measuring different sensors never touch
// the same path — and thanks to per-field map diffs, even edits to the same
// sensor conflict only on the fields that actually differ.
type Reading struct {
	Value float64 `json:"value"`
	Unit  string  `json:"unit,omitempty"`
	By    string  `json:"by,omitempty"`
}

// Check is one checklist item, keyed by ID: reordering is meaningless and
// two techs completing different items never collide.
type Check struct {
	ID    string `deep:"key" json:"id"`
	Label string `json:"label"`
	Done  bool   `json:"done,omitempty"`
	By    string `json:"by,omitempty"`
}

// Asset is the record itself.
type Asset struct {
	ID       string             `json:"id"`
	Name     string             `json:"name"`
	Site     string             `json:"site"`
	Status   Status             `json:"status"`
	Assignee string             `json:"assignee,omitempty"`
	Readings map[string]Reading `json:"readings,omitempty"`
	Checks   []Check            `json:"checks,omitempty"`
	Notes    string             `json:"notes,omitempty"`
}

// IDPattern constrains asset, sensor and check ids: they become path
// segments and file names.
var IDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

// Validate checks structural sanity — for records arriving from outside.
func (a *Asset) Validate() error {
	if !IDPattern.MatchString(a.ID) {
		return fmt.Errorf("model: bad asset id %q", a.ID)
	}
	if strings.TrimSpace(a.Name) == "" {
		return fmt.Errorf("model: asset %s has no name", a.ID)
	}
	if !a.Status.Valid() {
		return fmt.Errorf("model: asset %s has status %q", a.ID, a.Status)
	}
	for sensor := range a.Readings {
		if !IDPattern.MatchString(sensor) {
			return fmt.Errorf("model: asset %s has bad sensor id %q", a.ID, sensor)
		}
	}
	seen := map[string]bool{}
	for _, c := range a.Checks {
		if !IDPattern.MatchString(c.ID) {
			return fmt.Errorf("model: asset %s has bad check id %q", a.ID, c.ID)
		}
		if seen[c.ID] {
			return fmt.Errorf("model: asset %s has duplicate check %q", a.ID, c.ID)
		}
		seen[c.ID] = true
	}
	return nil
}
