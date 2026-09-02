package deep_test

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"testing"
	"time"

	deep "github.com/brunoga/deep/v5"
)

// What survives a patch being serialized, and in which format.
//
// Operation.Old and Operation.New are declared `any`, so what comes back from
// a decoder is whatever that decoder produces for an untyped value rather than
// the type the patch was built from. JSON has one number type and no notion of
// a struct, so an int arrives as a float64 and a struct as a map[string]any;
// gob preserves types but only for types that were registered.
//
// That one fact has produced a run of separate-looking bugs: strict checks
// rejecting state they matched, an `add` of a pointer field failing to assign,
// values needing to be re-decoded on the way in. This table is here so the
// answer is a property of the library that is checked, rather than something
// rediscovered each time someone hits it.

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
	// limitation names the mutations the format itself cannot carry, with the
	// reason. These are properties of the encoding rather than defects in the
	// patch, and they are recorded here rather than worked around: a patch
	// representation bent to suit one codec would cost every other caller.
	limitation map[string]string
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
		strict:     true,
		limitation: gobLimitations,
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

// gobLimitations are encoding/gob's, not this library's.
var gobLimitations = map[string]string{
	"pointer field cleared": "gob cannot encode a nil pointer inside an interface, " +
		"and Operation.New is an interface holding the typed nil that clearing a pointer means",
	"slice emptied": "gob does not distinguish a nil slice from an empty one, " +
		"so an operation that sets a slice to empty arrives setting it to nil",
}

func init() {
	// gob needs to be told the concrete types it will find behind an `any`.
	// gob flattens pointers, so registering wireInner covers *wireInner too;
	// registering both is a duplicate-name panic.
	gob.Register(wireInner{})
	gob.Register(time.Time{})
	gob.Register(wirePriority(0))
	gob.Register([]int(nil))
	gob.Register([]byte(nil))
	gob.Register(map[string]int(nil))
	gob.Register(map[string]string(nil))
	gob.Register(wireDoc{})
}

func TestWireFidelity(t *testing.T) {
	for _, enc := range wireEncodings {
		t.Run(enc.name, func(t *testing.T) {
			for _, m := range wireMutations {
				t.Run(m.name, func(t *testing.T) {
					if why, ok := enc.limitation[m.name]; ok {
						t.Skip(why)
					}
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
					if why, ok := enc.limitation[m.name]; ok {
						t.Skip(why)
					}
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
