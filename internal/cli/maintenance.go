package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/sagnik26/macdoc/internal/engine"
	"github.com/sagnik26/macdoc/internal/recipes"
)

// newMaintenanceCmd builds `macdoc refresh`: the maintenance steps
// macdoc can plan, such as flushing the DNS cache. Read-only; it lists them
// with the IDs to plan. A step that needs administrator rights is marked, and
// runs only from a plan you make and apply in Terminal (ADR 0009).
func newRefreshCmd(g *GlobalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "refresh",
		Short: "Maintenance steps you can apply, such as flushing DNS (read-only list)",
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
				return writeJSONWarn(cmd.OutOrStdout(), "refresh", res, res.Warnings)
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
	b.WriteString(t.commandHeader("refresh", res.Scan.ID, res.ScanAge))
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
	b.WriteString(t.footer("macdoc apply <id…> (shows the plan, then asks you to confirm in Terminal)"))
	_, err := io.WriteString(w, b.String())
	return err
}

// Maintenance reads the maintenance steps for the dashboard; nil uses the
// engine. Tests replace it.
func (d *TuiDeps) maintenance(ctx context.Context) (engine.OpportunityResult, error) {
	if d.Maintenance != nil {
		return d.Maintenance(ctx, d)
	}
	return engine.Maintenance(ctx, engine.OpportunityOptions{Home: d.Home, Runner: Runner, Catalog: d.cat})
}

// openMaintenance opens the maintenance overlay: the steps you can ask for,
// with their IDs. It only lists them; a plan is made and applied in Terminal.
func (m TuiModel) openMaintenance() (tea.Model, tea.Cmd) {
	if m.Data == nil {
		m.Flash = "Check your Mac first: maintenance steps come from the latest scan."
		return m, nil
	}
	res, err := m.Deps.maintenance(context.Background())
	if err != nil {
		m.Flash = "Couldn't list the maintenance steps: " + err.Error()
		return m, nil
	}
	m.maint = &res
	m.Overlay, m.scroll = overlayMaintenance, 0
	return m, nil
}

// maintenanceBody is the maintenance overlay's text.
func (m TuiModel) maintenanceBody() string {
	t := m.t
	if m.maint == nil || len(m.maint.Opportunities) == 0 {
		return t.block("Maintenance", "", " No maintenance steps apply to this Mac.\n")
	}
	res := display(*m.maint)
	var rows []string
	for _, op := range res.Opportunities {
		note := ""
		if m.Deps.cat == nil {
			// no catalog (a test): nothing to look the recipe up in
		} else if r, ok := m.Deps.cat.Get(op.RecipeID); ok && r.Privileged {
			note = "  needs your Mac password"
		}
		rows = append(rows, fmt.Sprintf(" %s  %s%s", t.ID.Render(op.ID), op.Title, t.muted.Render(note)))
	}
	body := strings.Join(rows, "\n") + "\n\n " + t.muted.Render("Apply one in Terminal: macdoc apply <id> (it shows the plan and asks you to confirm).") + "\n"
	return t.block("Maintenance", "", body)
}
