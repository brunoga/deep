// incident is the responder's tool: structured edits go to the server as
// deep patches, and every command prints what actually happened — applied,
// skipped by a condition, or refused.
//
//	incident create inc-1 "checkout errors" -sev 2
//	incident task add inc-1 t1 "page db oncall"
//	incident task claim inc-1 t1
//	incident escalate inc-1 1
//	incident history inc-1
//	incident undo inc-1 3
//	incident watch inc-1
//
// Server, token and author come from -server/-token/-author or the
// INCIDENT_SERVER, INCIDENT_TOKEN and INCIDENT_AUTHOR environment variables.
package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	deep "github.com/brunoga/deep/v6"

	"github.com/brunoga/deep/examples/incident/client"
	"github.com/brunoga/deep/examples/incident/model"
	"github.com/brunoga/deep/examples/incident/server"
	"github.com/brunoga/deep/examples/incident/tui"
)

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	serverURL := flag.String("server", envOr("INCIDENT_SERVER", "http://localhost:8080"), "server URL")
	token := flag.String("token", os.Getenv("INCIDENT_TOKEN"), "bearer token")
	author := flag.String("author", envOr("INCIDENT_AUTHOR", envOr("USER", "anonymous")), "author name for the audit log")
	flag.Usage = usage
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		usage()
		os.Exit(2)
	}

	c := client.New(*serverURL, *token, *author)
	if err := run(c, args); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `usage: incident [flags] <command> [args]

commands:
  create <id> <title> [severity 1-4]     open a new incident
  list                                   all incidents
  show <id>                              one incident, in full
  title <id> <new title>                 rename
  status <id> <open|mitigated|resolved>  move status
  severity <id> <1-4>                    set severity outright
  escalate <id> <1-4>                    raise severity (never lowers)
  handoff <id> <to>                      transfer command (strict)
  task add <id> <task-id> <text>         add a checklist task
  task claim <id> <task-id>              take a task (if unclaimed)
  task done <id> <task-id>               mark a task done
  task rm <id> <task-id>                 remove a task
  close <id>                             close (guarded: must be resolved)
  open <id>                              live TUI: record, shared notes, presence
  history <id>                           the audit log
  undo <id> <seq>                        reverse one audit entry
  compact <id> [keep]                    collapse old audit entries (default keep 10)
  watch <id>                             poll and print changes as they land

flags:
`)
	flag.PrintDefaults()
}

func run(c *client.Client, args []string) error {
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "create":
		if len(rest) < 2 {
			return fmt.Errorf("usage: create <id> <title> [severity]")
		}
		sev := model.Sev3
		if len(rest) > 2 {
			n, err := strconv.Atoi(rest[2])
			if err != nil {
				return fmt.Errorf("severity: %w", err)
			}
			sev = model.Severity(n)
		}
		inc, err := c.Create(model.Incident{
			ID: rest[0], Title: rest[1], Severity: sev, Status: model.StatusOpen,
		})
		if err != nil {
			return err
		}
		fmt.Printf("created %s (%s)\n", inc.ID, inc.Severity)
		return nil

	case "list":
		incs, err := c.List()
		if err != nil {
			return err
		}
		if len(incs) == 0 {
			fmt.Println("no incidents")
			return nil
		}
		for _, inc := range incs {
			open := 0
			for _, t := range inc.Tasks {
				if !t.Done {
					open++
				}
			}
			fmt.Printf("%-10s %s %-10s %2d open tasks  %s\n",
				inc.ID, inc.Severity, inc.Status, open, inc.Title)
		}
		return nil

	case "show":
		if len(rest) != 1 {
			return fmt.Errorf("usage: show <id>")
		}
		inc, err := c.Get(rest[0])
		if err != nil {
			return err
		}
		printIncident(inc)
		return nil

	case "title":
		if len(rest) < 2 {
			return fmt.Errorf("usage: title <id> <new title>")
		}
		return patch(c, rest[0], client.SetTitle(strings.Join(rest[1:], " ")))

	case "status":
		if len(rest) != 2 {
			return fmt.Errorf("usage: status <id> <status>")
		}
		return patch(c, rest[0], client.SetStatus(model.Status(rest[1])))

	case "severity", "escalate":
		if len(rest) != 2 {
			return fmt.Errorf("usage: %s <id> <1-4>", cmd)
		}
		n, err := strconv.Atoi(rest[1])
		if err != nil {
			return fmt.Errorf("severity: %w", err)
		}
		p := client.SetSeverity(model.Severity(n))
		if cmd == "escalate" {
			p = client.Escalate(model.Severity(n))
		}
		return patch(c, rest[0], p)

	case "handoff":
		if len(rest) != 2 {
			return fmt.Errorf("usage: handoff <id> <to>")
		}
		// Strictness needs the sender's belief about who commands now; fetch
		// it, and if it changes between this read and the apply, the server
		// refuses rather than overriding whoever got there first.
		inc, err := c.Get(rest[0])
		if err != nil {
			return err
		}
		return patch(c, rest[0], client.Handoff(inc.Commander, rest[1]))

	case "task":
		if len(rest) < 3 {
			return fmt.Errorf("usage: task <add|claim|done|rm> <id> <task-id> [text]")
		}
		sub, id, taskID := rest[0], rest[1], rest[2]
		switch sub {
		case "add":
			if len(rest) < 4 {
				return fmt.Errorf("usage: task add <id> <task-id> <text>")
			}
			return patch(c, id, client.AddTask(model.Task{ID: taskID, Text: strings.Join(rest[3:], " ")}))
		case "claim":
			return patch(c, id, client.ClaimTask(taskID, c.Author()))
		case "done":
			return patch(c, id, client.CompleteTask(taskID))
		case "rm":
			return patch(c, id, client.RemoveTask(taskID))
		}
		return fmt.Errorf("unknown task subcommand %q", sub)

	case "close":
		if len(rest) != 1 {
			return fmt.Errorf("usage: close <id>")
		}
		return patch(c, rest[0], client.Close())

	case "history":
		if len(rest) != 1 {
			return fmt.Errorf("usage: history <id>")
		}
		log, err := c.History(rest[0])
		if err != nil {
			return err
		}
		for _, entry := range log {
			note := ""
			if entry.Note != "" {
				note = " (" + entry.Note + ")"
			}
			fmt.Printf("#%d %s %s%s\n", entry.Seq, entry.Time.Format(time.RFC3339), entry.Author, note)
			for line := range strings.Lines(entry.Patch.String()) {
				fmt.Printf("    %s", line)
			}
			fmt.Println()
		}
		return nil

	case "compact":
		if len(rest) < 1 || len(rest) > 2 {
			return fmt.Errorf("usage: compact <id> [keep]")
		}
		keep := 10
		if len(rest) == 2 {
			n, err := strconv.Atoi(rest[1])
			if err != nil {
				return fmt.Errorf("keep: %w", err)
			}
			keep = n
		}
		entries, err := c.Compact(rest[0], keep)
		if err != nil {
			return err
		}
		fmt.Printf("audit log now holds %d entries\n", entries)
		return nil

	case "undo":
		if len(rest) != 2 {
			return fmt.Errorf("usage: undo <id> <seq>")
		}
		seq, err := strconv.ParseInt(rest[1], 10, 64)
		if err != nil {
			return fmt.Errorf("seq: %w", err)
		}
		res, err := c.Undo(rest[0], seq)
		if err != nil {
			return err
		}
		printResult(res)
		return nil

	case "watch":
		if len(rest) != 1 {
			return fmt.Errorf("usage: watch <id>")
		}
		return watch(c, rest[0])

	case "open":
		if len(rest) != 1 {
			return fmt.Errorf("usage: open <id>")
		}
		return tui.Run(c, rest[0])
	}
	return fmt.Errorf("unknown command %q", cmd)
}

// patch submits and narrates: applied operations, skips (a condition met
// reality and stood down), and refusals all print as what they mean.
func patch(c *client.Client, id string, p deep.Patch[model.Incident]) error {
	res, err := c.Patch(id, p)
	if err != nil {
		return err
	}
	printResult(res)
	return nil
}

func printResult(res server.Result) {
	switch {
	case res.Applied == 0 && res.Skipped > 0:
		for _, o := range res.Outcomes {
			if o.Status == "skipped" {
				fmt.Printf("no change: %s (condition not met — someone got there first?)\n", o.Path)
			}
		}
	case res.Seq != 0:
		fmt.Printf("applied as audit entry #%d\n", res.Seq)
	default:
		fmt.Println("no change")
	}
}

func printIncident(inc model.Incident) {
	fmt.Printf("%s: %s\n", inc.ID, inc.Title)
	fmt.Printf("  %s, %s", inc.Severity, inc.Status)
	if inc.Commander != "" {
		fmt.Printf(", commanded by %s", inc.Commander)
	}
	fmt.Println()
	if len(inc.Services) > 0 {
		fmt.Printf("  services: %s\n", strings.Join(inc.Services, ", "))
	}
	if len(inc.Hosts) > 0 {
		hosts := make([]string, len(inc.Hosts))
		for i, h := range inc.Hosts {
			hosts[i] = h.String()
		}
		fmt.Printf("  hosts: %s\n", strings.Join(hosts, ", "))
	}
	for _, t := range inc.Tasks {
		mark := " "
		if t.Done {
			mark = "x"
		}
		owner := ""
		if t.Owner != "" {
			owner = " @" + t.Owner
		}
		fmt.Printf("  [%s] %s: %s%s\n", mark, t.ID, t.Text, owner)
	}
	fmt.Printf("  updated %s\n", inc.Updated.Format(time.RFC3339))
}

// watch polls and diffs: the change feed is deep.Diff between consecutive
// snapshots, rendered as it lands. What the server did with patches, the
// watcher recovers with diffs.
func watch(c *client.Client, id string) error {
	prev, err := c.Get(id)
	if err != nil {
		return err
	}
	fmt.Printf("watching %s (^C to stop)\n", id)
	for {
		time.Sleep(2 * time.Second)
		cur, err := c.Get(id)
		if err != nil {
			return err
		}
		p, err := deep.Diff(prev, cur)
		if err != nil {
			return err
		}
		if !p.IsEmpty() {
			fmt.Printf("--- %s\n", time.Now().Format(time.TimeOnly))
			for line := range strings.Lines(p.String()) {
				fmt.Printf("  %s", line)
			}
			fmt.Println()
		}
		prev = cur
	}
}
