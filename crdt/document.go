package crdt

import (
	"encoding/json"
	"log/slog"
	"strings"
	"sync/atomic"

	deep "github.com/brunoga/deep/v6"
	"github.com/brunoga/deep/v6/crdt/hlc"
)

// Document is a collaborative text document, holding the same runs as [Text]
// but indexed for editing rather than stored as a plain slice.
//
// Text is a value: every edit copies its runs, and finding a position walks
// them. That is fine for a document edited in a few places, and the cost grows
// with the number of runs — which grows as edits are scattered around. Document
// keeps the runs in a balanced tree ordered by position, so finding a position
// and editing there cost logarithmic rather than linear time in the number of
// runs, and an edit does not copy the document.
//
// The tree is only an index. Which order the runs are in is decided the same
// way as in Text, by the run each one is anchored to, so the two agree on the
// text they describe and serialize to the same thing. A Document can be built
// from a Text and back again, and a replica running one converges with a
// replica running the other.
//
// A Document is mutable and is not safe for concurrent use; guard it, or keep
// it inside a [CRDT], which does.
//
// Two documents edited independently are two replicas, and each needs its own
// clock. What one replica holds of another is described by a single bound per
// writer, which only means anything if a writer's output goes to one document:
// two documents drawing from one clock produce interleaved identifiers that no
// such bound can describe, and a sync between them would appear complete while
// leaving text behind. [Document.Copy] shares a clock deliberately, because a
// copy stands in for the document it came from rather than alongside it.
//
// # Which to use
//
// Hold a Document directly for a large collaborative document — a shared
// editor, a long note — and exchange its runs with peers through MergeFrom. An
// edit costs the same whether the document holds a hundred runs or ten
// thousand, where a Text takes proportionally longer as the runs pile up:
// fifty edits to an eight-thousand-run document take microseconds against tens
// of milliseconds.
//
// A Document also works inside a [CRDT], and converges there, but does not
// bring its speed with it. [CRDT.Edit] copies the value and compares the copy
// to work out what changed, which costs time proportional to the size of the
// document however small the edit — the wrapper's doing, not the document's,
// and a [Text] pays it too. Put a Document in a [CRDT] when it sits alongside
// other fields that need the wrapper's conflict resolution; hold it directly
// when the document is the thing being edited.
type Document struct {
	root  *docNode
	clock *hlc.Clock
	rng   uint32
	// sv is kept up to date as the document changes rather than derived when
	// asked. Deriving it means walking every run, and it is asked for on every
	// edit a [CRDT] makes — to say what the edit changed, the wrapper needs to
	// know where the document stood before it. Keeping it costs one map entry
	// per writer.
	sv StateVector
	// pending is what this document has changed since it was made, copied or
	// merged. A document knows what it changed as it changes; noting it down
	// costs nothing and saves rediscovering it by comparison, which means
	// reading every run. Copy starts a fresh record, so the copy an edit is
	// made on ends up holding exactly that edit.
	pending Update
	// deleted counts the characters deleted so far, which is what says whether
	// the pending record accounts for every deletion as well as every addition.
	deleted int
}

// docNode is one run, together with the totals for the subtree beneath it. The
// totals are what make a position lookup logarithmic: descending the tree, the
// visible length of a subtree says whether the position sought lies inside it.
type docNode struct {
	run         TextRun
	left, right *docNode
	priority    uint32
	size        int // runs in this subtree
	visible     int // visible runes in this subtree
}

var documentSeed atomic.Uint32

// NewDocument returns an empty document that draws identifiers from clock.
func NewDocument(clock *hlc.Clock) *Document {
	return &Document{
		clock: clock,
		rng:   documentSeed.Add(0x9E3779B9) | 1,
		sv:    make(StateVector),
	}
}

// DocumentFromText returns a document holding the runs of t.
func DocumentFromText(t Text, clock *hlc.Clock) *Document {
	d := NewDocument(clock)
	d.reset(t)
	return d
}

// Text returns the document's runs in order, in the form [Text] uses. This is
// the document's whole state: it serializes, merges and compacts as a Text.
func (d *Document) Text() Text {
	if d == nil || d.root == nil {
		return Text{}
	}
	out := make(Text, 0, d.root.size)
	var walk func(*docNode)
	walk = func(n *docNode) {
		if n == nil {
			return
		}
		walk(n.left)
		out = append(out, n.run)
		walk(n.right)
	}
	walk(d.root)
	return out
}

// Clock returns the clock the document draws identifiers from.
func (d *Document) Clock() *hlc.Clock { return d.clock }

// Len returns the number of visible characters, in runes.
func (d *Document) Len() int {
	if d == nil || d.root == nil {
		return 0
	}
	return d.root.visible
}

// String returns the visible text.
func (d *Document) String() string {
	if d == nil || d.root == nil {
		return ""
	}
	var b strings.Builder
	var walk func(*docNode)
	walk = func(n *docNode) {
		if n == nil {
			return
		}
		walk(n.left)
		if !n.run.Deleted {
			b.WriteString(n.run.Value)
		}
		walk(n.right)
	}
	walk(d.root)
	return b.String()
}

// Insert places value at pos, counted in visible runes.
func (d *Document) Insert(pos int, value string) {
	if value == "" {
		return
	}
	if pos < 0 {
		pos = 0
	}
	if n := d.Len(); pos > n {
		pos = n
	}

	left, right := d.splitVisible(d.root, pos)
	run := TextRun{
		ID:    d.clock.ReserveSequence(runeLen(value)),
		Value: value,
		N:     int32(runeLen(value)),
		Prev:  lastCharID(left),
	}

	// Typing continues the run before the cursor rather than starting a new
	// one. Extending it in place matters: forming the neighbourhood by position
	// would divide that run first, which is exactly the boundary the merge is
	// there to remove, and would leave one run per keystroke after all.
	d.note(run)
	d.pending.Runs = append(d.pending.Runs, run)
	if extended := extendTail(left, run); extended != nil {
		d.root = joinNodes(extended, right)
		return
	}
	d.root = joinNodes(joinNodes(left, d.newNode(run)), right)

	// Placing the run at the cursor is where it belongs unless something else
	// is already anchored to the same character. Runs sharing an anchor are
	// ordered by identifier, so a run written elsewhere and merged in can sit
	// between the anchor and this one — putting this run at the cursor would
	// then order the document differently here than every other replica orders
	// it. This only arises just after a merge, at the exact spot the merge
	// touched, so the ordering is re-derived rather than reasoned about.
	if displacedBy(right, run) {
		d.recanonicalize()
	}
}

// displacedBy reports whether the subtree begins with a run that shares the new
// run's anchor and sorts before it.
func displacedBy(right *docNode, run TextRun) bool {
	n := right
	for n != nil && n.left != nil {
		n = n.left
	}
	if n == nil {
		return false
	}
	return n.run.Prev == run.Prev && n.run.ID.After(run.ID)
}

// recanonicalize rebuilds the tree in the order every replica derives, leaving
// the record of what this document has changed intact.
func (d *Document) recanonicalize() {
	runs := d.Text().getOrdered()
	d.root = nil
	for _, run := range runs {
		d.root = joinNodes(d.root, d.newNode(run))
	}
}

// note records a run in the state vector.
func (d *Document) note(run TextRun) {
	if d.sv == nil {
		d.sv = make(StateVector)
	}
	key := originOf(run.ID)
	if end := run.ID.Logical + int32(run.runeCount()); end > d.sv[key] {
		d.sv[key] = end
	}
}

// extendTail returns the subtree with run appended to its final run, or nil
// when the two do not describe one continuous stretch.
func extendTail(n *docNode, run TextRun) *docNode {
	if n == nil {
		return nil
	}
	if n.right != nil {
		r := extendTail(n.right, run)
		if r == nil {
			return nil
		}
		c := *n
		c.right = r
		c.update()
		return &c
	}
	if !canMergeRuns(n.run, run) {
		return nil
	}
	c := *n
	joined := int32(c.run.runeCount() + run.runeCount())
	c.run.Value += run.Value
	c.run.N = joined
	c.update()
	return &c
}

// Delete removes count visible characters starting at pos. The runs stay as
// tombstones, which is what a concurrent insertion next to them anchors to.
func (d *Document) Delete(pos, count int) {
	if count <= 0 || d.root == nil {
		return
	}
	if pos < 0 {
		pos = 0
	}
	if pos >= d.Len() {
		return
	}
	if pos+count > d.Len() {
		count = d.Len() - pos
	}

	left, rest := d.splitVisible(d.root, pos)
	middle, right := d.splitVisible(rest, count)

	marked, ranges, chars := markSubtreeDeleted(middle)
	d.pending.Deleted = append(d.pending.Deleted, ranges...)
	d.deleted += chars
	d.root = joinNodes(joinNodes(left, marked), right)
}

// markSubtreeDeleted returns the subtree with every run marked deleted, along
// with the ranges it marked and how many characters they held.
func markSubtreeDeleted(n *docNode) (*docNode, []DeletedRange, int) {
	if n == nil {
		return nil, nil, 0
	}
	c := *n
	left, leftRanges, leftChars := markSubtreeDeleted(n.left)
	right, rightRanges, rightChars := markSubtreeDeleted(n.right)
	c.left, c.right = left, right

	ranges := append(leftRanges, rightRanges...)
	chars := leftChars + rightChars
	if !c.run.Deleted {
		n := c.run.runeCount()
		ranges = append(ranges, DeletedRange{ID: c.run.ID, N: int32(n)})
		chars += n
		c.run.Deleted = true
	}
	c.update()
	return &c, ranges, chars
}

// MergeFrom implements [Convergent]. Merging combines the two sets of runs and
// rebuilds the index: an edit has to be fast because it happens on every
// keystroke, whereas a merge happens once per exchange with a peer and can
// afford to walk the document. Reusing the ordering [MergeTextRuns] already
// implements also means the two types cannot disagree about it.
func (d *Document) MergeFrom(other any) any {
	var incoming Text
	switch o := other.(type) {
	case *Document:
		incoming = o.Text()
	case Document:
		incoming = o.Text()
	case Text:
		incoming = o
	default:
		return d
	}

	merged := MergeTextRuns(d.Text(), incoming)
	d.reset(merged)
	return d
}

// Merge combines other into d.
func (d *Document) Merge(other *Document) { d.MergeFrom(other) }

// Compact drops runs deleted at or before before; see [Text.Compact] for what
// the watermark has to be.
func (d *Document) Compact(before hlc.HLC) {
	d.reset(d.Text().Compact(before))
}

// CompactBefore implements [Compactable].
func (d *Document) CompactBefore(before hlc.HLC) any {
	d.Compact(before)
	return d
}

// reset rebuilds the index from an ordered run list.
func (d *Document) reset(runs Text) {
	d.root = nil
	d.sv = make(StateVector)
	d.pending = Update{}
	d.deleted = 0
	for _, run := range runs.getOrdered() {
		d.note(run)
		if run.Deleted {
			d.deleted += run.runeCount()
		}
		d.root = joinNodes(d.root, d.newNode(run))
	}
}

// Copy returns a document holding the same text, independent of this one.
//
// It shares the tree rather than duplicating it, which is safe because no
// operation ever changes a node: editing builds new nodes along the path it
// touches and leaves the rest alone, so this document goes on describing what
// it describes now however much the copy is edited afterwards. A copy is
// therefore a few words of memory rather than a walk of the whole document,
// whatever its size.
//
// That is what a [CRDT] needs. Every edit it makes takes a copy of the value
// first, so that it has something to compare the result against and can say
// what changed; copying the document in full made that cost grow with the
// document rather than with the edit.
//
// This is the method [deep.Clone] looks for, so it applies wherever a document
// is copied.
func (d *Document) Copy() (*Document, error) {
	if d == nil {
		return nil, nil
	}
	sv := make(StateVector, len(d.sv))
	for k, v := range d.sv {
		sv[k] = v
	}
	return &Document{root: d.root, clock: d.clock, rng: d.rng, sv: sv, deleted: d.deleted}, nil
}

// MarshalJSON writes the document as its runs, the same shape a [Text] takes.
func (d *Document) MarshalJSON() ([]byte, error) { return json.Marshal(d.Text()) }

// UnmarshalJSON reads runs written by either a Document or a [Text].
func (d *Document) UnmarshalJSON(data []byte) error {
	var runs Text
	if err := json.Unmarshal(data, &runs); err != nil {
		return err
	}
	if d.rng == 0 {
		d.rng = documentSeed.Add(0x9E3779B9) | 1
	}
	d.reset(runs)
	return nil
}

// DocumentPatch is the change from one document to another, expressed as the
// part the first is missing rather than as the whole of the second.
type DocumentPatch struct {
	Update Update
}

// Apply merges the change into the document.
func (p *DocumentPatch) Apply(d **Document) {
	if d == nil || *d == nil {
		return
	}
	(*d).Apply(p.Update)
}

// FlatOperation describes the change for a patch's flat operation form. Saying
// what is missing rather than what the document became is what keeps a delta
// the size of the edit: a document held inside a [CRDT] would otherwise put its
// whole contents into every delta it produced.
func (p *DocumentPatch) FlatOperation() (old, new any) { return nil, p.Update }

// Diff reports the change from d to other.
//
// The engine calls this instead of comparing the two documents structurally,
// which would walk both indexes to describe a change the document can state
// directly. Working out what other holds that d does not is the same question
// [Document.Since] answers for a peer.
func (d *Document) Diff(other *Document) (*DocumentPatch, error) {
	if d == nil || other == nil {
		return nil, nil
	}

	// The usual case is that other is this document with an edit applied, in
	// which case it already knows what that edit was and there is nothing to
	// work out. Reading every run to rediscover it would make describing an
	// edit cost what the document weighs.
	if update, ok := other.pendingSince(d); ok {
		if update.IsEmpty() {
			return nil, nil
		}
		return &DocumentPatch{Update: update}, nil
	}

	update := other.Since(d.StateVector())
	if update.IsEmpty() {
		return nil, nil
	}
	return &DocumentPatch{Update: update}, nil
}

// pendingSince returns what this document has changed since old, when its own
// record of its changes accounts for the whole difference.
//
// The record is only trustworthy when old is what this document was copied
// from. Rather than assume that, the check confirms it: applying the record to
// where old stood has to land exactly where this document stands, for every
// writer and for the deleted characters both. Anything else — two unrelated
// documents, or one that has merged since — fails the check and is compared
// the general way.
func (d *Document) pendingSince(old *Document) (Update, bool) {
	if len(old.sv) > len(d.sv) {
		return Update{}, false
	}

	reached := make(StateVector, len(old.sv))
	for k, v := range old.sv {
		if v > d.sv[k] {
			return Update{}, false // old knows something this document does not
		}
		reached[k] = v
	}
	for _, run := range d.pending.Runs {
		key := originOf(run.ID)
		if end := run.ID.Logical + int32(run.runeCount()); end > reached[key] {
			reached[key] = end
		}
	}
	if len(reached) != len(d.sv) {
		return Update{}, false
	}
	for k, v := range d.sv {
		if reached[k] != v {
			return Update{}, false
		}
	}

	deleted := old.deleted
	for _, r := range d.pending.Deleted {
		deleted += int(r.N)
	}
	if deleted != d.deleted {
		return Update{}, false
	}

	return d.pending, true
}

// Patch applies p to d, merging rather than overwriting.
func (d *Document) Patch(p deep.Patch[Document], logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}
	var errs []error
	for _, op := range p.Operations {
		if _, err := d.applyOperation(op, logger); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return &deep.ApplyError{Errors: errs}
	}
	return nil
}

func (d *Document) applyOperation(op deep.Operation, _ *slog.Logger) (bool, error) {
	if op.Path != "" && op.Path != "/" {
		return false, nil
	}
	switch v := op.New.(type) {
	case Update:
		d.Apply(v)
		return true, nil
	case Text:
		d.MergeFrom(v)
		return true, nil
	case *Document:
		d.MergeFrom(v)
		return true, nil
	case deep.RawValue:
		// A delta that arrived over the wire is still encoded; decode it as
		// what it is. An Update is a JSON object, a Text a JSON array.
		if len(v.JSON) > 0 && v.JSON[0] == '[' {
			var t Text
			if err := json.Unmarshal(v.JSON, &t); err != nil {
				return false, err
			}
			d.MergeFrom(t)
			return true, nil
		}
		var u Update
		if err := json.Unmarshal(v.JSON, &u); err != nil {
			return false, err
		}
		d.Apply(u)
		return true, nil
	case []any:
		data, err := json.Marshal(v)
		if err != nil {
			return false, err
		}
		var runs Text
		if err := json.Unmarshal(data, &runs); err != nil {
			return false, err
		}
		d.MergeFrom(runs)
		return true, nil
	}
	return false, nil
}

// ── tree ─────────────────────────────────────────────────────────────────────

func (d *Document) newNode(run TextRun) *docNode {
	// xorshift: the priorities only have to be unpredictable enough to keep the
	// tree balanced, and the tree is a local index, so nothing depends on them
	// being the same anywhere else.
	d.rng ^= d.rng << 13
	d.rng ^= d.rng >> 17
	d.rng ^= d.rng << 5
	n := &docNode{run: run, priority: d.rng}
	n.update()
	return n
}

func (n *docNode) update() {
	n.size, n.visible = 1, 0
	if !n.run.Deleted {
		n.visible = n.run.runeCount()
	}
	if n.left != nil {
		n.size += n.left.size
		n.visible += n.left.visible
	}
	if n.right != nil {
		n.size += n.right.size
		n.visible += n.right.visible
	}
}

// joinNodes concatenates two subtrees, keeping the heap order on priorities.
//
// Like every operation on the tree it builds new nodes along the path it
// descends and leaves the ones it passes untouched, so any document still
// holding the old root keeps describing what it described before. That is what
// makes copying a document free — see [Document.Copy].
func joinNodes(a, b *docNode) *docNode {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if a.priority > b.priority {
		n := *a
		n.right = joinNodes(a.right, b)
		n.update()
		return &n
	}
	n := *b
	n.left = joinNodes(a, b.left)
	n.update()
	return &n
}

// splitVisible splits n so that the left side holds exactly pos visible runes,
// dividing a run when the position falls inside one.
func (d *Document) splitVisible(n *docNode, pos int) (*docNode, *docNode) {
	if n == nil {
		return nil, nil
	}
	leftVisible := 0
	if n.left != nil {
		leftVisible = n.left.visible
	}

	if pos <= leftVisible {
		l, r := d.splitVisible(n.left, pos)
		c := *n
		c.left = r
		c.update()
		return l, &c
	}

	own := 0
	if !n.run.Deleted {
		own = n.run.runeCount()
	}
	if pos < leftVisible+own {
		// The position falls inside this run: divide it, and let the right side
		// keep everything that followed.
		offset := pos - leftVisible
		leftRun, rightRun := splitRun(n.run, offset)
		c := *n
		c.run = rightRun
		c.left = nil
		c.update()
		return joinNodes(n.left, d.newNode(leftRun)), &c
	}

	l, r := d.splitVisible(n.right, pos-leftVisible-own)
	c := *n
	c.right = l
	c.update()
	return &c, r
}

// splitRun divides a run after offset runes.
func splitRun(run TextRun, offset int) (TextRun, TextRun) {
	total := run.runeCount()
	left := run.withValue(runeSlice(run.Value, 0, offset))
	right := run.withValue(runeSlice(run.Value, offset, total))
	right.ID = run.ID
	right.ID.Logical += int32(offset)
	right.Prev = run.ID
	right.Prev.Logical += int32(offset - 1)
	return left, right
}

// lastCharID returns the identifier of the last visible character in a subtree,
// which is what an insertion after it anchors to.
func lastCharID(n *docNode) hlc.HLC {
	for n != nil {
		if n.right != nil && n.right.visible > 0 {
			n = n.right
			continue
		}
		if !n.run.Deleted && n.run.runeCount() > 0 {
			id := n.run.ID
			id.Logical += int32(n.run.runeCount() - 1)
			return id
		}
		if n.left != nil && n.left.visible > 0 {
			n = n.left
			continue
		}
		return hlc.HLC{}
	}
	return hlc.HLC{}
}
