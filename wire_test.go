package deep_test

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"testing"
	"time"

	deep "github.com/brunoga/deep/v6"
)

// What survives a patch being serialized, and in which format.
//
// Operation.Old and Operation.New are declared `any`, so what comes back from
// a decoder is whatever that decoder produces for an untyped value rather than
// the type the patch was built from. JSON has one number type and no notion of
// a struct, so an int arrives as a float64 and a struct as a map[string]any;
// gob preserves types but only for types that were registered.
//
// Since v6, a decoded operation keeps its values encoded (RawValue) and
// decodes them at apply time against the target field's real type, and gob
// carries operations as their JSON form — so every mutation below survives
// every encoding, strict checks included, with no gob.Register calls anywhere
// in this file. This table is what checks that.

type wireInner struct {
	N     int    `json:"n"`
	Label string `json:"label"`
}

type wirePriority int

type wireDoc struct {
	Str    string            `json:"str"`
	Int    int               `json:"int"`
	I64    int64             `json:"i64"`
	Float  float64           `json:"float"`
	Bool   bool              `json:"bool"`
	Named  wirePriority      `json:"named"`
	Time   time.Time         `json:"time"`
	Ptr    *wireInner        `json:"ptr"`
	Nested wireInner         `json:"nested"`
	Ints   []int             `json:"ints"`
	Bytes  []byte            `json:"bytes"`
	MapSI  map[string]int    `json:"mapsi"`
	MapSS  map[string]string `json:"mapss"`
}

func wireBase() wireDoc {
	return wireDoc{
		Str: "a", Int: 1, I64: 2, Float: 1.5, Bool: false, Named: 1,
		Time:   time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		Ptr:    &wireInner{N: 1, Label: "p"},
		Nested: wireInner{N: 1, Label: "n"},
		Ints:   []int{1, 2},
		Bytes:  []byte{1},
		MapSI:  map[string]int{"k": 1},
		MapSS:  map[string]string{"k": "v"},
	}
}

// mutations each change one thing, so a failure names the field responsible.
var wireMutations = []struct {
	name  string
	apply func(*wireDoc)
}{
	{"string", func(d *wireDoc) { d.Str = "b" }},
	{"int", func(d *wireDoc) { d.Int = 42 }},
	{"int64", func(d *wireDoc) { d.I64 = 1 << 40 }},
	{"float", func(d *wireDoc) { d.Float = 2.25 }},
	{"bool", func(d *wireDoc) { d.Bool = true }},
	{"named scalar", func(d *wireDoc) { d.Named = 7 }},
	{"foreign value type", func(d *wireDoc) { d.Time = d.Time.Add(time.Hour) }},
	{"pointer field changed", func(d *wireDoc) { d.Ptr = &wireInner{N: 9, Label: "q"} }},
	{"pointer field cleared", func(d *wireDoc) { d.Ptr = nil }},
	{"pointer field added", func(d *wireDoc) { d.Ptr = nil; d.Ptr = &wireInner{N: 3} }},
	{"nested struct field", func(d *wireDoc) { d.Nested.N = 5 }},
	{"slice grown", func(d *wireDoc) { d.Ints = []int{1, 2, 3} }},
	{"slice shrunk", func(d *wireDoc) { d.Ints = []int{1} }},
	{"slice element", func(d *wireDoc) { d.Ints = []int{1, 9} }},
	{"slice emptied", func(d *wireDoc) { d.Ints = []int{} }},
	{"slice nilled", func(d *wireDoc) { d.Ints = nil }},
	{"byte slice", func(d *wireDoc) { d.Bytes = []byte{7, 8} }},
	{"map value", func(d *wireDoc) { d.MapSI = map[string]int{"k": 2} }},
	{"map key added", func(d *wireDoc) { d.MapSI = map[string]int{"k": 1, "j": 3} }},
	{"map key removed", func(d *wireDoc) { d.MapSI = map[string]int{} }},
	{"map nilled", func(d *wireDoc) { d.MapSI = nil }},
	{"string map value", func(d *wireDoc) { d.MapSS = map[string]string{"k": "w"} }},
}

// The encodings a patch can travel in.
var wireEncodings = []struct {
	name string
	// roundTrip returns the patch as it arrives at the other end, or an error
	// if the format cannot carry it.
	roundTrip func(deep.Patch[wireDoc]) (deep.Patch[wireDoc], error)
	// strict says whether strict-mode checks are expected to survive. Strict
	// verifies Operation.Old against the target, so it only works where the
	// decoded Old can still be compared with the value it describes.
	strict bool
}{
	{
		name:      "in process",
		roundTrip: func(p deep.Patch[wireDoc]) (deep.Patch[wireDoc], error) { return p, nil },
		strict:    true,
	},
	{
		name: "encoding/json",
		roundTrip: func(p deep.Patch[wireDoc]) (deep.Patch[wireDoc], error) {
			data, err := json.Marshal(p)
			if err != nil {
				return p, err
			}
			var back deep.Patch[wireDoc]
			err = json.Unmarshal(data, &back)
			return back, err
		},
		strict: true,
	},
	{
		name: "encoding/gob",
		roundTrip: func(p deep.Patch[wireDoc]) (deep.Patch[wireDoc], error) {
			var buf bytes.Buffer
			if err := gob.NewEncoder(&buf).Encode(p); err != nil {
				return p, err
			}
			var back deep.Patch[wireDoc]
			err := gob.NewDecoder(&buf).Decode(&back)
			return back, err
		},
		strict: true,
	},
	{
		name: "RFC 6902",
		roundTrip: func(p deep.Patch[wireDoc]) (deep.Patch[wireDoc], error) {
			data, err := p.ToJSONPatch()
			if err != nil {
				return p, err
			}
			return deep.ParseJSONPatch[wireDoc](data)
		},
		// RFC 6902 carries no expected-value field of its own; ToJSONPatch
		// expresses strictness as separate `test` operations, so a patch that
		// went through it is checked by those rather than by Operation.Old.
		strict: false,
	},
}

func TestWireFidelity(t *testing.T) {
	for _, enc := range wireEncodings {
		t.Run(enc.name, func(t *testing.T) {
			for _, m := range wireMutations {
				t.Run(m.name, func(t *testing.T) {
					before := wireBase()
					after := wireBase()
					m.apply(&after)

					p, err := deep.Diff(before, after)
					if err != nil {
						t.Fatalf("diff: %v", err)
					}
					sent, err := enc.roundTrip(p)
					if err != nil {
						t.Fatalf("%s: %v", enc.name, err)
					}

					got := deep.Clone(before)
					if err := deep.Apply(&got, sent); err != nil {
						t.Fatalf("apply: %v", err)
					}
					if !deep.Equal(got, after) {
						t.Errorf("did not reach the target\n got: %+v\nwant: %+v", got, after)
					}
				})
			}
		})
	}
}

func TestWireFidelityStrict(t *testing.T) {
	// Strict mode verifies every recorded Old before writing. Applied to the
	// value the diff came from, each of those checks has to pass whatever the
	// patch travelled in.
	for _, enc := range wireEncodings {
		if !enc.strict {
			continue
		}
		t.Run(enc.name, func(t *testing.T) {
			for _, m := range wireMutations {
				t.Run(m.name, func(t *testing.T) {
					before := wireBase()
					after := wireBase()
					m.apply(&after)

					p, err := deep.Diff(before, after)
					if err != nil {
						t.Fatalf("diff: %v", err)
					}
					sent, err := enc.roundTrip(p)
					if err != nil {
						t.Fatalf("%s: %v", enc.name, err)
					}
					sent.Strict = true

					got := deep.Clone(before)
					if err := deep.Apply(&got, sent); err != nil {
						t.Fatalf("strict apply against the value it came from: %v", err)
					}
					if !deep.Equal(got, after) {
						t.Errorf("did not reach the target\n got: %+v\nwant: %+v", got, after)
					}
				})
			}
		})
	}
}

func TestWireStrictStillCatchesDrift(t *testing.T) {
	// The other half of the contract: coercion must not make strict mode a
	// formality. A target that has moved must be rejected, in every encoding.
	for _, enc := range wireEncodings {
		if !enc.strict {
			continue
		}
		t.Run(enc.name, func(t *testing.T) {
			before := wireBase()
			after := wireBase()
			after.Int = 42

			p, err := deep.Diff(before, after)
			if err != nil {
				t.Fatalf("diff: %v", err)
			}
			sent, err := enc.roundTrip(p)
			if err != nil {
				t.Fatalf("%s: %v", enc.name, err)
			}
			sent.Strict = true

			drifted := deep.Clone(before)
			drifted.Int = 99 // somebody else got here first
			if err := deep.Apply(&drifted, sent); err == nil {
				t.Error("strict apply succeeded against a value that had drifted")
			}
		})
	}
}

func TestV5IntegerKindsStillParse(t *testing.T) {
	// v5 wrote operation kinds as small integers in declaration order. v6
	// writes strings, but a patch stored by v5 should still be readable.
	v5 := []byte(`{"ops":[{"k":2,"p":"/int","o":1,"n":42}],"strict":true}`)
	var p deep.Patch[wireDoc]
	if err := json.Unmarshal(v5, &p); err != nil {
		t.Fatalf("unmarshal v5 patch: %v", err)
	}
	if p.Operations[0].Kind != deep.OpReplace {
		t.Fatalf("kind = %q, want replace", p.Operations[0].Kind)
	}
	target := wireBase()
	if err := deep.Apply(&target, p); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if target.Int != 42 {
		t.Errorf("Int = %d, want 42", target.Int)
	}
}
