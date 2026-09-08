// dispatch is the office tool: online, authoritative, no local copy. It
// edits assets directly with patches, which is what makes it the other half
// of every conflict a technician's device has to reconcile.
//
//	dispatch list
//	dispatch show pump-7
//	dispatch assign pump-7 bruno
//	dispatch status pump-7 attention
//	dispatch note pump-7 "scada flagged drift"
//	dispatch create tank-9 "Settling tank 9" hilltop
//	dispatch history pump-7
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	deep "github.com/brunoga/deep/v6"

	"github.com/brunoga/deep/examples/fieldwork/client"
	"github.com/brunoga/deep/examples/fieldwork/model"
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
	author := flag.String("author", envOr("FIELD_AUTHOR", "dispatch"), "who the change is attributed to")
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
	fmt.Fprint(os.Stderr, `usage: dispatch [flags] <command> [args]

commands:
  list                        every asset with its version
  show <id>                   one asset
  create <id> <name> <site>   register a new asset
  assign <id> <who>           set the assignee
  status <id> <status>        set the status (ok|attention|fault|offline)
  note <id> <text...>         append a line to the notes
  history <id>                the asset's version log

flags:
`)
	flag.PrintDefaults()
}

func run(c *client.Client, args []string) error {
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "list":
		resp, err := c.Assets()
		if err != nil {
			return err
		}
		for _, a := range resp.Assets {
			fmt.Printf("v%-3d %-10s %-10s %-10s %s\n",
				resp.Versions[a.ID], a.ID, a.Status, a.Site, a.Name)
		}
		return nil

	case "show":
		if len(rest) != 1 {
			return fmt.Errorf("usage: show <id>")
		}
		resp, err := c.Get(rest[0])
		if err != nil {
			return err
		}
		fmt.Printf("%s: %s (version %d)\n", resp.Asset.ID, resp.Asset.Name, resp.Version)
		fmt.Printf("  %s at %s", resp.Asset.Status, resp.Asset.Site)
		if resp.Asset.Assignee != "" {
			fmt.Printf(", assigned to %s", resp.Asset.Assignee)
		}
		fmt.Println()
		for sensor, r := range resp.Asset.Readings {
			fmt.Printf("    %-8s %g %s (%s)\n", sensor, r.Value, r.Unit, r.By)
		}
		for _, ch := range resp.Asset.Checks {
			mark := " "
			if ch.Done {
				mark = "x"
			}
			fmt.Printf("  [%s] %s: %s\n", mark, ch.ID, ch.Label)
		}
		if resp.Asset.Notes != "" {
			fmt.Printf("  notes:\n    %s\n", strings.ReplaceAll(resp.Asset.Notes, "\n", "\n    "))
		}
		return nil

	case "create":
		if len(rest) != 3 {
			return fmt.Errorf("usage: create <id> <name> <site>")
		}
		created, err := c.Create(model.Asset{
			ID: rest[0], Name: rest[1], Site: rest[2], Status: model.StatusOK,
		})
		if err != nil {
			return err
		}
		fmt.Printf("created %s at version %d\n", created.Asset.ID, created.Version)
		return nil

	case "assign", "status", "note":
		if len(rest) < 2 {
			return fmt.Errorf("usage: %s <id> <value...>", cmd)
		}
		id := rest[0]
		cur, err := c.Get(id)
		if err != nil {
			return err
		}
		next := deep.Clone(cur.Asset)
		switch cmd {
		case "assign":
			next.Assignee = rest[1]
		case "status":
			s := model.Status(rest[1])
			if !s.Valid() {
				return fmt.Errorf("unknown status %q", rest[1])
			}
			next.Status = s
		case "note":
			text := strings.Join(rest[1:], " ")
			if next.Notes == "" {
				next.Notes = text
			} else {
				next.Notes += "\n" + text
			}
		}
		patch, err := deep.Diff(cur.Asset, next)
		if err != nil {
			return err
		}
		if patch.IsEmpty() {
			fmt.Println("no change")
			return nil
		}
		updated, err := c.Change(id, patch)
		if err != nil {
			return err
		}
		fmt.Printf("%s now at version %d\n", id, updated.Version)
		return nil

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
