package v5

import (
	"sort"
	"strings"

	"github.com/brunoga/deep/v5/crdt/hlc"
)

type TextRun struct {
	ID      hlc.HLC `json:"id"`
	Value   string  `json:"v"`
	Prev    hlc.HLC `json:"p,omitempty"`
	Deleted bool    `json:"d,omitempty"`
}

type Text []TextRun

func (t Text) String() string {
	var b strings.Builder
	for _, run := range t.getOrdered() {
		if !run.Deleted {
			b.WriteString(run.Value)
		}
	}
	return b.String()
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

func (t Text) Diff(other Text) Patch[Text] {
	if len(t) == len(other) {
		same := true
		for i := range t {
			if t[i] != other[i] {
				same = false
				break
			}
		}
		if same {
			return Patch[Text]{}
		}
	}

	// For text, we usually just want to include the whole state for convergence
	// or generate a specialized text operation.
	// For v5 prototype, let's just return a replace op.
	return Patch[Text]{
		Operations: []Operation{
			{Kind: OpReplace, Path: "", Old: t, New: other},
		},
	}
}

func (t *Text) GeneratedApply(p Patch[Text]) error {
	for _, op := range p.Operations {
		if op.Path == "" || op.Path == "/" {
			*t = op.New.(Text)
		}
	}
	return nil
}
