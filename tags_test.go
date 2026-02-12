package deep

import (
	"testing"
)

type TaggedStruct struct {
	Normal   string
	Ignored  string `deep:"-"`
	ReadOnly string `deep:"readonly"`
	Atomic   Inner  `deep:"atomic"`
}

type Inner struct {
	A int
	B int
}

func TestTags_Copy(t *testing.T) {
	src := TaggedStruct{
		Normal:   "normal",
		Ignored:  "ignored",
		ReadOnly: "readonly",
		Atomic: Inner{
			A: 1,
			B: 2,
		},
	}

	dst, err := Copy(src)
	if err != nil {
		t.Fatalf("Copy failed: %v", err)
	}

	if dst.Normal != src.Normal {
		t.Errorf("Normal field not copied: expected %s, got %s", src.Normal, dst.Normal)
	}

	if dst.Ignored != "" {
		t.Errorf("Ignored field was copied: expected empty, got %s", dst.Ignored)
	}

	if dst.ReadOnly != src.ReadOnly {
		t.Errorf("ReadOnly field not copied: expected %s, got %s", src.ReadOnly, dst.ReadOnly)
	}

	if dst.Atomic != src.Atomic {
		t.Errorf("Atomic field not copied: expected %+v, got %+v", src.Atomic, dst.Atomic)
	}
}

func TestTags_Diff(t *testing.T) {
	a := TaggedStruct{
		Normal:   "a",
		Ignored:  "a",
		ReadOnly: "a",
		Atomic: Inner{
			A: 1,
			B: 2,
		},
	}
	b := TaggedStruct{
		Normal:   "b",
		Ignored:  "b",
		ReadOnly: "b",
		Atomic: Inner{
			A: 3,
			B: 4,
		},
	}

	patch := Diff(a, b)
	if patch == nil {
		t.Fatal("Expected non-nil patch")
	}

	// Verify that Ignored is NOT in the patch.
	// Verify that Normal IS in the patch.
	// Verify that ReadOnly IS in the patch (as ReadOnly(valuePatch)).
	// Verify that Atomic IS in the patch (as valuePatch, not structPatch).

	patch.Apply(&a)

	if a.Normal != "b" {
		t.Errorf("Normal field not updated: expected b, got %s", a.Normal)
	}

	if a.Ignored != "a" {
		t.Errorf("Ignored field was updated: expected a, got %s", a.Ignored)
	}

	if a.ReadOnly != "a" {
		t.Errorf("ReadOnly field was updated: expected a, got %s", a.ReadOnly)
	}

	if a.Atomic.A != 3 || a.Atomic.B != 4 {
		t.Errorf("Atomic field not updated: expected {3 4}, got %+v", a.Atomic)
	}

	// Check if Atomic was indeed atomic (no inner field patches)
	tp := patch.(*typedPatch[TaggedStruct])
	sp := tp.inner.(*structPatch)
	atomicPatch := sp.fields["Atomic"]
	if _, ok := atomicPatch.(*valuePatch); !ok {
		t.Errorf("Atomic field patch is not a valuePatch: %T", atomicPatch)
	}

	readonlyPatch := sp.fields["ReadOnly"]
	if _, ok := readonlyPatch.(*readOnlyPatch); !ok {
		t.Errorf("ReadOnly field patch is not a readOnlyPatch: %T", readonlyPatch)
	}
}
