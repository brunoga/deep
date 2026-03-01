package crdt

import (
	"sort"
	"strings"

	"github.com/brunoga/deep/v5"
	"github.com/brunoga/deep/v5/crdt/hlc"
)

// TextRun represents a contiguous run of characters with a unique starting ID.
type TextRun struct {
	ID      hlc.HLC `deep:"key" json:"id"`
	Value   string  `json:"v"`
	Prev    hlc.HLC `json:"p,omitempty"`
	Deleted bool    `json:"d,omitempty"`
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
	prevID := t.findIDAt(pos - 1)
	result := t.splitAt(pos)
	newRun := TextRun{
		ID:    clock.Reserve(len(value)),
		Value: value,
		Prev:  prevID,
	}
	result = append(result, newRun)
	return result.normalize()
}

// Delete removes length characters starting at pos.
func (t Text) Delete(pos, length int) Text {
	if length <= 0 {
		return t
	}
	result := t.splitAt(pos).splitAt(pos + length)
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

func (t Text) getOrdered() Text {
	if len(t) <= 1 {
		return t
	}
	children := make(map[hlc.HLC][]TextRun)
	for _, run := range t {
		children[run.Prev] = append(children[run.Prev], run)
	}
	for _, runs := range children {
		sort.Slice(runs, func(i, j int) bool {
			return runs[i].ID.After(runs[j].ID)
		})
	}
	var result Text
	seen := make(map[hlc.HLC]bool)
	var walk func(hlc.HLC)
	walk = func(id hlc.HLC) {
		for _, run := range children[id] {
			if !seen[run.ID] {
				seen[run.ID] = true
				result = append(result, run)
				for i := 0; i < len(run.Value); i++ {
					charID := run.ID
					charID.Logical += int32(i)
					walk(charID)
				}
			}
		}
	}
	walk(hlc.HLC{})
	return result
}

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

// Diff compares t with other and returns a Patch.
func (t Text) Diff(other Text) deep.Patch[Text] {
	if len(t) == len(other) {
		same := true
		for i := range t {
			if t[i] != other[i] {
				same = false
				break
			}
		}
		if same {
			return deep.Patch[Text]{}
		}
	}
	return deep.Patch[Text]{
		Operations: []deep.Operation{
			{Kind: deep.OpReplace, Path: "/", New: other},
		},
	}
}

// ApplyOperation implements the Applier interface for optimized patch application.
func (t *Text) ApplyOperation(op deep.Operation) (bool, error) {
	if op.Path == "" || op.Path == "/" {
		if other, ok := op.New.(Text); ok {
			*t = MergeTextRuns(*t, other)
			return true, nil
		}
	}
	return false, nil
}

// MergeTextRuns merges two Text states into a single convergent state.
func MergeTextRuns(a, b Text) Text {
	allRuns := append(a[:0:0], a...)
	allRuns = append(allRuns, b...)
	type baseID struct {
		WallTime int64
		NodeID   string
	}
	splits := make(map[baseID]map[int32]bool)
	for _, run := range allRuns {
		base := baseID{run.ID.WallTime, run.ID.NodeID}
		if splits[base] == nil {
			splits[base] = make(map[int32]bool)
		}
		splits[base][run.ID.Logical] = true
		splits[base][run.ID.Logical+int32(len(run.Value))] = true
	}
	combinedMap := make(map[hlc.HLC]TextRun)
	for _, run := range allRuns {
		base := baseID{run.ID.WallTime, run.ID.NodeID}
		relevantSplits := []int32{}
		for s := range splits[base] {
			if s > run.ID.Logical && s < run.ID.Logical+int32(len(run.Value)) {
				relevantSplits = append(relevantSplits, s)
			}
		}
		sort.Slice(relevantSplits, func(i, j int) bool { return relevantSplits[i] < relevantSplits[j] })
		currentLogical := run.ID.Logical
		currentValue := run.Value
		currentPrev := run.Prev
		for _, s := range relevantSplits {
			offset := int(s - currentLogical)
			id := run.ID
			id.Logical = currentLogical
			newRun := TextRun{ID: id, Value: currentValue[:offset], Prev: currentPrev, Deleted: run.Deleted}
			if existing, ok := combinedMap[id]; ok {
				if newRun.Deleted {
					existing.Deleted = true
				}
				combinedMap[id] = existing
			} else {
				combinedMap[id] = newRun
			}
			currentPrev = id
			currentPrev.Logical += int32(offset - 1)
			currentValue = currentValue[offset:]
			currentLogical = s
		}
		id := run.ID
		id.Logical = currentLogical
		newRun := TextRun{ID: id, Value: currentValue, Prev: currentPrev, Deleted: run.Deleted}
		if existing, ok := combinedMap[id]; ok {
			if newRun.Deleted {
				existing.Deleted = true
			}
			combinedMap[id] = existing
		} else {
			combinedMap[id] = newRun
		}
	}
	result := make(Text, 0, len(combinedMap))
	for _, run := range combinedMap {
		result = append(result, run)
	}
	return result.normalize()
}
