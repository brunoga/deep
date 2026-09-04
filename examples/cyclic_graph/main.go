//go:generate go run github.com/brunoga/deep/v6/cmd/deep-gen -type=Team,Person -output team_deep.go .

package main

import (
	"fmt"

	"github.com/brunoga/deep/v6"
)

// A Person belongs to a Team, and a Team holds its members — so following the
// types leads back where it started. Values of these types reference
// themselves: a member's Team is the team that lists them, and a person's
// Manager is another person on the same chart.
//
// deep-gen looks for cycles like this in the package's type graph. When it
// finds one it generates methods that keep track of what they have already
// visited, so a copy rebuilds the cycle instead of following it forever, and a
// value reached twice is copied once. Types that cannot reach a cycle generate
// exactly the code they otherwise would, with no bookkeeping at all.
type Person struct {
	Name    string  `json:"name"`
	Team    *Team   `json:"team"`    // back to the team that lists this person
	Manager *Person `json:"manager"` // and sideways to another person on it
}

type Team struct {
	Name    string             `json:"name"`
	Members []*Person          `json:"members"`
	ByName  map[string]*Person `json:"byName"`
	Lead    *Person            `json:"lead"`
}

// newTeam builds a chart where every reference that should point at one person
// does: the lead appears in Members, in ByName, and as everyone's Manager.
func newTeam(name, leadName string, reports ...string) *Team {
	t := &Team{Name: name, ByName: map[string]*Person{}}

	lead := &Person{Name: leadName, Team: t}
	t.Lead = lead
	t.Members = append(t.Members, lead)
	t.ByName[leadName] = lead

	for _, r := range reports {
		p := &Person{Name: r, Team: t, Manager: lead}
		t.Members = append(t.Members, p)
		t.ByName[r] = p
	}
	return t
}

func main() {
	team := newTeam("platform", "ada", "grace", "alan")

	fmt.Println("== the original ==")
	describe(team)

	// ── Clone ────────────────────────────────────────────────────────────────
	// Nothing special at the call site: Clone is the same method it has always
	// been. What changed is that it can now be called on a value like this one.
	copyOfTeam := team.Clone()

	fmt.Println("\n== the copy ==")
	describe(copyOfTeam)

	fmt.Println("\ncycles rebuilt on the copy, not the original:")
	fmt.Printf("  copy.Lead.Team points back at the copy: %v\n", copyOfTeam.Lead.Team == copyOfTeam)
	fmt.Printf("  ...and not at the original:             %v\n", copyOfTeam.Lead.Team == team)

	fmt.Println("\nthe lead is reached four ways, and is one person in the copy:")
	lead := copyOfTeam.Lead
	fmt.Printf("  Lead == Members[0]:            %v\n", lead == copyOfTeam.Members[0])
	fmt.Printf("  Lead == ByName[\"ada\"]:         %v\n", lead == copyOfTeam.ByName["ada"])
	fmt.Printf("  Lead == Members[1].Manager:    %v\n", lead == copyOfTeam.Members[1].Manager)
	fmt.Printf("  ...and is not the original:    %v\n", lead != team.Lead)

	// Sharing is not a detail: a write through one route is visible from the
	// others, exactly as it was in the original.
	copyOfTeam.ByName["ada"].Name = "ada l."
	fmt.Printf("\nrenaming through ByName shows up on Lead: %q\n", copyOfTeam.Lead.Name)
	fmt.Printf("the original is untouched:                %q\n", team.Lead.Name)

	// ── Equal ────────────────────────────────────────────────────────────────
	// Comparing two values that reference themselves terminates: a pair already
	// under comparison is taken as matching, so the cycle is followed once.
	same := newTeam("platform", "ada", "grace", "alan")
	fmt.Printf("\nfresh team equals the original: %v\n", deep.Equal(*team, *same))

	same.ByName["alan"].Manager = nil
	fmt.Printf("after dropping one manager link: %v\n", deep.Equal(*team, *same))

	// ── Diff and Apply ───────────────────────────────────────────────────────
	// A patch addresses values by path, and the lead is reachable by several of
	// them. The diff descends into a pair of values once, at the first path
	// that reaches it — shared structure can be reachable by exponentially many
	// paths, so repeating the operations at each one is not an option. Every
	// later route becomes a single alias operation instead: "make this path
	// hold the same object that path holds". Applying the patch therefore lands
	// the right values at every route and rebuilds the sharing, whether the
	// target still shared the person (a clone) or was rebuilt without sharing
	// (decoded from JSON, say).
	renamed := newTeam("platform", "ada l.", "grace", "alan")

	patch := team.Diff(renamed)
	fmt.Println("\n== renaming the lead ==")
	for _, op := range patch.Operations {
		if op.From != "" {
			fmt.Printf("  %s %s (from %s)\n", op.Kind, op.Path, op.From)
		} else {
			fmt.Printf("  %s %s\n", op.Kind, op.Path)
		}
	}

	target := team.Clone()
	if err := deep.Apply(target, patch); err != nil {
		panic(err)
	}
	fmt.Println("\nafter applying:")
	fmt.Printf("  Lead.Name               = %q\n", target.Lead.Name)
	fmt.Printf("  Members[0].Name         = %q\n", target.Members[0].Name)
	fmt.Printf("  Members[1].Manager.Name = %q\n", target.Members[1].Manager.Name)
	fmt.Printf("  all one person          = %v\n",
		target.Lead == target.Members[0] && target.Lead == target.Members[1].Manager)
	fmt.Printf("  the cycle still closes  = %v\n", target.Lead.Team == target)
}

func describe(t *Team) {
	fmt.Printf("team %q, lead %q\n", t.Name, t.Lead.Name)
	for _, m := range t.Members {
		manager := "(none)"
		if m.Manager != nil {
			manager = m.Manager.Name
		}
		fmt.Printf("  %-6s manager=%-6s team=%s\n", m.Name, manager, m.Team.Name)
	}
}
