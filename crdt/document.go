package crdt

import (
	"encoding/json"
	"log/slog"
	"strings"
	"sync/atomic"

	deep "github.com/brunoga/deep/v5"
	"github.com/brunoga/deep/v5/crdt/hlc"
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
	return &Document{clock: clock, rng: documentSeed.Add(0x9E3779B9) | 1}
}

// DocumentFromText returns a document holding the runs of t.
func DocumentFromText(t Text, clock *hlc.Clock) *Document {
	d := NewDocument(clock)
	for _, run := range t.getOrdered() {
		d.root = joinNodes(d.root, d.newNode(run))
	}
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
	if extendTail(left, run) {
		d.root = joinNodes(left, right)
		return
	}
	d.root = joinNodes(joinNodes(left, d.newNode(run)), right)
}

// extendTail appends run to the last run of the subtree when the two describe
// one continuous stretch, reporting whether it did.
func extendTail(n *docNode, run TextRun) bool {
	if n == nil {
		return false
	}
	if n.right != nil {
		if extendTail(n.right, run) {
			n.update()
			return true
		}
		return false
	}
	if !canMergeRuns(n.run, run) {
		return false
	}
	joined := int32(n.run.runeCount() + run.runeCount())
	n.run.Value += run.Value
	n.run.N = joined
	n.update()
	return true
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

	var mark func(*docNode)
	mark = func(n *docNode) {
		if n == nil {
			return
		}
		mark(n.left)
		n.run.Deleted = true
		mark(n.right)
		n.update()
	}
	mark(middle)

	d.root = joinNodes(joinNodes(left, middle), right)
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
	for _, run := range runs.getOrdered() {
		d.root = joinNodes(d.root, d.newNode(run))
	}
}

// Copy returns an independent document holding the same text.
//
// Copying a document by walking its structure would visit every node of the
// tree and rebuild it one field at a time, which is what a general deep copy
// does with no knowledge of what the tree is for. Rebuilding from the runs
// instead skips all of that: the runs are already in order, so the copy is one
// pass. This is the method [deep.Clone] looks for, so it applies wherever a
// document is copied, including a [CRDT] taking a snapshot of its value.
func (d *Document) Copy() (*Document, error) {
	if d == nil {
		return nil, nil
	}
	out := &Document{clock: d.clock, rng: d.rng}
	var build func(*docNode) *docNode
	build = func(n *docNode) *docNode {
		if n == nil {
			return nil
		}
		c := &docNode{run: n.run, priority: n.priority, size: n.size, visible: n.visible}
		c.left = build(n.left)
		c.right = build(n.right)
		return c
	}
	out.root = build(d.root)
	return out, nil
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

// Diff reports the change from d to other as one whole-value operation: the
// receiving side merges rather than overwrites.
func (d *Document) Diff(other *Document) deep.Patch[Document] {
	if d.String() == other.String() && d.Len() == other.Len() {
		if len(d.Text()) == len(other.Text()) {
			return deep.Patch[Document]{}
		}
	}
	return deep.Patch[Document]{Operations: []deep.Operation{
		{Kind: deep.OpReplace, Path: "/", Old: d.Text(), New: other.Text()},
	}}
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
	case Text:
		d.MergeFrom(v)
		return true, nil
	case *Document:
		d.MergeFrom(v)
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
func joinNodes(a, b *docNode) *docNode {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if a.priority > b.priority {
		a.right = joinNodes(a.right, b)
		a.update()
		return a
	}
	b.left = joinNodes(a, b.left)
	b.update()
	return b
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
		n.left = r
		n.update()
		return l, n
	}

	own := 0
	if !n.run.Deleted {
		own = n.run.runeCount()
	}
	if pos < leftVisible+own {
		// The position falls inside this run: divide it and take the left part.
		offset := pos - leftVisible
		left, right := splitRun(n.run, offset)
		leftSub, rightSub := n.left, n.right
		n.run = right
		n.left = nil
		n.update()
		n.right = rightSub
		n.update()
		return joinNodes(leftSub, d.newNode(left)), n
	}

	l, r := d.splitVisible(n.right, pos-leftVisible-own)
	n.right = l
	n.update()
	return n, r
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
