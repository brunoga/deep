package crdt

import (
	"strconv"

	"github.com/brunoga/deep/v6/crdt/hlc"
)

// StateVector says how much of each origin's output a replica already holds.
//
// Every character a replica writes is identified by the node that wrote it and
// a counter that only goes up, so "everything node A wrote up to 500" describes
// a replica's knowledge of A completely, however that text was later split,
// merged or moved. A state vector is one such bound per origin, which is enough
// for a peer to work out exactly what to send back — see [Document.Since].
//
// It is small: one entry per node that has ever written, not per character.
type StateVector map[string]int32

// originOf identifies the space a character identifier was allocated from. A
// node allocates from a fresh space each time it starts, so the space is the
// node together with the point it started counting.
func originOf(id hlc.HLC) string {
	return id.NodeID + "@" + strconv.FormatInt(id.WallTime, 10)
}

// covers reports how many of the run's characters, counting from its start, the
// state vector already accounts for.
func (sv StateVector) covers(run TextRun) int {
	seen, ok := sv[originOf(run.ID)]
	if !ok {
		return 0
	}
	covered := int(seen - run.ID.Logical)
	if covered < 0 {
		return 0
	}
	if n := run.runeCount(); covered > n {
		return n
	}
	return covered
}

// Includes reports whether the state vector accounts for every character of the
// run.
func (sv StateVector) Includes(run TextRun) bool {
	return sv.covers(run) >= run.runeCount()
}

// DeletedRange marks a stretch of characters as removed, without carrying the
// text itself: a replica being told about the deletion already has the
// characters, and only needs to hear that they are gone.
type DeletedRange struct {
	ID hlc.HLC `json:"id"`
	N  int32   `json:"n"`
}

// contains reports whether id falls inside the range.
func (r DeletedRange) contains(id hlc.HLC) bool {
	if id.NodeID != r.ID.NodeID || id.WallTime != r.ID.WallTime {
		return false
	}
	off := id.Logical - r.ID.Logical
	return off >= 0 && off < r.N
}

// Update is what one replica sends another: the text the other does not have,
// and which characters have been deleted.
//
// Runs carries only what is missing, which after the first exchange is usually
// just what has been typed since. Deleted carries every deletion the sender
// knows about rather than only recent ones, because a deletion is not itself
// timestamped — it is a flag on text that already exists. The set stays small,
// since it holds one entry per deleted stretch rather than per character, and
// [Document.Compact] discards the ones every replica has seen.
type Update struct {
	Runs    Text           `json:"r,omitempty"`
	Deleted []DeletedRange `json:"d,omitempty"`
}

// IsEmpty reports whether the update carries nothing.
func (u Update) IsEmpty() bool { return len(u.Runs) == 0 && len(u.Deleted) == 0 }

// StateVector returns what this document holds, to hand to a peer so it can
// work out what to send.
//
// It is maintained as the document changes rather than derived on demand, so
// asking costs one entry per writer rather than a walk of every run.
func (d *Document) StateVector() StateVector {
	out := make(StateVector, len(d.sv))
	for k, v := range d.sv {
		out[k] = v
	}
	return out
}

// Since returns what a replica holding sv is missing.
//
// A run the peer holds entirely is left out; one it holds part of is trimmed to
// the part it does not. That is what makes syncing cost the size of the change
// rather than the size of the document.
func (d *Document) Since(sv StateVector) Update {
	var u Update
	for _, run := range d.Text() {
		if covered := sv.covers(run); covered == 0 {
			u.Runs = append(u.Runs, run)
		} else if covered < run.runeCount() {
			_, tail := splitRun(run, covered)
			u.Runs = append(u.Runs, tail)
		}
		if run.Deleted {
			u.Deleted = append(u.Deleted, DeletedRange{ID: run.ID, N: int32(run.runeCount())})
		}
	}
	return u
}

// Apply integrates an update from a peer.
func (d *Document) Apply(u Update) {
	if u.IsEmpty() {
		return
	}
	runs := d.Text()
	if len(u.Runs) > 0 {
		runs = MergeTextRuns(runs, u.Runs)
	}
	if len(u.Deleted) > 0 {
		runs = markDeleted(runs, u.Deleted)
	}
	d.reset(runs)
}

// markDeleted applies deletion ranges to runs, dividing a run when only part of
// it was deleted.
func markDeleted(runs Text, ranges []DeletedRange) Text {
	if len(ranges) == 0 {
		return runs
	}

	out := make(Text, 0, len(runs))
	for _, run := range runs {
		if run.Deleted {
			out = append(out, run)
			continue
		}
		out = append(out, splitByDeletion(run, ranges)...)
	}
	return out.mergeAdjacent()
}

// splitByDeletion divides a run wherever a deletion range starts or ends inside
// it, marking the parts that fall within one.
func splitByDeletion(run TextRun, ranges []DeletedRange) Text {
	n := run.runeCount()
	deleted := make([]bool, n)
	touched := false
	for i := 0; i < n; i++ {
		id := run.ID
		id.Logical += int32(i)
		for _, r := range ranges {
			if r.contains(id) {
				deleted[i] = true
				touched = true
				break
			}
		}
	}
	if !touched {
		return Text{run}
	}

	var out Text
	start := 0
	for i := 1; i <= n; i++ {
		if i == n || deleted[i] != deleted[start] {
			piece := run
			if start > 0 {
				_, piece = splitRun(run, start)
			}
			if i < n {
				piece, _ = splitRun(piece, i-start)
			}
			piece.Deleted = deleted[start]
			out = append(out, piece)
			start = i
		}
	}
	return out
}
