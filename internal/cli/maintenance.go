package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sagnik26/macdoc/internal/engine"
	"github.com/sagnik26/macdoc/internal/recipes"
)

// newMaintenanceCmd builds `macdoc doctor maintenance`: the maintenance steps
// macdoc can plan, such as flushing the DNS cache. Read-only; it lists them
// with the IDs to plan. A step that needs administrator rights is marked, and
// runs only from a plan you make and apply in Terminal (ADR 0009).
func newMaintenanceCmd(g *GlobalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "maintenance",
		Short: "Maintenance steps you can plan, such as flushing DNS (read-only)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := UserHome()
			if err != nil {
				return err
			}
			cat, err := LoadCatalog()
			if err != nil {
				return err
			}
			res, err := engine.Maintenance(context.Background(), engine.OpportunityOptions{Home: home, Runner: Runner, Catalog: cat})
			if err != nil {
				return err
			}
			if g.json {
				return writeJSONWarn(cmd.OutOrStdout(), "doctor maintenance", res, res.Warnings)
			}
			return RenderMaintenance(cmd.OutOrStdout(), res, cat)
		},
	}
}

// RenderMaintenance writes the maintenance steps in the human format.
func RenderMaintenance(w io.Writer, res engine.OpportunityResult, cat *recipes.Catalog) error {
	res = display(res)
	t := themeFor(w)
	var b strings.Builder
	b.WriteString(t.commandHeader("maintenance", res.Scan.ID, res.ScanAge))
	if len(res.Opportunities) == 0 {
		b.WriteString(" No maintenance steps apply to this Mac.\n")
	}
	for _, op := range res.Opportunities {
		r, _ := cat.Get(op.RecipeID)
		note := ""
		if r.Privileged {
			note = "  needs your Mac password"
		}
		fmt.Fprintf(&b, "  %s  %s%s\n", t.ID.Render(op.ID), op.Title, t.muted.Render(note))
	}
	writeNotes(&b, t, res.Warnings)
	b.WriteString(t.footer("macdoc plan <id…> (a dry run), then macdoc apply in Terminal"))
	_, err := io.WriteString(w, b.String())
	return err
}
