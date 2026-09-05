package deep_test

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	deep "github.com/brunoga/deep/v6"
)

// A stand-in for a foreign runtime's type: a "sealed" value whose Go struct
// must not be walked directly. Its bookkeeping field plays the role protobuf's
// internal state plays — different between logically equal values, corrupted
// by naive copying — and its wire form is a bare string, which encoding/json
// could never produce from the struct.
type sealed struct {
	Text        string
	bookkeeping int // differs between equal values; must never leak into a diff
}

// sealedHolder is an ordinary struct holding sealed values the way an
// application struct holds proto messages.
type sealedHolder struct {
	Name string  `json:"name"`
	Doc  *sealed `json:"doc"`
	Note string  `json:"note"`
}

func init() {
	deep.RegisterTypeFamily(deep.TypeFamily{
		Name: "sealed",
		Match: func(t reflect.Type) bool {
			return t == reflect.TypeOf(&sealed{}) || t == reflect.TypeOf(sealed{})
		},
		Equal: func(a, b any) bool {
			sa, sb := asSealed(a), asSealed(b)
			if sa == nil || sb == nil {
				return sa == sb
			}
			return sa.Text == sb.Text // bookkeeping deliberately ignored
		},
		Clone: func(v any) any {
			s := asSealed(v)
			if s == nil {
				return v
			}
			c := &sealed{Text: s.Text, bookkeeping: 777} // a fresh runtime state
			if _, isPtr := v.(*sealed); isPtr {
				return c
			}
			return *c
		},
		Diff: func(a, b any) ([]deep.Operation, error) {
			sa, sb := asSealed(a), asSealed(b)
			if sa == nil || sb == nil || sa.Text == sb.Text {
				return nil, nil
			}
			return []deep.Operation{{Kind: deep.OpReplace, Path: "/text", Old: sa.Text, New: sb.Text}}, nil
		},
		Apply: func(target any, op deep.Operation) error {
			s := asSealed(target)
			switch op.Path {
			case "/text":
				if op.Strict && op.Old != nil {
					if want, ok := deep.ValueAs[string](op.Old); ok && want != s.Text {
						return fmt.Errorf("strict: text is %q, expected %q", s.Text, want)
					}
				}
				v, ok := deep.ValueAs[string](op.New)
				if !ok {
					return fmt.Errorf("cannot read %v as text", op.New)
				}
				s.Text = v
				return nil
			case "/", "":
				v, ok := deep.ValueAs[*sealed](op.New)
				if !ok {
					return fmt.Errorf("cannot read %v as sealed", op.New)
				}
				*s = *v
				return nil
			}
			return fmt.Errorf("no such path in a sealed value: %s", op.Path)
		},
		Marshal: func(v any) ([]byte, error) {
			s := asSealed(v)
			return json.Marshal("sealed:" + s.Text) // a shape encoding/json would never produce
		},
		Unmarshal: func(data []byte, t reflect.Type) (any, error) {
			var s string
			if err := json.Unmarshal(data, &s); err != nil {
				return nil, err
			}
			out := &sealed{Text: strings.TrimPrefix(s, "sealed:"), bookkeeping: 777}
			if t.Kind() == reflect.Pointer {
				return out, nil
			}
			return *out, nil
		},
	})
}

func asSealed(v any) *sealed {
	switch s := v.(type) {
	case *sealed:
		return s
	case sealed:
		return &s
	}
	return nil
}

func TestFamilyEqualIgnoresRuntimeState(t *testing.T) {
	a := sealedHolder{Doc: &sealed{Text: "hi", bookkeeping: 1}}
	b := sealedHolder{Doc: &sealed{Text: "hi", bookkeeping: 2}}
	if !deep.Equal(a, b) {
		t.Error("bookkeeping leaked into equality")
	}
	b.Doc.Text = "bye"
	if deep.Equal(a, b) {
		t.Error("a real difference was missed")
	}
}

func TestFamilyCloneUsesTheFamilysRuntime(t *testing.T) {
	v := sealedHolder{Name: "n", Doc: &sealed{Text: "hi", bookkeeping: 1}}
	c := deep.Clone(v)
	if c.Doc == v.Doc {
		t.Fatal("clone shares the sealed value")
	}
	if c.Doc.Text != "hi" || c.Doc.bookkeeping != 777 {
		t.Errorf("family Clone was not used: %+v", c.Doc)
	}
	// Sharing across two routes is still one value in the copy.
	shared := &sealed{Text: "s"}
	type two struct{ A, B *sealed }
	tc := deep.Clone(two{A: shared, B: shared})
	if tc.A != tc.B {
		t.Error("a sealed value reached twice was cloned twice")
	}
}

func TestFamilyDiffProducesOnlyFamilyOperations(t *testing.T) {
	a := sealedHolder{Name: "n", Doc: &sealed{Text: "old", bookkeeping: 1}}
	b := sealedHolder{Name: "n", Doc: &sealed{Text: "new", bookkeeping: 2}}

	p := deep.MustDiff(a, b)
	if len(p.Operations) != 1 {
		t.Fatalf("got %d operations, want 1: %v", len(p.Operations), p)
	}
	op := p.Operations[0]
	// The reflection engine addresses fields by Go name (apply accepts json
	// names too); the family's relative "/text" is rooted at that position.
	if op.Path != "/Doc/text" {
		t.Errorf("path = %q, want /Doc/text", op.Path)
	}

	got := deep.Clone(a)
	if err := deep.Apply(&got, p); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got.Doc.Text != "new" {
		t.Errorf("Text = %q after apply", got.Doc.Text)
	}
	if !deep.Equal(got, b) {
		t.Error("diff+apply did not reach the target")
	}
}

func TestFamilyDiffApplySurvivesTheWire(t *testing.T) {
	a := sealedHolder{Doc: &sealed{Text: "old"}}
	b := sealedHolder{Doc: &sealed{Text: "new"}}
	p := deep.MustDiff(a, b)

	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var wire deep.Patch[sealedHolder]
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	got := deep.Clone(a)
	if err := deep.Apply(&got, wire); err != nil {
		t.Fatalf("apply decoded patch: %v", err)
	}
	if got.Doc.Text != "new" {
		t.Errorf("Text = %q after decoded apply", got.Doc.Text)
	}
}

func TestFamilyValuesUseTheFamilyCodecOnTheWire(t *testing.T) {
	// A whole-value replace carries the sealed value; on the wire it must be
	// the family's form, and on arrival it must decode through the family.
	a := sealedHolder{Doc: &sealed{Text: "old"}}
	p := deep.Patch[sealedHolder]{Operations: []deep.Operation{
		{Kind: deep.OpReplace, Path: "/doc", New: &sealed{Text: "new"}},
	}}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(data), `"sealed:new"`) {
		t.Fatalf("family Marshal not used on the wire: %s", data)
	}

	var wire deep.Patch[sealedHolder]
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if err := deep.Apply(&a, wire); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if a.Doc.Text != "new" || a.Doc.bookkeeping != 777 {
		t.Errorf("family Unmarshal not used on arrival: %+v", a.Doc)
	}
}

func TestFamilyStrictAndReverse(t *testing.T) {
	a := sealedHolder{Doc: &sealed{Text: "old"}}
	b := sealedHolder{Doc: &sealed{Text: "new"}}
	p := deep.MustDiff(a, b)
	p.Strict = true

	drifted := sealedHolder{Doc: &sealed{Text: "elsewhere"}}
	if err := deep.Apply(&drifted, p); err == nil {
		t.Error("strict apply passed against a drifted sealed value")
	}

	got := deep.Clone(b)
	if err := deep.Apply(&got, p.Reverse()); err != nil {
		t.Fatalf("reverse: %v", err)
	}
	if got.Doc.Text != "old" {
		t.Errorf("reverse landed on %q, want old", got.Doc.Text)
	}
}
