package model

import (
	"encoding/json"
	"testing"

	deep "github.com/brunoga/deep/v6"
)

func sample() Asset {
	return Asset{
		ID: "pump-7", Name: "Intake pump 7", Site: "riverside", Status: StatusOK,
		Assignee: "ana",
		Readings: map[string]Reading{
			"ph":   {Value: 7.1, Unit: "pH", By: "ana"},
			"flow": {Value: 122, Unit: "l/min", By: "ana"},
		},
		Checks: []Check{
			{ID: "seals", Label: "inspect seals"},
			{ID: "filter", Label: "replace filter"},
		},
		Notes: "installed 2024",
	}
}

// Concurrent-edit geometry: different sensors, different checklist items and
// different fields of one reading all diff to disjoint paths — the property
// the whole sync design leans on.
func TestEditsLandOnDisjointPaths(t *testing.T) {
	base := sample()

	tech := deep.Clone(base)
	r := tech.Readings["ph"]
	r.Value = 6.4
	tech.Readings["ph"] = r
	tech.Checks[0].Done = true
	tech.Checks[0].By = "ana"

	office := deep.Clone(base)
	office.Assignee = "bruno"
	office.Readings["temp"] = Reading{Value: 18.5, Unit: "C", By: "scada"}

	techPatch, err := deep.Diff(base, tech)
	if err != nil {
		t.Fatal(err)
	}
	officePatch, err := deep.Diff(base, office)
	if err != nil {
		t.Fatal(err)
	}

	paths := map[string]bool{}
	for _, op := range techPatch.Operations {
		paths[op.Path] = true
	}
	for _, want := range []string{"/readings/ph/value", "/checks/seals/done", "/checks/seals/by"} {
		if !paths[want] {
			t.Errorf("tech diff missing %s: %v", want, paths)
		}
	}
	for _, op := range officePatch.Operations {
		if paths[op.Path] {
			t.Errorf("office op %s collides with tech's", op.Path)
		}
	}

	// Both apply to the base in either order and everything survives.
	merged := deep.Clone(base)
	if err := deep.Apply(&merged, officePatch); err != nil {
		t.Fatal(err)
	}
	if err := deep.Apply(&merged, techPatch); err != nil {
		t.Fatal(err)
	}
	if merged.Readings["ph"].Value != 6.4 || merged.Assignee != "bruno" ||
		merged.Readings["temp"].Value != 18.5 || !merged.Checks[0].Done {
		t.Fatalf("merged lost an edit: %+v", merged)
	}
}

// The wire round trip: a diff through JSON applies identically.
func TestPatchWireRoundTrip(t *testing.T) {
	a := sample()
	b := deep.Clone(a)
	b.Status = StatusFault
	delete(b.Readings, "flow")
	b.Checks = append(b.Checks, Check{ID: "motor", Label: "check motor"})
	b.Notes += "\nvibration at startup"

	p, err := deep.Diff(a, b)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var q deep.Patch[Asset]
	if err := json.Unmarshal(data, &q); err != nil {
		t.Fatal(err)
	}
	c := deep.Clone(a)
	if err := deep.Apply(&c, q); err != nil {
		t.Fatal(err)
	}
	if !deep.Equal(c, b) {
		t.Fatalf("wire round trip diverged:\n got  %+v\n want %+v", c, b)
	}
}

// Generated and reflection appliers agree through the wire.
func TestGeneratedAgreesWithReflection(t *testing.T) {
	a := sample()
	b := deep.Clone(a)
	r := b.Readings["flow"]
	r.Value = 99
	b.Readings["flow"] = r
	b.Checks[1].Done = true

	data, err := json.Marshal(deep.Patch[Asset](a.Diff(&b)))
	if err != nil {
		t.Fatal(err)
	}
	var wire deep.Patch[Asset]
	if err := json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
	viaGen := deep.Clone(a)
	if err := viaGen.Patch(wire, nil); err != nil {
		t.Fatal(err)
	}
	viaRefl := deep.Clone(a)
	if err := deep.Apply(&viaRefl, wire); err != nil {
		t.Fatal(err)
	}
	if !deep.Equal(viaGen, b) || !deep.Equal(viaRefl, b) {
		t.Fatalf("appliers disagree:\n gen  %+v\n refl %+v\n want %+v", viaGen, viaRefl, b)
	}
}

func TestWorse(t *testing.T) {
	if Worse(StatusOK, StatusFault) != StatusFault || Worse(StatusOffline, StatusAttention) != StatusAttention {
		t.Fatal("severity ordering broken")
	}
	// The ordering exists to protect observations from silence: a fault
	// somebody saw outranks telemetry that merely lost contact.
	if Worse(StatusFault, StatusOffline) != StatusFault {
		t.Fatal("offline outranked an observed fault")
	}
	if Worse(Status("garbage"), StatusOK) != Status("garbage") {
		t.Fatal("unknown status must rank worst, not vanish")
	}
	if Worse(StatusFault, Status("garbage")) != Status("garbage") {
		t.Fatal("unknown status must outrank even a fault")
	}
}

func TestValidate(t *testing.T) {
	a := sample()
	if err := a.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := deep.Clone(a)
	bad.Readings["../evil"] = Reading{}
	if bad.Validate() == nil {
		t.Fatal("traversal sensor id accepted")
	}
	bad = deep.Clone(a)
	bad.Status = "meh"
	if bad.Validate() == nil {
		t.Fatal("bad status accepted")
	}
}
