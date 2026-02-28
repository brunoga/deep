package crdt

import (
	"sort"
	"strings"

	"github.com/brunoga/deep/v5/crdt/hlc"
)

// TextRun represents a contiguous run of characters with a unique starting ID.
// Individual characters within the run have implicit IDs: ID + index.
type TextRun struct {
	ID      hlc.HLC `deep:"key" json:"id"`
	Value   string  `json:"v"`
	Prev    hlc.HLC `json:"p,omitempty"` // ID of the character this run follows
	Deleted bool    `json:"d,omitempty"` // Tombstone for CRDT convergence
}

// Text represents a CRDT-friendly text structure using runs.
type Text []TextRun

// String returns the full text content, skipping deleted runs.
func (t Text) String() string {
	var b strings.Builder
	for _, run := range t.getOrdered() {
		if !run.Deleted {
			b.WriteString(run.Value)
		}
	}
	return b.String()
}

// Insert inserts a string at the given character position.
func (t Text) Insert(pos int, value string, clock *hlc.Clock) Text {
	if value == "" {
		return t
	}

	// 1. Find the anchor (predecessor ID)
	prevID := t.findIDAt(pos - 1)

	// 2. Create the new run
	newRun := TextRun{
		ID:    clock.Reserve(len(value)),
		Value: value,
		Prev:  prevID,
	}

	// 3. If we are inserting in the middle of a run, we MUST split it
	// to maintain causal integrity.
	result := t.splitAt(pos)

	// 4. Add the new run. For now, we just append; the custom merge/render
	// will handle the ordering.
	result = append(result, newRun)

	return result.normalize()
}

// Delete removes length characters starting at pos by marking them as deleted.
func (t Text) Delete(pos, length int) Text {
	if length <= 0 {
		return t
	}

	// Split at boundaries to isolate the range
	result := t.splitAt(pos)
	result = result.splitAt(pos + length)

	// Mark runs in range as deleted
	currentPos := 0
	ordered := result.getOrdered()
	for i := range ordered {
		runLen := len(ordered[i].Value)
		if !ordered[i].Deleted {
			if currentPos >= pos && currentPos+runLen <= pos+length {
				ordered[i].Deleted = true
			}
			currentPos += runLen
		}
	}

	return ordered.normalize()
}

func (t Text) findIDAt(pos int) hlc.HLC {
	if pos < 0 {
		return hlc.HLC{}
	}

	currentPos := 0
	for _, run := range t.getOrdered() {
		if run.Deleted {
			continue
		}
		runLen := len(run.Value)
		if pos >= currentPos && pos < currentPos+runLen {
			id := run.ID
			id.Logical += int32(pos - currentPos)
			return id
		}
		currentPos += runLen
	}

	return hlc.HLC{}
}

func (t Text) splitAt(pos int) Text {
	if pos <= 0 {
		return t
	}

	ordered := t.getOrdered()
	currentPos := 0
	for i, run := range ordered {
		if run.Deleted {
			continue
		}
		runLen := len(run.Value)
		if pos > currentPos && pos < currentPos+runLen {
			offset := pos - currentPos

			// Original run is replaced by two parts
			left := TextRun{
				ID:      run.ID,
				Value:   run.Value[:offset],
				Prev:    run.Prev,
				Deleted: run.Deleted,
			}

			rightID := run.ID
			rightID.Logical += int32(offset)

			rightPrev := run.ID
			rightPrev.Logical += int32(offset - 1)

			right := TextRun{
				ID:      rightID,
				Value:   run.Value[offset:],
				Prev:    rightPrev,
				Deleted: run.Deleted,
			}

			// Mark original as "tombstoned" but we don't need the actual
			// tombstone if we are in a slice-based approach, UNLESS we
			// want to avoid the "Replace" conflict.

			// Actually, let's keep it simple: the split SHOULD be deterministic.
			// The problem is that the "Value" of run.ID is being changed
			// in two different ways by Node A and Node B.

			// Node A splits at 6: ID 1.0 becomes "word1 "
			// Node B splits at 18: ID 1.0 becomes "word1 word2 word3 "

			// If we merge these, one wins.
			// If Node B wins, ID 1.0 is "word1 word2 word3 ".
			// Then we have ID 1.6 (from A) which is "word2 word3 word4".
			// Result: "word1 word2 word3 " + "word2 word3 word4" -> DUPLICATION.

			newText := make(Text, 0, len(ordered)+1)
			newText = append(newText, ordered[:i]...)
			newText = append(newText, left, right)
			newText = append(newText, ordered[i+1:]...)
			return newText
		}
		currentPos += runLen
	}
	return t
}

// getOrdered returns the runs in their causal order.
func (t Text) getOrdered() Text {
	if len(t) <= 1 {
		return t
	}

	// Build adjacency list: PrevID -> [Runs]
	children := make(map[hlc.HLC][]TextRun)
	for _, run := range t {
		children[run.Prev] = append(children[run.Prev], run)
	}

	// Sort siblings by ID descending for deterministic convergence
	for _, runs := range children {
		sort.Slice(runs, func(i, j int) bool {
			return runs[i].ID.After(runs[j].ID)
		})
	}

	var result Text
	seen := make(map[hlc.HLC]bool)

	var walk func(hlc.HLC)
	walk = func(id hlc.HLC) {
		// A parent can be the start of a run OR any character within a run.
		// If 'id' is a character within a run, we should have already rendered
		// up to that character.

		// In this optimized implementation, we assume children only attach to
		// explicitly split boundaries or the very end of a run.
		// (Text.Insert and Text.splitAt ensure this).

		for _, run := range children[id] {
			if !seen[run.ID] {
				seen[run.ID] = true
				result = append(result, run)

				// After rendering this run, check for children attached to any of its characters.
				for i := 0; i < len(run.Value); i++ {
					charID := run.ID
					charID.Logical += int32(i)
					walk(charID)
				}
			}
		}
	}

	walk(hlc.HLC{}) // Start from root
	return result
}

// normalize merges adjacent runs if they are contiguous in ID and causality.
func (t Text) normalize() Text {
	ordered := t.getOrdered()
	if len(ordered) <= 1 {
		return ordered
	}

	result := make(Text, 0, len(ordered))
	result = append(result, ordered[0])

	for i := 1; i < len(ordered); i++ {
		lastIdx := len(result) - 1
		last := &result[lastIdx]
		curr := ordered[i]

		// Check if they can be merged:
		// 1. Same deletion status
		// 2. Contiguous IDs
		// 3. Current follows exactly the last char of previous
		expectedID := last.ID
		expectedID.Logical += int32(len(last.Value))

		prevID := last.ID
		prevID.Logical += int32(len(last.Value) - 1)

		if curr.Deleted == last.Deleted && curr.ID == expectedID && curr.Prev == prevID {
			last.Value += curr.Value
		} else {
			result = append(result, curr)
		}
	}

	return result
}
