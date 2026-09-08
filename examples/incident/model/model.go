// Package model is commandpost's domain: the incident record every other
// piece of the system diffs, patches, audits and renders.
//
// The struct definitions double as the library showcase: a keyed task list, a
// custom enum-ish type, and two foreign types (time.Time, netip.Addr) whose
// insides belong to their own packages and are kept off limits with type
// families.
package model

//go:generate go run github.com/brunoga/deep/v6/cmd/deep-gen -type=Incident,Task -output model_deep.go .

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"reflect"
	"regexp"
	"time"

	deep "github.com/brunoga/deep/v6"
)

// Status is an incident's position in its lifecycle. The order matters:
// open → mitigated → resolved → closed, and the server refuses transitions
// that skip backwards past what a patch's own guards allow.
type Status string

const (
	StatusOpen      Status = "open"
	StatusMitigated Status = "mitigated"
	StatusResolved  Status = "resolved"
	StatusClosed    Status = "closed"
)

// Valid reports whether s is one of the defined statuses.
func (s Status) Valid() bool {
	switch s {
	case StatusOpen, StatusMitigated, StatusResolved, StatusClosed:
		return true
	}
	return false
}

// Severity ranks impact: 1 is the worst. The numeric order is what lets a
// patch escalate with a condition — "set severity to 1 If /severity > 1" is
// idempotent and can never de-escalate, no matter how late it arrives.
type Severity int

const (
	Sev1 Severity = 1 // all hands
	Sev2 Severity = 2 // major impact
	Sev3 Severity = 3 // partial impact
	Sev4 Severity = 4 // cosmetic
)

// Valid reports whether v is a defined severity.
func (v Severity) Valid() bool { return v >= Sev1 && v <= Sev4 }

func (v Severity) String() string { return fmt.Sprintf("SEV%d", int(v)) }

// Task is one item of the incident's checklist. ID is the element's identity:
// the `deep:"key"` tag makes the task list a keyed collection, so diffs match
// tasks by ID rather than by position — reordering produces no operations,
// paths read /tasks/<id>/done, and two responders editing different tasks can
// never collide.
type Task struct {
	ID    string `deep:"key" json:"id"`
	Text  string `json:"text"`
	Owner string `json:"owner,omitempty"`
	Done  bool   `json:"done,omitempty"`
}

// IDPattern constrains the identifiers that become path segments: an
// incident id names a directory and a websocket room, and a task id becomes a
// path token and travels back in error messages. Anything outside this
// alphabet is a traversal risk or a way to smuggle markup into whatever
// renders those messages.
var IDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

// Incident is the record itself. Nobody ever sends one of these whole after
// creation: every change is a deep.Patch[Incident] and the server replays the
// patch log to audit it.
type Incident struct {
	ID        string       `json:"id"`
	Title     string       `json:"title"`
	Severity  Severity     `json:"severity"`
	Status    Status       `json:"status"`
	Commander string       `json:"commander,omitempty"`
	Services  []string     `json:"services,omitempty"`
	Hosts     []netip.Addr `json:"hosts,omitempty"`
	Tasks     []Task       `json:"tasks,omitempty"`
	Updated   time.Time    `json:"updated"`
}

// The type families. Without them the reflection walker dives into the
// unexported fields of time.Time and netip.Addr and produces operations like
// "Replace /updated/ext" and "/hosts/0/addr/lo" — apply-able in-process, but
// implementation detail on the wire and meaningless to any other program. A
// family draws the boundary: these types are opaque, compared and carried in
// their own canonical forms.
func init() {
	deep.RegisterTypeFamily(deep.TypeFamily{
		Name:  "time",
		Match: func(t reflect.Type) bool { return t == reflect.TypeFor[time.Time]() },
		// time's own Equal, not ==: it treats a wall-clock instant as equal to
		// itself with or without the monotonic reading, which == does not.
		Equal: func(a, b any) bool { return a.(time.Time).Equal(b.(time.Time)) },
		Clone: func(v any) any { return v },
		Marshal: func(v any) ([]byte, error) {
			return json.Marshal(v.(time.Time).Format(time.RFC3339Nano))
		},
		Unmarshal: func(data []byte, _ reflect.Type) (any, error) {
			var s string
			if err := json.Unmarshal(data, &s); err != nil {
				return nil, err
			}
			return time.Parse(time.RFC3339Nano, s)
		},
	})

	deep.RegisterTypeFamily(deep.TypeFamily{
		Name:  "netip",
		Match: func(t reflect.Type) bool { return t == reflect.TypeFor[netip.Addr]() },
		Equal: func(a, b any) bool { return a.(netip.Addr) == b.(netip.Addr) },
		Clone: func(v any) any { return v },
		Marshal: func(v any) ([]byte, error) {
			return json.Marshal(v.(netip.Addr).String())
		},
		Unmarshal: func(data []byte, _ reflect.Type) (any, error) {
			var s string
			if err := json.Unmarshal(data, &s); err != nil {
				return nil, err
			}
			return netip.ParseAddr(s)
		},
	})
}
