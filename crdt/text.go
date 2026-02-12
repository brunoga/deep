package crdt

import (
	"strings"

	"github.com/brunoga/deep/v2/crdt/hlc"
)

// TextRun represents a contiguous run of characters with a unique starting ID.
// Individual characters within the run have implicit IDs: ID + index.
type TextRun struct {
	ID    hlc.HLC `deep:"key" json:"id"`
	Value string  `json:"v"`
}

// Text represents a CRDT-friendly text structure using runs.
type Text []TextRun

// String returns the full text content.
func (t Text) String() string {
	var b strings.Builder
	for _, run := range t {
		b.WriteString(run.Value)
	}
	return b.String()
}

// Insert inserts a string at the given character position.
// It returns the updated Text and the split or new runs.
// This is a helper for local edits.
func (t Text) Insert(pos int, value string, clock *hlc.Clock) Text {
	if value == "" {
		return t
	}

	newRun := TextRun{
		ID:    clock.Reserve(len(value)),
		Value: value,
	}

	if len(t) == 0 {
		return Text{newRun}
	}

	result := make(Text, 0, len(t)+1)
	currentPos := 0
	inserted := false

	for _, run := range t {
		runLen := len(run.Value)
		if !inserted && pos >= currentPos && pos <= currentPos+runLen {
			// Split this run if necessary
			offset := pos - currentPos
			if offset > 0 {
				left := TextRun{
					ID:    run.ID,
					Value: run.Value[:offset],
				}
				result = append(result, left)
			}
			
			result = append(result, newRun)
			
			if offset < runLen {
				rightID := run.ID
				rightID.Logical += int32(offset)
				right := TextRun{
					ID:    rightID,
					Value: run.Value[offset:],
				}
				result = append(result, right)
			}
			inserted = true
		} else {
			result = append(result, run)
		}
		currentPos += runLen
	}

	if !inserted {
		result = append(result, newRun)
	}

	return result.normalize()
}

// Delete removes length characters starting at pos.
func (t Text) Delete(pos, length int) Text {
	if length <= 0 {
		return t
	}

	result := make(Text, 0, len(t))
	currentPos := 0

	for _, run := range t {
		runLen := len(run.Value)
		
		// Run is before the delete range
		if currentPos+runLen <= pos {
			result = append(result, run)
			currentPos += runLen
			continue
		}

		// Run is after the delete range
		if currentPos >= pos+length {
			result = append(result, run)
			currentPos += runLen
			continue
		}

		// Run overlaps with delete range
		startInRun := pos - currentPos
		if startInRun < 0 {
			startInRun = 0
		}
		
		endInRun := (pos + length) - currentPos
		if endInRun > runLen {
			endInRun = runLen
		}

		// Keep part before delete
		if startInRun > 0 {
			result = append(result, TextRun{
				ID:    run.ID,
				Value: run.Value[:startInRun],
			})
		}

		// Keep part after delete
		if endInRun < runLen {
			rightID := run.ID
			rightID.Logical += int32(endInRun)
			result = append(result, TextRun{
				ID:    rightID,
				Value: run.Value[endInRun:],
			})
		}

		currentPos += runLen
	}

	return result.normalize()
}

// normalize merges adjacent runs if they have contiguous IDs.
func (t Text) normalize() Text {
	if len(t) <= 1 {
		return t
	}

	result := make(Text, 0, len(t))
	result = append(result, t[0])

	for i := 1; i < len(t); i++ {
		lastIdx := len(result) - 1
		last := result[lastIdx]
		curr := t[i]

		// Check if contiguous
		expectedID := last.ID
		expectedID.Logical += int32(len(last.Value))

		if curr.ID == expectedID {
			result[lastIdx].Value += curr.Value
		} else {
			result = append(result, curr)
		}
	}

	return result
}
