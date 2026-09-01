package crdt

import (
	"sync"
	"testing"

	deep "github.com/brunoga/deep/v5"
)

type obsDoc struct {
	Title string         `json:"title"`
	Count int            `json:"count"`
	Meta  map[string]int `json:"meta"`
}

func TestOnChangeReportsLocalEdits(t *testing.T) {
	c := NewCRDT(obsDoc{}, "a")

	var got []Change[obsDoc]
	c.OnChange(func(ch Change[obsDoc]) { got = append(got, ch) })

	c.Edit(func(d *obsDoc) { d.Title = "hello" })
	c.Edit(func(d *obsDoc) { d.Count = 7 })

	if len(got) != 2 {
		t.Fatalf("expected two changes, got %d", len(got))
	}
	for i, ch := range got {
		if ch.Source != ChangeLocal {
			t.Errorf("change %d: source = %v, want local", i, ch.Source)
		}
		if ch.Patch.IsEmpty() {
			t.Errorf("change %d carried no operations", i)
		}
	}
	if p := got[0].Patch.Operations[0].Path; p != "/Title" && p != "/title" {
		t.Errorf("first change path = %q, want the title field", p)
	}
}

// An edit that changes nothing must not be announced.
func TestOnChangeSkipsEmptyEdits(t *testing.T) {
	c := NewCRDT(obsDoc{Title: "same"}, "a")

	calls := 0
	c.OnChange(func(Change[obsDoc]) { calls++ })

	c.Edit(func(d *obsDoc) { d.Title = "same" })
	if calls != 0 {
		t.Errorf("an edit that changed nothing was announced %d times", calls)
	}
}

// A remote delta reports only what survived conflict resolution.
func TestOnChangeReportsAppliedOperationsOnly(t *testing.T) {
	a := NewCRDT(obsDoc{}, "a")
	b := NewCRDT(obsDoc{}, "b")

	stale := a.Edit(func(d *obsDoc) { d.Title = "from-a" })

	// b writes the same field later, so a's operation is stale on arrival.
	b.Edit(func(d *obsDoc) { d.Title = "from-b" })

	var got []Change[obsDoc]
	b.OnChange(func(ch Change[obsDoc]) { got = append(got, ch) })

	b.ApplyDelta(stale)

	for _, ch := range got {
		for _, op := range ch.Patch.Operations {
			if op.Path == "/Title" || op.Path == "/title" {
				t.Errorf("a rejected operation was announced: %v", op)
			}
		}
	}
	if b.View().Title != "from-b" {
		t.Errorf("stale delta was applied: %q", b.View().Title)
	}
}

func TestOnChangeReportsRemoteAndMerge(t *testing.T) {
	a := NewCRDT(obsDoc{Meta: map[string]int{}}, "a")
	b := NewCRDT(obsDoc{Meta: map[string]int{}}, "b")

	var sources []ChangeSource
	b.OnChange(func(ch Change[obsDoc]) { sources = append(sources, ch.Source) })

	delta := a.Edit(func(d *obsDoc) { d.Meta["x"] = 1 })
	b.ApplyDelta(delta)

	a.Edit(func(d *obsDoc) { d.Meta["y"] = 2 })
	b.Merge(a)

	if len(sources) != 2 {
		t.Fatalf("expected a remote change and a merge, got %v", sources)
	}
	if sources[0] != ChangeRemote {
		t.Errorf("first source = %v, want remote", sources[0])
	}
	if sources[1] != ChangeMerge {
		t.Errorf("second source = %v, want merge", sources[1])
	}
}

// The callback runs with no lock held, so it can read the replica — and a
// naive implementation that notified while holding the lock would deadlock
// here rather than fail.
func TestOnChangeCallbackCanReadReplica(t *testing.T) {
	c := NewCRDT(obsDoc{}, "a")

	done := make(chan string, 1)
	c.OnChange(func(Change[obsDoc]) { done <- c.View().Title })

	c.Edit(func(d *obsDoc) { d.Title = "readable" })

	select {
	case got := <-done:
		if got != "readable" {
			t.Errorf("callback saw %q, want the applied value", got)
		}
	default:
		t.Fatal("callback did not run")
	}
}

// A callback may edit the replica in turn.
func TestOnChangeCallbackCanEdit(t *testing.T) {
	c := NewCRDT(obsDoc{}, "a")

	c.OnChange(func(ch Change[obsDoc]) {
		if ch.Source == ChangeLocal && c.View().Count == 0 {
			c.Edit(func(d *obsDoc) { d.Count = 1 })
		}
	})

	c.Edit(func(d *obsDoc) { d.Title = "trigger" })

	if got := c.View().Count; got != 1 {
		t.Errorf("Count = %d, want the callback's edit to have applied", got)
	}
}

func TestOnChangeCancel(t *testing.T) {
	c := NewCRDT(obsDoc{}, "a")

	calls := 0
	cancel := c.OnChange(func(Change[obsDoc]) { calls++ })

	c.Edit(func(d *obsDoc) { d.Title = "one" })
	cancel()
	c.Edit(func(d *obsDoc) { d.Title = "two" })

	if calls != 1 {
		t.Errorf("observer was called %d times, want 1 before cancelling", calls)
	}
}

func TestOnChangeMultipleObservers(t *testing.T) {
	c := NewCRDT(obsDoc{}, "a")

	var a, b int
	c.OnChange(func(Change[obsDoc]) { a++ })
	c.OnChange(func(Change[obsDoc]) { b++ })

	c.Edit(func(d *obsDoc) { d.Title = "x" })

	if a != 1 || b != 1 {
		t.Errorf("observers called %d and %d times, want 1 each", a, b)
	}
}

// Changes made from several goroutines must all be delivered, without racing.
// Delivery is deliberately not serialized — callbacks run on the goroutine that
// made the change — so the callback here does its own locking, which is what
// the documentation tells a consumer to do.
func TestOnChangeConcurrentDelivery(t *testing.T) {
	c := NewCRDT(obsDoc{Meta: map[string]int{}}, "a")

	var mu sync.Mutex
	total := 0
	c.OnChange(func(Change[obsDoc]) {
		mu.Lock()
		total++
		mu.Unlock()
	})

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c.Edit(func(d *obsDoc) { d.Meta[string(rune('a'+i))] = i })
		}(i)
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if total != 8 {
		t.Errorf("got %d notifications, want 8", total)
	}
}

// The reported patch must describe what changed well enough to act on.
func TestChangePatchIsActionable(t *testing.T) {
	c := NewCRDT(obsDoc{Meta: map[string]int{"keep": 1}}, "a")

	var last Change[obsDoc]
	c.OnChange(func(ch Change[obsDoc]) { last = ch })

	before := c.View()
	c.Edit(func(d *obsDoc) { d.Meta["added"] = 2 })

	// Replaying the reported patch onto the previous value must reproduce the
	// new one — that is what makes it usable for incremental updates.
	replayed := deep.Clone(before)
	if err := deep.Apply(&replayed, last.Patch); err != nil {
		t.Fatalf("replaying the reported patch: %v", err)
	}
	if !deep.Equal(replayed, c.View()) {
		t.Errorf("replaying the change gave %+v, want %+v", replayed, c.View())
	}
}
