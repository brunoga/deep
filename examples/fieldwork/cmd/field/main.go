// field is the technician's tool. Every command except sync works with no
// network at all: edits go into a local working copy, and the outbox is
// derived — `field status` is literally a diff between the last state the
// server confirmed and what the technician has done since.
//
//	field pull                       # first download (needs a signal)
//	field list                       # what is on the device
//	field show pump-7
//	field status                     # what this device owes the server
//	field reading pump-7 ph 6.4 pH   # offline edits
//	field check pump-7 seals
//	field status pump-7 fault
//	field note pump-7 "seals leaking"
//	field revert pump-7              # throw local work away
//	field sync                       # push and pull in one round trip
//
// Server, token, author and the on-device state directory come from flags or
// FIELD_SERVER, FIELD_TOKEN, FIELD_AUTHOR and FIELD_STATE.
package main

import (
	"flag"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/brunoga/deep/examples/fieldwork/client"
	"github.com/brunoga/deep/examples/fieldwork/model"
	"github.com/brunoga/deep/examples/fieldwork/server"
)

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	serverURL := flag.String("server", envOr("FIELD_SERVER", "http://localhost:8090"), "server URL")
	token := flag.String("token", os.Getenv("FIELD_TOKEN"), "bearer token")
	author := flag.String("author", envOr("FIELD_AUTHOR", envOr("USER", "tech")), "who this device belongs to")
	state := flag.String("state", envOr("FIELD_STATE", defaultState()), "on-device state directory")
	flag.Usage = usage
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		usage()
		os.Exit(2)
	}

	local, err := client.OpenLocal(*state)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: opening local state:", err)
		os.Exit(1)
	}
	c := client.New(*serverURL, *token, *author)

	if err := run(c, local, args); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func defaultState() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".field"
	}
	return filepath.Join(home, ".field")
}

func usage() {
	fmt.Fprint(os.Stderr, `usage: field [flags] <command> [args]

offline commands (no network):
  list                            assets on this device
  show <id>                       one asset in full
  status                          what this device owes the server
  status <id> <status>            set an asset's status (ok|attention|fault|offline)
  assign <id> <who>               set the assignee
  note <id> <text...>             append a line to the notes
  reading <id> <sensor> <v> [u]   record a measurement
  check <id> <check-id>           mark a checklist item done
  revert <id>                     throw away this asset's local changes

online commands:
  pull                            first download onto a fresh device
  sync                            push local work, pull the office's changes
  history <id>                    the asset's version log on the server

flags:
`)
	flag.PrintDefaults()
}

func run(c *client.Client, local *client.Local, args []string) error {
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "pull", "sync":
		report, err := c.Sync(local)
		if err != nil {
			return err
		}
		printSync(report)
		return nil

	case "list":
		assets := local.List()
		if len(assets) == 0 {
			fmt.Println("nothing on this device yet — run: field pull")
			return nil
		}
		pending, err := local.Pending()
		if err != nil {
			return err
		}
		dirty := map[string]int{}
		for _, p := range pending {
			dirty[p.ID] = len(p.Patch.Operations)
		}
		for _, a := range assets {
			mark := "  "
			if n := dirty[a.ID]; n > 0 {
				mark = fmt.Sprintf("*%d", n)
			}
			fmt.Printf("%s %-10s %-10s %-10s %s\n", mark, a.ID, a.Status, a.Site, a.Name)
		}
		return nil

	case "show":
		if len(rest) != 1 {
			return fmt.Errorf("usage: show <id>")
		}
		a, err := local.Get(rest[0])
		if err != nil {
			return err
		}
		printAsset(a)
		p, err := local.PendingFor(rest[0])
		if err != nil {
			return err
		}
		if !p.Patch.IsEmpty() {
			fmt.Printf("\n  unsynced (from version %d):\n", p.BaseVersion)
			for line := range strings.Lines(p.Patch.String()) {
				fmt.Printf("    %s", line)
			}
			fmt.Println()
		}
		return nil

	case "status":
		// With no arguments this is the outbox; with two it sets a status.
		switch len(rest) {
		case 0:
			return printPending(local)
		case 2:
			s := model.Status(rest[1])
			if !s.Valid() {
				return fmt.Errorf("unknown status %q", rest[1])
			}
			return local.Edit(rest[0], func(a *model.Asset) { a.Status = s })
		}
		return fmt.Errorf("usage: status | status <id> <status>")

	case "assign":
		if len(rest) != 2 {
			return fmt.Errorf("usage: assign <id> <who>")
		}
		return local.Edit(rest[0], func(a *model.Asset) { a.Assignee = rest[1] })

	case "note":
		if len(rest) < 2 {
			return fmt.Errorf("usage: note <id> <text...>")
		}
		text := strings.Join(rest[1:], " ")
		return local.Edit(rest[0], func(a *model.Asset) {
			if a.Notes == "" {
				a.Notes = text
				return
			}
			a.Notes += "\n" + text
		})

	case "reading":
		if len(rest) < 3 || len(rest) > 4 {
			return fmt.Errorf("usage: reading <id> <sensor> <value> [unit]")
		}
		value, err := strconv.ParseFloat(rest[2], 64)
		if err != nil {
			return fmt.Errorf("value: %w", err)
		}
		unit := ""
		if len(rest) == 4 {
			unit = rest[3]
		}
		return local.Edit(rest[0], func(a *model.Asset) {
			if a.Readings == nil {
				a.Readings = map[string]model.Reading{}
			}
			prev := a.Readings[rest[1]]
			if unit == "" {
				unit = prev.Unit
			}
			a.Readings[rest[1]] = model.Reading{Value: value, Unit: unit, By: c.Author()}
		})

	case "check":
		if len(rest) != 2 {
			return fmt.Errorf("usage: check <id> <check-id>")
		}
		found := false
		if err := local.Edit(rest[0], func(a *model.Asset) {
			for i := range a.Checks {
				if a.Checks[i].ID == rest[1] {
					a.Checks[i].Done = true
					a.Checks[i].By = c.Author()
					found = true
					return
				}
			}
		}); err != nil {
			return err
		}
		if !found {
			// Without this the edit is a silent no-op and a mistyped id looks
			// exactly like a completed check.
			return fmt.Errorf("%s has no check %q", rest[0], rest[1])
		}
		return nil

	case "revert":
		if len(rest) != 1 {
			return fmt.Errorf("usage: revert <id>")
		}
		return local.Revert(rest[0])

	case "history":
		if len(rest) != 1 {
			return fmt.Errorf("usage: history <id>")
		}
		log, err := c.History(rest[0])
		if err != nil {
			return err
		}
		for _, e := range log {
			fmt.Printf("v%d %s %s\n", e.Version, e.Time.Format("2006-01-02 15:04"), e.Author)
			for line := range strings.Lines(e.Patch.String()) {
				fmt.Printf("    %s", line)
			}
			fmt.Println()
		}
		return nil
	}
	return fmt.Errorf("unknown command %q", cmd)
}

func printPending(local *client.Local) error {
	pending, err := local.Pending()
	if err != nil {
		return err
	}
	if len(pending) == 0 {
		fmt.Println("nothing to sync")
		return nil
	}
	for _, p := range pending {
		fmt.Printf("%s (from version %d)\n", p.ID, p.BaseVersion)
		for line := range strings.Lines(p.Patch.String()) {
			fmt.Printf("    %s", line)
		}
		fmt.Println()
	}
	return nil
}

func printSync(r client.SyncReport) {
	fmt.Printf("pushed %d, merged %d, received %d\n", r.Pushed, r.Merged, r.Updated)
	for _, res := range r.Conflicts {
		fmt.Printf("\n%s — the office had changed things too:\n", res.ID)
		for _, c := range res.Conflicts {
			switch c.Kept {
			case "mine":
				fmt.Printf("    %s: kept yours%s\n", c.Path, versus(c))
			case "theirs":
				fmt.Printf("    %s: kept the office's%s\n", c.Path, versus(c))
			default:
				fmt.Printf("    %s: combined both\n", c.Path)
			}
		}
	}
	for _, res := range r.Rejected {
		fmt.Printf("\n%s — REJECTED, your work is still on the device:\n    %s\n", res.ID, res.Error)
	}
}

func versus(c server.Conflict) string {
	if len(c.Mine) == 0 && len(c.Theirs) == 0 {
		return ""
	}
	return fmt.Sprintf(" (yours %s, theirs %s)", string(c.Mine), string(c.Theirs))
}

func printAsset(a model.Asset) {
	fmt.Printf("%s: %s\n", a.ID, a.Name)
	fmt.Printf("  %s at %s", a.Status, a.Site)
	if a.Assignee != "" {
		fmt.Printf(", assigned to %s", a.Assignee)
	}
	fmt.Println()
	if len(a.Readings) > 0 {
		fmt.Println("  readings:")
		for _, sensor := range sortedKeys(a.Readings) {
			r := a.Readings[sensor]
			by := ""
			if r.By != "" {
				by = " by " + r.By
			}
			fmt.Printf("    %-8s %g %s%s\n", sensor, r.Value, r.Unit, by)
		}
	}
	for _, c := range a.Checks {
		mark := " "
		if c.Done {
			mark = "x"
		}
		by := ""
		if c.By != "" {
			by = " (" + c.By + ")"
		}
		fmt.Printf("  [%s] %s: %s%s\n", mark, c.ID, c.Label, by)
	}
	if a.Notes != "" {
		fmt.Println("  notes:")
		for line := range strings.Lines(a.Notes) {
			fmt.Printf("    %s\n", strings.TrimRight(line, "\n"))
		}
	}
}

func sortedKeys[V any](m map[string]V) []string {
	return slices.Sorted(maps.Keys(m))
}
