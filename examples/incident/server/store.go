// Package server is commandpost's authority: it owns the incidents, applies
// every patch, keeps the audit log, and persists the lot to a plain
// directory.
package server

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	deep "github.com/brunoga/deep/v6"

	"github.com/brunoga/deep/examples/incident/model"
)

var (
	// ErrNotFound reports an incident ID the store does not hold.
	ErrNotFound = errors.New("incident not found")
	// ErrConflict reports a patch the current state refused: a guard not met,
	// a strict Old that no longer matches, an operation that failed.
	ErrConflict = errors.New("patch conflicts with current state")
	// ErrValidation reports a patch whose result would not be a legal
	// incident. The patch applied cleanly; the outcome is what is wrong.
	ErrValidation = errors.New("resulting incident is invalid")
)

// editablePaths is the allowlist handed to every apply: a patch may touch
// these subtrees and nothing else. /id and /updated are absent on purpose —
// identity is immutable and the freshness stamp is the server's to write.
var editablePaths = []string{
	"/title", "/severity", "/status", "/commander", "/services", "/hosts", "/tasks",
}

// LogEntry is one audited change. Patch is the canonical form of what
// happened — re-derived by the server as Diff(before, after), so it carries
// full Old and New values regardless of what the client sent, applies without
// conditions, and reverses exactly.
type LogEntry struct {
	Seq    int64                      `json:"seq"`
	Author string                     `json:"author"`
	Time   time.Time                  `json:"time"`
	Note   string                     `json:"note,omitempty"`
	Patch  deep.Patch[model.Incident] `json:"patch"`
}

// Outcome mirrors one operation's fate in JSON-friendly form.
type Outcome struct {
	Path   string `json:"path"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// Result reports what a submitted patch did. Seq is zero when nothing
// changed — every operation skipped by its conditions, or a no-op write.
type Result struct {
	Seq      int64     `json:"seq,omitempty"`
	Applied  int       `json:"applied"`
	Skipped  int       `json:"skipped"`
	Failed   int       `json:"failed"`
	Outcomes []Outcome `json:"outcomes,omitempty"`
}

type record struct {
	inc *model.Incident
	log []LogEntry
}

// Store holds every incident and its audit log, mirrored to a directory.
type Store struct {
	mu      sync.Mutex
	dir     string
	records map[string]*record

	// now is the clock, injectable for tests.
	now func() time.Time
}

// Open loads a store from dir, creating it empty if the directory does not
// exist yet.
func Open(dir string) (*Store, error) {
	s := &Store{dir: dir, records: map[string]*record{}, now: time.Now}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		rec, err := loadRecord(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("loading incident %s: %w", e.Name(), err)
		}
		s.records[rec.inc.ID] = rec
	}
	return s, nil
}

func loadRecord(dir string) (*record, error) {
	data, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		return nil, err
	}
	var inc model.Incident
	if err := json.Unmarshal(data, &inc); err != nil {
		return nil, err
	}
	rec := &record{inc: &inc}

	f, err := os.Open(filepath.Join(dir, "log.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return rec, nil
		}
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		var entry LogEntry
		if err := json.Unmarshal(sc.Bytes(), &entry); err != nil {
			return nil, err
		}
		rec.log = append(rec.log, entry)
	}
	return rec, sc.Err()
}

func (s *Store) incidentDir(id string) string { return filepath.Join(s.dir, id) }

// persistState writes the incident snapshot; persistEntry appends one log
// line. Both are called with the store lock held. The snapshot goes through
// a temp file and a rename so a crash mid-write can never leave a truncated
// state.json behind.
func (s *Store) persistState(inc *model.Incident) error {
	dir := s.incidentDir(inc.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(inc, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, "state.json.tmp")
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, "state.json"))
}

func (s *Store) persistEntry(id string, entry LogEntry) error {
	f, err := os.OpenFile(filepath.Join(s.incidentDir(id), "log.jsonl"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	_, err = f.Write(append(data, '\n'))
	return err
}

func (s *Store) rewriteLog(id string, log []LogEntry) error {
	var b strings.Builder
	for _, entry := range log {
		data, err := json.Marshal(entry)
		if err != nil {
			return err
		}
		b.Write(data)
		b.WriteByte('\n')
	}
	return os.WriteFile(filepath.Join(s.incidentDir(id), "log.jsonl"), []byte(b.String()), 0o644)
}

// Create adds a new incident. The record starts life whole; everything after
// this is patches.
func (s *Store) Create(inc model.Incident) error {
	if err := validate(&inc); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.records[inc.ID]; ok {
		return fmt.Errorf("%w: incident %q already exists", ErrConflict, inc.ID)
	}
	inc.Updated = s.now()
	clone := deep.Clone(inc)
	if err := s.persistState(&clone); err != nil {
		return err
	}
	s.records[inc.ID] = &record{inc: &clone}
	return nil
}

// Get returns a copy of one incident: the caller can hold it, mutate it,
// diff against it, and never touch the store's own state.
func (s *Store) Get(id string) (model.Incident, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[id]
	if !ok {
		return model.Incident{}, fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	return deep.Clone(*rec.inc), nil
}

// List returns copies of all incidents, ordered by ID.
func (s *Store) List() []model.Incident {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]model.Incident, 0, len(s.records))
	for _, rec := range s.records {
		out = append(out, deep.Clone(*rec.inc))
	}
	slices.SortFunc(out, func(a, b model.Incident) int { return strings.Compare(a.ID, b.ID) })
	return out
}

// History returns the audit log for one incident, oldest first.
func (s *Store) History(id string) ([]LogEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[id]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	return slices.Clone(rec.log), nil
}

// ApplyPatch runs a submitted patch against one incident. The patch executes
// on a clone; only a result that survives validation is swapped in, so a
// failure of any kind leaves the incident untouched. What lands in the audit
// log is not the submission but the canonical diff of what actually changed.
func (s *Store) ApplyPatch(id, author string, p deep.Patch[model.Incident]) (Result, error) {
	return s.applyPatch(id, author, "", p)
}

func (s *Store) applyPatch(id, author, note string, p deep.Patch[model.Incident]) (Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[id]
	if !ok {
		return Result{}, fmt.Errorf("%w: %q", ErrNotFound, id)
	}

	work := deep.Clone(*rec.inc)
	res, err := deep.ApplyWithResult(&work, p, deep.WithAllowedPaths(editablePaths...))
	out := toResult(res)
	if err != nil {
		// Whatever partially happened, happened to the clone; the incident is
		// untouched. Guards, strict mismatches and failed operations are all
		// conflicts with the current state.
		return out, fmt.Errorf("%w: %v", ErrConflict, err)
	}
	if err := validate(&work); err != nil {
		return out, err
	}

	// The canonical change, derived before the freshness stamp so it holds
	// exactly what the caller's patch did and nothing of the server's.
	canonical, err := deep.Diff(*rec.inc, work)
	if err != nil {
		return out, err
	}
	if canonical.IsEmpty() {
		return out, nil // all skipped or writes of what was already there
	}

	work.Updated = s.now()
	entry := LogEntry{
		Seq:    nextSeq(rec.log),
		Author: author,
		Time:   work.Updated,
		Note:   note,
		Patch:  canonical,
	}

	// Disk first, memory second: an error here reports failure for a change
	// that truly did not take effect, instead of a 500 for one already live.
	// The entry goes before the snapshot so a crash between the two writes
	// leaves an audit line whose change the state does not show — over-
	// reporting history — rather than a state change no log entry explains.
	if err := s.persistEntry(id, entry); err != nil {
		return out, err
	}
	if err := s.persistState(&work); err != nil {
		return out, err
	}
	rec.inc = &work
	rec.log = append(rec.log, entry)
	out.Seq = entry.Seq
	return out, nil
}

func nextSeq(log []LogEntry) int64 {
	if len(log) == 0 {
		return 1
	}
	return log[len(log)-1].Seq + 1
}

// Undo reverses one audit-log entry, as a new strict entry: the log never
// rewrites, and if the fields that entry touched have since moved on, the
// strict Old checks refuse rather than clobber.
func (s *Store) Undo(id, author string, seq int64) (Result, error) {
	s.mu.Lock()
	var target LogEntry
	found := false
	if rec, ok := s.records[id]; ok {
		for _, entry := range rec.log {
			if entry.Seq == seq {
				target, found = entry, true
				break
			}
		}
	}
	s.mu.Unlock()
	if !found {
		return Result{}, fmt.Errorf("%w: incident %q has no log entry %d", ErrNotFound, id, seq)
	}
	reverse := target.Patch.Reverse().AsStrict()
	return s.applyPatch(id, author, fmt.Sprintf("undo of #%d", seq), reverse)
}

// Compact collapses all but the last keep audit entries into a single
// baseline entry, merged oldest-to-newest so later writes win. It trades undo
// granularity for size: the merged entry records where each path ended up,
// not every step it took.
func (s *Store) Compact(id string, keep int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[id]
	if !ok {
		return fmt.Errorf("%w: %q", ErrNotFound, id)
	}
	if keep < 0 {
		keep = 0
	}
	if len(rec.log) <= keep+1 {
		return nil // nothing to collapse
	}
	head := rec.log[:len(rec.log)-keep]
	tail := rec.log[len(rec.log)-keep:]

	merged := head[0].Patch
	for _, entry := range head[1:] {
		merged = deep.Merge(merged, entry.Patch, nil) // nil resolver: later wins
	}
	base := LogEntry{
		Seq:    head[len(head)-1].Seq,
		Author: "compaction",
		Time:   s.now(),
		Note:   fmt.Sprintf("compacted #%d..#%d", head[0].Seq, head[len(head)-1].Seq),
		Patch:  merged,
	}
	rec.log = append([]LogEntry{base}, tail...)
	return s.rewriteLog(id, rec.log)
}

// idPattern is the shape of every identifier that reaches the filesystem or
// a path segment. The incident ID names a directory and a websocket room;
// anything beyond this alphabet is a traversal risk ("../../etc") or a
// reload-keying hazard, not a name.
var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

// validate is the rulebook patch guards cannot express: it judges the whole
// resulting incident, not one path.
func validate(inc *model.Incident) error {
	if !idPattern.MatchString(inc.ID) {
		return fmt.Errorf("%w: id %q (want letters, digits, '_', '.', '-'; max 64)", ErrValidation, inc.ID)
	}
	if strings.TrimSpace(inc.Title) == "" {
		return fmt.Errorf("%w: empty title", ErrValidation)
	}
	if !inc.Severity.Valid() {
		return fmt.Errorf("%w: severity %d", ErrValidation, inc.Severity)
	}
	if !inc.Status.Valid() {
		return fmt.Errorf("%w: status %q", ErrValidation, inc.Status)
	}
	seen := map[string]bool{}
	for _, task := range inc.Tasks {
		if task.ID == "" {
			return fmt.Errorf("%w: task with empty id", ErrValidation)
		}
		if seen[task.ID] {
			return fmt.Errorf("%w: duplicate task id %q", ErrValidation, task.ID)
		}
		seen[task.ID] = true
		if inc.Status == model.StatusClosed && !task.Done {
			return fmt.Errorf("%w: cannot be closed with open task %q", ErrValidation, task.ID)
		}
	}
	return nil
}

func toResult(res *deep.ApplyResult) Result {
	if res == nil {
		return Result{}
	}
	applied, skipped, failed := res.Counts()
	out := Result{Applied: applied, Skipped: skipped, Failed: failed}
	for _, o := range res.Outcomes {
		oc := Outcome{Path: o.Path, Status: o.Status.String()}
		if o.Err != nil {
			oc.Error = o.Err.Error()
		}
		out.Outcomes = append(out.Outcomes, oc)
	}
	return out
}
