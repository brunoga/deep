package crdt

import (
	"encoding/json"
	"log/slog"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/brunoga/deep/v5"
	"github.com/brunoga/deep/v5/crdt/hlc"
)

// Positions and lengths throughout this file are counted in runes, not bytes.
// A character ID is its run's ID advanced by the character's rune offset, so
// the two have to agree: counting bytes would hand out IDs that do not exist
// and let a split land inside a multi-byte sequence, corrupting the text.

// runeLen returns the number of runes in s.
func runeLen(s string) int { return utf8.RuneCountInString(s) }

// runeSlice returns s[from:to] with the bounds counted in runes.
func runeSlice(s string, from, to int) string {
	start, i := -1, 0
	for off := range s {
		if i == from {
			start = off
		}
		if i == to {
			return s[start:off]
		}
		i++
	}
	if start < 0 {
		if from == i {
			return ""
		}
		return s
	}
	return s[start:]
}

// TextRun represents a contiguous run of characters with a unique starting ID.
type TextRun struct {
	ID    hlc.HLC `deep:"key" json:"id"`
	Value string  `json:"v"`
	Prev  hlc.HLC `json:"p,omitempty"`
	// N is the number of runes in Value. Positions and identifiers are counted
	// in runes, so nearly every operation needs this number, and counting it
	// means walking the string — which for a document held as one long run
	// means walking the whole document, once per operation. Carrying the count
	// makes those operations depend on the number of runs instead of the length
	// of the text. A run without one (an older document, or a literal built by
	// hand) is counted on demand.
	N       int32 `json:"n,omitempty"`
	Deleted bool  `json:"d,omitempty"`
}

// runeCount returns the number of runes in the run's value.
func (r TextRun) runeCount() int {
	if r.N > 0 {
		return int(r.N)
	}
	return runeLen(r.Value)
}

// withValue returns a copy of the run holding v, with the rune count updated.
func (r TextRun) withValue(v string) TextRun {
	r.Value = v
	r.N = int32(runeLen(v))
	return r
}

// Text represents a CRDT-friendly text structure using runs.
//
// A Text held by this package is always stored in document order. Every
// operation here either preserves that order or restores it: Insert splices a
// run into the position the ordering would give it, Delete only flips flags,
// and [MergeTextRuns] — the one place runs arrive from elsewhere — derives the
// order from scratch. Operations can therefore read the runs directly instead
// of rebuilding the ordering tree, which is what keeps editing from costing
// more as a document grows.
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

// Len returns the number of visible characters, counted in runes — the same
// unit Insert and Delete take positions in.
func (t Text) Len() int {
	n := 0
	for _, run := range t {
		if !run.Deleted {
			n += run.runeCount()
		}
	}
	return n
}

// Insert inserts a string at the given character position.
func (t Text) Insert(pos int, value string, clock *hlc.Clock) Text {
	if value == "" {
		return t
	}
	// Order once and reuse it: locating the anchor and splitting both need the
	// document order, and deriving it twice per keystroke is what made typing
	// cost more as the document grew.
	ordered := t.getOrdered()
	prevID := ordered.findIDAt(pos - 1)
	result := ordered.splitAt(pos)
	newRun := TextRun{
		ID:    clock.ReserveSequence(runeLen(value)),
		Value: value,
		N:     int32(runeLen(value)),
		Prev:  prevID,
	}

	// Place the run where it belongs rather than appending it. The new run has
	// the newest ID, so among everything anchored to prevID it sorts first,
	// which puts it directly after the anchor — exactly where this splice puts
	// it. Keeping the slice in document order lets the ordering pass below take
	// its fast path instead of rebuilding the tree.
	at := len(result)
	if prevID == (hlc.HLC{}) {
		at = 0
	} else {
		for i, run := range result {
			last := run.ID
			last.Logical += int32(run.runeCount() - 1)
			if last == prevID {
				at = i + 1
				break
			}
		}
	}
	result = append(append(Text{}, result...), TextRun{})
	copy(result[at+1:], result[at:])
	result[at] = newRun

	return result.mergeAdjacent()
}

// Delete removes length characters starting at pos.
func (t Text) Delete(pos, length int) Text {
	if length <= 0 {
		return t
	}
	ordered := append(Text{}, t.getOrdered().splitAt(pos).splitAt(pos+length)...)
	currentPos := 0
	for i := range ordered {
		runLen := ordered[i].runeCount()
		if !ordered[i].Deleted {
			if currentPos >= pos && currentPos+runLen <= pos+length {
				ordered[i].Deleted = true
			}
			currentPos += runLen
		}
	}
	// Only flags changed, so the order still holds.
	return ordered.mergeAdjacent()
}

// findIDAt returns the ID of the character at pos. It expects t to be in
// document order.
func (t Text) findIDAt(pos int) hlc.HLC {
	if pos < 0 {
		return hlc.HLC{}
	}
	currentPos := 0
	for _, run := range t {
		if run.Deleted {
			continue
		}
		runLen := run.runeCount()
		if pos >= currentPos && pos < currentPos+runLen {
			id := run.ID
			id.Logical += int32(pos - currentPos)
			return id
		}
		currentPos += runLen
	}
	return hlc.HLC{}
}

// splitAt splits the run containing pos so that a run boundary falls there. It
// expects t to be in document order and returns runs in that same order.
func (t Text) splitAt(pos int) Text {
	if pos <= 0 {
		return t
	}
	ordered := t
	currentPos := 0
	for i, run := range ordered {
		if run.Deleted {
			continue
		}
		runLen := run.runeCount()
		if pos > currentPos && pos < currentPos+runLen {
			offset := pos - currentPos
			left := run.withValue(runeSlice(run.Value, 0, offset))
			rightID := run.ID
			rightID.Logical += int32(offset)
			rightPrev := run.ID
			rightPrev.Logical += int32(offset - 1)
			right := run.withValue(runeSlice(run.Value, offset, runLen))
			right.ID = rightID
			right.Prev = rightPrev
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
	if t.inSequence() {
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
	result := make(Text, 0, len(t))
	seen := make(map[hlc.HLC]bool, len(t))

	// Visit a run, then the runs anchored to each of its characters in turn.
	// Only characters that something is actually anchored to are worth looking
	// at: probing every character of every run costs a map lookup per character
	// of the document, which dominates once runs are long.
	var stack []TextRun
	push := func(anchor hlc.HLC) {
		group := children[anchor]
		for i := len(group) - 1; i >= 0; i-- {
			stack = append(stack, group[i])
		}
	}
	push(hlc.HLC{})
	for len(stack) > 0 {
		run := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[run.ID] {
			continue
		}
		seen[run.ID] = true
		result = append(result, run)

		n := run.runeCount()
		for i := n - 1; i >= 0; i-- {
			charID := run.ID
			charID.Logical += int32(i)
			if _, ok := children[charID]; ok {
				push(charID)
			}
		}
	}

	// Runs whose anchor never arrived would otherwise be dropped.
	if len(result) < len(t) {
		for _, run := range t {
			if !seen[run.ID] {
				seen[run.ID] = true
				result = append(result, run)
			}
		}
	}
	return result
}

// anchorFrame tracks one run while checking whether a sequence is in document
// order: the range of character IDs other runs may anchor to, how far through
// that range the check has advanced, and the last sibling seen at that point.
type anchorFrame struct {
	base    hlc.HLC // ID of the run's first character
	n       int32   // number of characters in the run
	cursor  int32   // character offset whose children are currently being read
	lastSib hlc.HLC
	hasSib  bool
}

// hosts reports the character offset of id within the frame's run.
func (f anchorFrame) hosts(id hlc.HLC) (int32, bool) {
	if id.WallTime != f.base.WallTime || id.NodeID != f.base.NodeID {
		return 0, false
	}
	off := id.Logical - f.base.Logical
	if off < 0 || off >= f.n {
		return 0, false
	}
	return off, true
}

// inSequence reports whether t is already in the order getOrdered would put it
// in, which is true of every Text this package produces — each operation either
// keeps the order or restores it. Deriving the order costs a map of every run
// plus a sort; confirming an existing one costs a single pass, so it is worth
// checking before doing the work.
//
// The check replays the walk against the stored sequence. Runs anchored to the
// same character must appear in descending ID order, and each run must anchor
// into a run still open at this point in the walk — the stack holds those, and
// leaving a run's subtree pops it. A sequence that satisfies both is the one
// the walk would produce.
func (t Text) inSequence() bool {
	// The virtual root: one character, the zero ID, which the first run anchors
	// to.
	stack := []anchorFrame{{n: 1}}
	for _, run := range t {
		for {
			top := &stack[len(stack)-1]
			if off, ok := top.hosts(run.Prev); ok && off >= top.cursor {
				if off > top.cursor {
					top.cursor, top.hasSib = off, false
				}
				if top.hasSib && !top.lastSib.After(run.ID) {
					return false // siblings out of order
				}
				top.lastSib, top.hasSib = run.ID, true
				break
			}
			if len(stack) == 1 {
				return false // anchored somewhere the walk would not be
			}
			stack = stack[:len(stack)-1]
		}
		stack = append(stack, anchorFrame{base: run.ID, n: int32(run.runeCount())})
	}
	return true
}

// normalize restores document order and then merges adjacent runs. Use it for
// runs that arrived from elsewhere; operations that already preserve order call
// mergeAdjacent directly.
func (t Text) normalize() Text {
	return t.getOrdered().mergeAdjacent()
}

// maxRunRunes bounds how long a single run may grow.
//
// Joining two runs concatenates their strings, which copies both. Left
// unbounded, appending one character to a document held as a single run copies
// the whole document, so typing it out costs time quadratic in its length even
// though the run count stays at one. Capping the length caps that copy: a
// document becomes a handful of runs rather than one, which costs a few bytes
// per chunk and makes typing linear. The value is a balance — small enough that
// a copy is cheap, large enough that the per-run overhead stays negligible.
const maxRunRunes = 4096

// canMergeRuns reports whether curr continues last exactly: the same deleted
// state, identifiers that carry straight on, curr anchored to last's final
// character, and a combined length within the cap. Two runs like that describe
// one stretch of text written in one go, and holding them as one is what keeps
// a document from carrying a run per keystroke.
func canMergeRuns(last, curr TextRun) bool {
	if curr.Deleted != last.Deleted {
		return false
	}
	if last.runeCount()+curr.runeCount() > maxRunRunes {
		return false
	}
	expectedID := last.ID
	expectedID.Logical += int32(last.runeCount())
	prevID := last.ID
	prevID.Logical += int32(last.runeCount() - 1)
	return curr.ID == expectedID && curr.Prev == prevID
}

// mergeAdjacent joins runs that sit next to each other and hold consecutive
// character IDs, so a document does not accumulate one run per edit. It assumes
// the runs are already in document order.
func (t Text) mergeAdjacent() Text {
	ordered := t
	if len(ordered) <= 1 {
		return ordered
	}
	result := make(Text, 0, len(ordered))
	result = append(result, ordered[0])
	for i := 1; i < len(ordered); i++ {
		lastIdx := len(result) - 1
		last := &result[lastIdx]
		curr := ordered[i]
		if canMergeRuns(*last, curr) {
			// The counts are taken before the values are joined: afterwards the
			// cached count on last no longer describes what it holds.
			joined := int32(last.runeCount() + curr.runeCount())
			last.Value += curr.Value
			last.N = joined
		} else {
			result = append(result, curr)
		}
	}
	return result
}

// MergeFrom implements [Convergent], so a Text merges with a peer's copy
// instead of one replacing the other under last-write-wins.
func (t Text) MergeFrom(other any) any {
	o, ok := other.(Text)
	if !ok {
		return t
	}
	return MergeTextRuns(t, o)
}

// Compact drops deleted runs that were removed at or before before, returning
// the remaining text.
//
// Deleting text does not reclaim it: the run stays as a tombstone, because a
// replica that has not heard about the deletion may still insert next to what
// was deleted, and the tombstone is what that insertion attaches to. A document
// edited for long enough is mostly tombstones — two hundred typed-then-deleted
// phrases leave two hundred runs behind nine visible characters.
//
// Dropping one is only safe once every replica has seen the deletion, so before
// must be a timestamp older than anything still in flight anywhere in the
// system; see [CRDT.Compact], which takes the same watermark for a replica's
// own bookkeeping.
//
// A tombstone that something is still anchored to is kept regardless of its
// age. Removing it would leave the runs anchored to it without a place, and
// re-anchoring them would change how this replica orders the document without
// changing how any other replica orders it — the two would then disagree.
// Keeping it costs a run and preserves the order exactly, and it becomes
// collectable once whatever anchored to it is itself deleted and compacted.
func (t Text) Compact(before hlc.HLC) Text {
	anchored := make(map[hlc.HLC]bool, len(t))
	for _, run := range t {
		anchored[run.Prev] = true
	}

	result := make(Text, 0, len(t))
	for _, run := range t {
		if run.Deleted && !run.ID.After(before) && !runAnchorsAnything(run, anchored) {
			continue
		}
		result = append(result, run)
	}
	if len(result) == len(t) {
		return t
	}
	return result.mergeAdjacent()
}

// CompactBefore implements [Compactable].
func (t Text) CompactBefore(before hlc.HLC) any { return t.Compact(before) }

// runAnchorsAnything reports whether any of the run's characters is something
// else's anchor.
func runAnchorsAnything(run TextRun, anchored map[hlc.HLC]bool) bool {
	n := run.runeCount()
	for i := 0; i < n; i++ {
		id := run.ID
		id.Logical += int32(i)
		if anchored[id] {
			return true
		}
	}
	return false
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

// Patch applies p to t.
func (t *Text) Patch(p deep.Patch[Text], logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}
	var errs []error
	for _, op := range p.Operations {
		if _, err := t.applyOperation(op, logger); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return &deep.ApplyError{Errors: errs}
	}
	return nil
}

func (t *Text) applyOperation(op deep.Operation, _ *slog.Logger) (bool, error) {
	if op.Path == "" || op.Path == "/" {
		if other, ok := op.New.(Text); ok {
			*t = MergeTextRuns(*t, other)
			return true, nil
		}
		// Handle JSON roundtrip: op.New arrives as []interface{} after JSON decode.
		if raw, ok := op.New.([]interface{}); ok {
			data, err := json.Marshal(raw)
			if err != nil {
				return false, err
			}
			var other Text
			if err := json.Unmarshal(data, &other); err != nil {
				return false, err
			}
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
	addSplit := func(base baseID, at int32) {
		if splits[base] == nil {
			splits[base] = make(map[int32]bool)
		}
		splits[base][at] = true
	}
	for _, run := range allRuns {
		base := baseID{run.ID.WallTime, run.ID.NodeID}
		addSplit(base, run.ID.Logical)
		addSplit(base, run.ID.Logical+int32(run.runeCount()))

		// A run anchored partway into another must leave that run divided at
		// the character it follows, so that it can be placed directly after it.
		// Without the division the character sits inside a run, the run is
		// emitted whole, and everything anchored to a character in its middle
		// lands after its end instead — the two sides then order the document
		// differently. A boundary only shows up on its own when some run
		// happens to end there, which is why this went unnoticed while replicas
		// exchanged whole documents and each supplied the other's boundaries.
		if run.Prev != (hlc.HLC{}) {
			addSplit(baseID{run.Prev.WallTime, run.Prev.NodeID}, run.Prev.Logical+1)
		}
	}
	combinedMap := make(map[hlc.HLC]TextRun)
	for _, run := range allRuns {
		base := baseID{run.ID.WallTime, run.ID.NodeID}
		relevantSplits := []int32{}
		for s := range splits[base] {
			if s > run.ID.Logical && s < run.ID.Logical+int32(run.runeCount()) {
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
			newRun := run.withValue(runeSlice(currentValue, 0, offset))
			newRun.ID = id
			newRun.Prev = currentPrev
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
			currentValue = runeSlice(currentValue, offset, runeLen(currentValue))
			currentLogical = s
		}
		id := run.ID
		id.Logical = currentLogical
		newRun := run.withValue(currentValue)
		newRun.ID = id
		newRun.Prev = currentPrev
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
