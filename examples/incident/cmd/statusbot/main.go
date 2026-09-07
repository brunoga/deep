// statusbot is the consumer on the far side of the schema boundary: it
// watches an incident through commandpost's protobuf face and narrates what
// changes, the way a status-page updater or a chat bot would.
//
// It deliberately never imports the server's Go model. Everything it knows
// comes from the generated protobuf types — the position any non-Go consumer
// is in — and everything it learns comes from diffing consecutive snapshots
// as protobuf messages: deepproto puts the proto runtime underneath deep.Diff,
// and registering the task list's key makes the diff element-addressed, so
// "task t3 done" comes out as exactly that.
//
//	statusbot -incident inc-1 -server http://localhost:8080 -token sekrit
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	deepproto "github.com/brunoga/deep/proto"
	deep "github.com/brunoga/deep/v6"
	"github.com/brunoga/deep/v6/condition"
	"google.golang.org/protobuf/proto"

	"github.com/brunoga/deep/examples/incident/pb"
)

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	server := flag.String("server", envOr("INCIDENT_SERVER", "http://localhost:8080"), "server URL")
	token := flag.String("token", os.Getenv("INCIDENT_TOKEN"), "bearer token")
	incident := flag.String("incident", "", "incident to watch (required)")
	interval := flag.Duration("interval", 2*time.Second, "poll interval")
	announce := flag.Bool("announce", true, "announce the bot on the incident's checklist when it starts watching")
	flag.Parse()
	if *incident == "" {
		flag.Usage()
		os.Exit(2)
	}

	// The two registrations that make protobuf messages first-class citizens:
	// Register installs the proto family (equality, cloning, diffing and
	// patching through the proto runtime), and RegisterListKey gives the
	// repeated task field an identity, the proto counterpart of `deep:"key"`.
	deepproto.Register()
	deepproto.RegisterListKey("commandpost.Incident.tasks", "id")

	bot := &bot{server: strings.TrimRight(*server, "/"), token: *token, incident: *incident}

	prev, err := bot.fetch()
	if err != nil {
		log.Fatalf("first snapshot: %v", err)
	}
	fmt.Printf("watching %s: %s (%s, SEV%d)\n", *incident, prev.GetTitle(), prev.GetStatus(), prev.GetSeverity())

	if *announce {
		if err := bot.announce(); err != nil {
			log.Printf("announce: %v", err)
		}
	}

	for {
		time.Sleep(*interval)
		cur, err := bot.fetch()
		if err != nil {
			log.Printf("fetch: %v", err)
			continue
		}
		// Two protobuf snapshots in, one element-addressed change set out.
		p, err := deep.Diff(prev, cur)
		if err != nil {
			log.Printf("diff: %v", err)
			continue
		}
		for _, op := range p.Operations {
			for _, line := range narrate(op) {
				fmt.Printf("%s %s\n", time.Now().Format(time.TimeOnly), line)
			}
		}
		prev = cur
	}
}

type bot struct {
	server, token, incident string
}

func (b *bot) fetch() (*pb.Incident, error) {
	req, err := http.NewRequest("GET", b.server+"/incidents/"+b.incident+"/proto", nil)
	if err != nil {
		return nil, err
	}
	if b.token != "" {
		req.Header.Set("Authorization", "Bearer "+b.token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server returned %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	var inc pb.Incident
	if err := proto.Unmarshal(data, &inc); err != nil {
		return nil, err
	}
	return &inc, nil
}

// announce adds the bot to the incident's checklist — through the wire.Patch
// envelope, the typed form a non-Go sender would build. The operation's value
// is a protobuf message and its condition rides along: add the task Unless it
// already exists, so restarting the bot is a skip, not a duplicate.
func (b *bot) announce() error {
	p := deep.Patch[*pb.Incident]{Operations: []deep.Operation{{
		Kind: deep.OpAdd,
		Path: "/tasks/statusbot",
		New:  &pb.Task{Id: "statusbot", Text: "status page is tracking this incident", Done: true},
		Unless: &condition.Condition{
			Op:   "exists",
			Path: "/tasks/statusbot",
		},
	}}}
	envelope, err := deepproto.ToProto(p)
	if err != nil {
		return err
	}
	data, err := proto.Marshal(envelope)
	if err != nil {
		return err
	}
	req, err := http.NewRequest("POST", b.server+"/incidents/"+b.incident+"/patch.pb", bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-protobuf")
	req.Header.Set("X-Author", "statusbot")
	if b.token != "" {
		req.Header.Set("Authorization", "Bearer "+b.token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("server returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

// narrate turns one operation into feed lines. Paths address tasks by id
// thanks to the registered list key; values may arrive still protojson-encoded
// and render via their JSON text.
func narrate(op deep.Operation) []string {
	one := func(format string, args ...any) []string {
		return []string{fmt.Sprintf(format, args...)}
	}
	path := op.Path
	switch {
	case path == "/severity":
		oldSev, newSev := val(op.Old), val(op.New)
		if newSev < oldSev {
			return one("⚠ escalated to SEV%s (was SEV%s)", newSev, oldSev)
		}
		return one("downgraded to SEV%s (was SEV%s)", newSev, oldSev)
	case path == "/status":
		return one("status: %s → %s", val(op.Old), val(op.New))
	case path == "/title":
		return one("retitled: %s", val(op.New))
	case path == "/commander":
		if v := val(op.New); v != "" {
			return one("command handed to %s", v)
		}
		return []string{"command released"}
	case path == "/tasks":
		// A whole-list operation — the shape a nil↔non-nil transition takes.
		// Unpack it into per-task lines instead of dumping the list.
		return narrateTaskList(op)
	case path == "/updated" || strings.HasPrefix(path, "/updated/"):
		return nil // freshness stamp; noise in a feed
	}
	if task, field, ok := taskPath(path); ok {
		switch {
		case field == "done" && val(op.New) == "true":
			return one("task %s completed", task)
		case field == "done":
			return one("task %s reopened", task)
		case field == "owner" && val(op.New) != "":
			return one("task %s claimed by %s", task, val(op.New))
		case field == "owner":
			return one("task %s unclaimed", task)
		case field == "" && op.Kind == deep.OpAdd:
			return one("new task %s: %s", task, taskText(op.New))
		case field == "" && op.Kind == deep.OpRemove:
			return one("task %s removed", task)
		}
	}
	return one("%s %s: %s → %s", strings.ToLower(op.Kind.String()), path, val(op.Old), val(op.New))
}

// narrateTaskList turns a whole-task-list operation into per-task lines,
// comparing ids on both sides.
func narrateTaskList(op deep.Operation) []string {
	oldTasks, newTasks := taskList(op.Old), taskList(op.New)
	oldByID := map[string]bool{}
	for _, t := range oldTasks {
		oldByID[t.ID] = true
	}
	var lines []string
	for _, t := range newTasks {
		if !oldByID[t.ID] {
			lines = append(lines, fmt.Sprintf("new task %s: %s", t.ID, t.Text))
		}
		delete(oldByID, t.ID)
	}
	for id := range oldByID {
		lines = append(lines, fmt.Sprintf("task %s removed", id))
	}
	return lines
}

type feedTask struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// taskList decodes a whole-list value, whichever form it took.
func taskList(v any) []feedTask {
	switch t := v.(type) {
	case nil:
		return nil
	case deep.RawValue:
		var tasks []feedTask
		if err := json.Unmarshal(t.JSON, &tasks); err == nil {
			return tasks
		}
	case []*pb.Task:
		out := make([]feedTask, len(t))
		for i, task := range t {
			out[i] = feedTask{ID: task.GetId(), Text: task.GetText()}
		}
		return out
	}
	return nil
}

// taskPath splits /tasks/<id>[/field] into its parts.
func taskPath(path string) (task, field string, ok bool) {
	rest, ok := strings.CutPrefix(path, "/tasks/")
	if !ok {
		return "", "", false
	}
	task, field, _ = strings.Cut(rest, "/")
	return deep.UnescapePathKey(task), field, true
}

// val renders an operation value for humans: wire-encoded values print their
// JSON text (unquoted for strings), in-memory values print themselves.
func val(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case deep.RawValue:
		return strings.Trim(t.String(), `"`)
	default:
		return strings.Trim(fmt.Sprintf("%v", t), `"`)
	}
}

// taskText pulls the text field out of an added task, whichever form the
// value took.
func taskText(v any) string {
	if task, ok := v.(*pb.Task); ok {
		return task.GetText()
	}
	if raw, ok := v.(deep.RawValue); ok {
		var m struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal(raw.JSON, &m); err == nil && m.Text != "" {
			return m.Text
		}
	}
	return val(v)
}
