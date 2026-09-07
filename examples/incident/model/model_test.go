package model

import (
	"encoding/json"
	"net/netip"
	"strings"
	"testing"
	"time"

	deep "github.com/brunoga/deep/v6"
)

func sample() Incident {
	return Incident{
		ID:        "inc-1",
		Title:     "checkout errors",
		Severity:  Sev2,
		Status:    StatusOpen,
		Commander: "",
		Services:  []string{"checkout", "payments"},
		Hosts:     []netip.Addr{netip.MustParseAddr("10.0.0.1")},
		Tasks: []Task{
			{ID: "t1", Text: "page db oncall"},
			{ID: "t2", Text: "check error rates"},
		},
		Updated: time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC),
	}
}

// The families keep foreign types opaque: no operation path may ever descend
// into time.Time or netip.Addr internals.
func TestDiffPathsStayOutsideForeignTypes(t *testing.T) {
	a := sample()
	b := deep.Clone(a)
	b.Updated = b.Updated.Add(time.Hour)
	b.Hosts = append(b.Hosts, netip.MustParseAddr("10.0.0.2"))

	p, err := deep.Diff(a, b)
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range p.Operations {
		for _, forbidden := range []string{"/ext", "/wall", "/loc", "/addr", "/lo", "/hi", "/z"} {
			if strings.HasSuffix(op.Path, forbidden) {
				t.Errorf("path %q descends into a foreign type", op.Path)
			}
		}
	}
	if err := deep.Apply(&a, p); err != nil {
		t.Fatal(err)
	}
	if !deep.Equal(a, b) {
		t.Fatal("apply did not converge")
	}
}

// Reordering a keyed task list is no change at all, and element edits address
// tasks by ID.
func TestTasksAreKeyed(t *testing.T) {
	a := sample()
	b := deep.Clone(a)
	b.Tasks[0], b.Tasks[1] = b.Tasks[1], b.Tasks[0]

	p, err := deep.Diff(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if !p.IsEmpty() {
		t.Fatalf("reorder produced operations: %s", p)
	}

	b.Tasks[0].Done = true // t2 after the swap
	p, err = deep.Diff(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Operations) != 1 || p.Operations[0].Path != "/tasks/t2/done" {
		t.Fatalf("want single op at /tasks/t2/done, got %s", p)
	}
}

// A patch that crossed the wire as JSON still applies: values arrive as
// RawValue and decode against the field types, families included.
func TestPatchWireRoundTrip(t *testing.T) {
	a := sample()
	b := deep.Clone(a)
	b.Severity = Sev1
	b.Status = StatusMitigated
	b.Commander = "bruno"
	b.Hosts = []netip.Addr{netip.MustParseAddr("10.0.0.9")}
	b.Tasks = append(b.Tasks, Task{ID: "t3", Text: "post status page"})
	b.Updated = b.Updated.Add(time.Minute)

	p, err := deep.Diff(a, b)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "\"ext\"") || strings.Contains(string(data), "\"lo\"") {
		t.Fatalf("wire form leaks foreign internals: %s", data)
	}

	var q deep.Patch[Incident]
	if err := json.Unmarshal(data, &q); err != nil {
		t.Fatal(err)
	}
	c := deep.Clone(a)
	if err := deep.Apply(&c, q); err != nil {
		t.Fatal(err)
	}
	if !deep.Equal(c, b) {
		t.Fatalf("wire round trip did not converge:\n got %+v\nwant %+v", c, b)
	}
}

// time.Time equality is the family's: an instant with a monotonic reading
// equals the same instant without one.
func TestTimeFamilyEquality(t *testing.T) {
	now := time.Now() // carries a monotonic reading
	a := sample()
	a.Updated = now
	b := deep.Clone(a)
	b.Updated = now.Round(0) // strips it

	p, err := deep.Diff(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if !p.IsEmpty() {
		t.Fatalf("monotonic reading produced a diff: %s", p)
	}
}

// The generated fast path and the reflection engine agree through the wire.
func TestGeneratedPatchAgreesWithReflection(t *testing.T) {
	a := sample()
	b := deep.Clone(a)
	b.Severity = Sev1
	b.Tasks[0].Owner = "ana"
	b.Updated = b.Updated.Add(time.Second)

	p := a.Diff(&b) // generated
	data, err := json.Marshal(deep.Patch[Incident](p))
	if err != nil {
		t.Fatal(err)
	}
	var q deep.Patch[Incident]
	if err := json.Unmarshal(data, &q); err != nil {
		t.Fatal(err)
	}

	viaGenerated := deep.Clone(a)
	if err := viaGenerated.Patch(q, nil); err != nil { // generated applier
		t.Fatal(err)
	}
	viaReflection := deep.Clone(a)
	if err := deep.Apply(&viaReflection, q); err != nil {
		t.Fatal(err)
	}
	if !deep.Equal(viaGenerated, b) || !deep.Equal(viaReflection, b) {
		t.Fatalf("appliers disagree:\n generated  %+v\n reflection %+v\n want       %+v",
			viaGenerated, viaReflection, b)
	}
}

// The escalation idiom: severity goes to 1 only If it is currently worse
// than 1 — idempotent, never a de-escalation.
func TestConditionalEscalation(t *testing.T) {
	esc := deep.NewPatch[Incident]().With(
		deep.Set(deep.PathString[Incident, Severity]("/severity"), Sev1).
			If(deep.Gt(deep.PathString[Incident, Severity]("/severity"), Sev1)),
	).Build()

	a := sample() // Sev2
	if err := deep.Apply(&a, esc); err != nil {
		t.Fatal(err)
	}
	if a.Severity != Sev1 {
		t.Fatalf("escalation did not apply: %v", a.Severity)
	}

	// Applying again is a no-op, not an error and not a change.
	if err := deep.Apply(&a, esc); err != nil {
		t.Fatal(err)
	}
	if a.Severity != Sev1 {
		t.Fatalf("second application changed severity: %v", a.Severity)
	}
}

// Clone shares nothing with its source.
func TestCloneIsIndependent(t *testing.T) {
	a := sample()
	c := deep.Clone(a)
	c.Tasks[0].Done = true
	c.Services[0] = "edited"
	c.Hosts[0] = netip.MustParseAddr("192.168.0.1")
	if a.Tasks[0].Done || a.Services[0] == "edited" || a.Hosts[0] != netip.MustParseAddr("10.0.0.1") {
		t.Fatal("clone shares memory with source")
	}
}
