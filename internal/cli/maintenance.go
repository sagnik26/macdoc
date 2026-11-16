package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"github.com/sagnik26/macdoc/internal/engine"
	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/planner"
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

// openMaintenance opens the refresh page: the maintenance steps you can ask
// for, as a table with an Apply button on each. It needs no dashboard data.
func (m TuiModel) openMaintenance() (tea.Model, tea.Cmd) {
	res, err := m.Deps.maintenance(context.Background())
	if err != nil {
		m.Flash = "Couldn't list the maintenance steps: " + err.Error()
		return m, nil
	}
	m.maint, m.maintCursor = &res, 0
	m.Overlay, m.scroll = overlayMaintenance, 0
	return m, nil
}

// maintenanceKey handles the refresh page's keys. Enter (or a) seals a plan
// for the highlighted step and hands it to apply, which asks you to confirm in
// Terminal. ok is false for the keys it doesn't use.
func (m TuiModel) maintenanceKey(k string) (tea.Model, tea.Cmd, bool) {
	if m.maint == nil {
		return m, nil, false
	}
	n := len(m.maint.Opportunities)
	switch k {
	case "up", "k":
		m.maintCursor = max(0, m.maintCursor-1)
	case "down", "j":
		m.maintCursor = max(0, min(n-1, m.maintCursor+1))
	case "enter", "a":
		if m.maintCursor >= n {
			return m, nil, true
		}
		next, cmd := m.sealStep(m.maint.Opportunities[m.maintCursor].ID)
		return next, cmd, true
	default:
		return m, nil, false
	}
	return m, nil, true
}

// sealStep seals a plan for one maintenance step and, once it exists, hands it
// to apply. Sealing writes only the plan file; nothing on the Mac changes until
// the person confirms in Terminal.
func (m TuiModel) sealStep(id string) (tea.Model, tea.Cmd) {
	if m.Planning || m.Deps.MakePlan == nil {
		return m, nil
	}
	m.Planning, m.sealForApply = true, true
	m.Flash = "Sealing a plan for " + id + "…"
	deps := m.Deps
	return m, func() tea.Msg {
		res, err := deps.MakePlan(deps, planner.Selector{IDs: []string{id}}, nil)
		return PlanMsg{Res: res, Err: err}
	}
}

// stepNeedsPassword reports whether a step runs with administrator rights.
func (m TuiModel) stepNeedsPassword(op model.Opportunity) bool {
	if m.Deps.cat == nil {
		return false // no catalog (a test): nothing to look the recipe up in
	}
	r, ok := m.Deps.cat.Get(op.RecipeID)
	return ok && r.Privileged
}

// maintenanceBody is the refresh page: a table of the steps, each with an Apply
// button, and what the highlighted one does.
func (m TuiModel) maintenanceBody() string {
	t := m.t
	if m.maint == nil || len(m.maint.Opportunities) == 0 {
		return "\n" + t.banner(colCyan, "ⓘ", "No maintenance steps apply to this Mac", "Nothing needs refreshing right now.")
	}
	res := display(*m.maint)
	ops := res.Opportunities
	rows := make([][]string, len(ops))
	needPassword := 0
	for i, op := range ops {
		password := "No"
		if m.stepNeedsPassword(op) {
			password = "Yes"
			needPassword++
		}
		button := "[ Apply ]"
		if i == m.maintCursor {
			button = "▶ Apply ◀"
		}
		rows[i] = []string{op.ID, op.Title, password, riskLabel(op.Risk), Truncate(RestoreNote(op), 40), button}
	}
	cols := []Column{{Title: "ID"}, {Title: "Step", Flex: true}, {Title: "Mac password"}, {Title: "Risk"}, {Title: "Afterwards", Flex: true}, {Title: "Action"}}
	table := t.Grid(cols, rows, max(40, m.w-4), func(r, c int) *lipgloss.Style {
		var s lipgloss.Style
		switch c {
		case 0:
			s = t.ID
		case 2:
			s = t.muted
			if rows[r][2] == "Yes" {
				s = t.warn
			}
		case 3:
			s = t.riskStyle(ops[r].Risk).Bold(true)
		case 4:
			s = t.muted
		case 5:
			s = t.accent.Bold(true)
		default:
			s = t.text
		}
		if r == m.maintCursor && t.on {
			s = s.Background(colSelect).Bold(true)
		}
		return &s
	})
	head := fmt.Sprintf("%d %s · %d need your Mac password", len(ops), plural(len(ops), "step", "steps"), needPassword)
	out := "\n" + t.block("Maintenance steps", "· "+head, table)
	if m.maintCursor < len(ops) {
		out += "\n" + m.stepDetail(ops[m.maintCursor])
	}
	return out
}

// stepDetail says what the highlighted step does and how it is applied.
func (m TuiModel) stepDetail(op model.Opportunity) string {
	t := m.t
	w := max(30, m.w-8)
	var b strings.Builder
	b.WriteString(t.text.Render(wrapText(op.Explain, w)) + "\n")
	if len(op.Command) > 0 {
		cmd := strings.Join(op.Command, " ")
		if m.stepNeedsPassword(op) {
			cmd = "sudo " + cmd + "   (asks for your Mac password in Terminal)"
		}
		b.WriteString("\n" + t.muted.Render("Runs: ") + t.accent.Render(cmd) + "\n")
	}
	if note := RestoreNote(op); note != "" {
		b.WriteString(t.muted.Render("Afterwards: ") + t.text.Render(wrapText(note, w-12)) + "\n")
	}
	b.WriteString("\n" + t.muted.Render("Press enter to apply: macdoc seals a plan, then asks you to confirm in Terminal."))
	return t.block(op.Title, "", b.String())
}

// wrapText breaks s into lines of at most w cells, at spaces, without padding
// the lines.
func wrapText(s string, w int) string {
	var lines []string
	line := ""
	for _, word := range strings.Fields(s) {
		if line != "" && lipgloss.Width(line)+1+lipgloss.Width(word) > w {
			lines = append(lines, line)
			line = ""
		}
		if line != "" {
			line += " "
		}
		line += word
	}
	if line != "" {
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}
