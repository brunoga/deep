package main

import (
	"testing"

	deepproto "github.com/brunoga/deep/proto"
	deep "github.com/brunoga/deep/v6"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/brunoga/deep/examples/incident/pb"
)

func init() {
	deepproto.Register()
	deepproto.RegisterListKey("commandpost.Incident.tasks", "id")
}

// The golden test: a known pair of snapshots must narrate into a known feed.
// This is the whole bot in one assertion — proto messages diffed through the
// proto runtime, tasks matched by id, operations turned into sentences.
func TestNarrateFeed(t *testing.T) {
	before := &pb.Incident{
		Id: "inc-1", Title: "checkout errors", Severity: 2, Status: "open",
		Tasks: []*pb.Task{
			{Id: "t1", Text: "page db oncall"},
			{Id: "t2", Text: "check error rates"},
		},
		Updated: timestamppb.New(timestamppb.Now().AsTime()),
	}
	after := &pb.Incident{
		Id: "inc-1", Title: "checkout errors", Severity: 1, Status: "mitigated",
		Commander: "ana",
		Tasks: []*pb.Task{
			// t2 moved to the front (no change), got done (a change);
			// t1 claimed; t3 is new.
			{Id: "t2", Text: "check error rates", Done: true},
			{Id: "t1", Text: "page db oncall", Owner: "bruno"},
			{Id: "t3", Text: "post status page"},
		},
		Updated: timestamppb.New(timestamppb.Now().AsTime().Add(1)),
	}

	p, err := deep.Diff(before, after)
	if err != nil {
		t.Fatal(err)
	}
	var feed []string
	for _, op := range p.Operations {
		feed = append(feed, narrate(op)...)
	}

	want := map[string]bool{
		"⚠ escalated to SEV1 (was SEV2)": false,
		"status: open → mitigated":       false,
		"command handed to ana":          false,
		"task t2 completed":              false,
		"task t1 claimed by bruno":       false,
		"new task t3: post status page":  false,
	}
	for _, line := range feed {
		if _, ok := want[line]; !ok {
			t.Errorf("unexpected feed line: %q", line)
		}
		want[line] = true
	}
	for line, seen := range want {
		if !seen {
			t.Errorf("missing feed line: %q (feed: %v)", line, feed)
		}
	}
}

// Reordering tasks alone is silence: the registered list key makes order
// meaningless.
func TestReorderIsSilent(t *testing.T) {
	before := &pb.Incident{Id: "inc-1", Title: "x", Severity: 3, Status: "open",
		Tasks: []*pb.Task{{Id: "a", Text: "1"}, {Id: "b", Text: "2"}}}
	after := &pb.Incident{Id: "inc-1", Title: "x", Severity: 3, Status: "open",
		Tasks: []*pb.Task{{Id: "b", Text: "2"}, {Id: "a", Text: "1"}}}

	p, err := deep.Diff(before, after)
	if err != nil {
		t.Fatal(err)
	}
	if !p.IsEmpty() {
		t.Fatalf("reorder produced operations: %s", p)
	}
}
