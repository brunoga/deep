// Package external holds models whose fields refer to types declared outside
// the generated package. It exercises deep-gen's type resolution and import
// emission; the checked-in job_deep.go is compiled as part of the build, so a
// generator that emits invalid code for these types breaks the build.
package external

//go:generate go run github.com/brunoga/deep/v5/cmd/deep-gen -type=Job,Stage -output job_deep.go .

import (
	"time"
	clock "time"

	"github.com/brunoga/deep/v5/crdt"
)

// Priority is a named type over a builtin: it has no generated methods.
type Priority int

// Stage is a struct in this package, so it does have generated methods.
type Stage struct {
	Name string    `json:"name"`
	At   time.Time `json:"at"`
}

type Job struct {
	StartAt  *time.Time            `json:"startAt"`
	Deadline time.Time             `json:"deadline"`
	Timeout  time.Duration         `json:"timeout"`
	Window   []time.Time           `json:"window"`
	Retries  map[string]*time.Time `json:"retries"`
	Stages   []Stage               `json:"stages"`
	Owners   map[string]*Stage     `json:"owners"`
	Priority Priority              `json:"priority"`
	Notes    crdt.Text             `json:"notes"`
	Title    crdt.LWW[string]      `json:"title"`
	Checked  clock.Time            `json:"checked"`
	Grid     [2]int                `json:"grid"`
	Done     chan struct{}         `json:"done"`
}
