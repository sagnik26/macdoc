package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"

	"github.com/sagnik26/macdoc/internal/executor"
	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/planner"
)

// `macdoc reclaim --choose` (ADR 0018): at a terminal, a list of what the fresh scan
// found that macdoc can delete, with nothing ticked at the start. space ticks a row, a
// ticks all, Enter takes the ticked ones to a plan that is shown and confirmed with y/N,
// and esc cancels. It is the terminal twin of the dashboard's /clean.

// ItemPicker is the tick list of `reclaim --choose`.
type ItemPicker struct {
	t        *Theme
	ops      []model.Opportunity
	cursor   int
	ticked   map[string]bool
	w, h     int
	hint     string
	done     bool
	canceled bool
}

// NewItemPicker returns a picker over ops with nothing ticked.
func NewItemPicker(t *Theme, ops []model.Opportunity) ItemPicker {
	return ItemPicker{t: t, ops: ops, ticked: map[string]bool{}, w: 100, h: 30}
}

// Chosen returns the ticked items in list order.
func (m ItemPicker) Chosen() []model.Opportunity {
	var out []model.Opportunity
	for _, op := range m.ops {
		if m.ticked[op.ID] {
			out = append(out, op)
		}
	}
	return out
}

// Done reports whether the person submitted or canceled.
func (m ItemPicker) Done() bool { return m.done }

// Canceled reports whether the person canceled.
func (m ItemPicker) Canceled() bool { return m.canceled }

// Init implements tea.Model.
func (m ItemPicker) Init() tea.Cmd { return nil }

// Update implements tea.Model.
func (m ItemPicker) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if ws, ok := msg.(tea.WindowSizeMsg); ok {
		m.w, m.h = ws.Width, ws.Height
		return m, nil
	}
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	m.hint = ""
	n := len(m.ops)
	switch s := k.String(); s {
	case "space", "x":
		if m.cursor < n {
			m.ticked[m.ops[m.cursor].ID] = !m.ticked[m.ops[m.cursor].ID]
			m.cursor++
		}
	case "a":
		all := len(m.Chosen()) == n
		for _, op := range m.ops {
			m.ticked[op.ID] = !all
		}
	case "enter":
		if len(m.Chosen()) == 0 {
			m.hint = "Tick something first (space), or press esc to cancel."
			return m, nil
		}
		m.done = true
		return m, tea.Quit
	case "esc", "q", "ctrl+c":
		m.canceled, m.done = true, true
		return m, tea.Quit
	default:
		m.cursor = chooseMove(m.cursor, s, n)
	}
	m.cursor = max(0, min(m.cursor, n-1))
	return m, nil
}

// View implements tea.Model: the list, drawn in place (no alternate screen).
func (m ItemPicker) View() tea.View { return tea.NewView(m.Screen()) }

// Screen is the list as text.
func (m ItemPicker) Screen() string {
	t := m.t
	var b strings.Builder
	b.WriteString("\n " + t.heading("Choose what to delete") + t.muted.Render(fmt.Sprintf("  · item %d of %d", m.cursor+1, len(m.ops))) + "\n\n")
	rowsFit := max(3, m.h-9)
	start := max(0, min(m.cursor-rowsFit/2, len(m.ops)-rowsFit))
	end := min(len(m.ops), start+rowsFit)
	b.WriteString(indentLines(opsGrid(t, m.ticked, m.ops[start:end], m.cursor-start, m.w-2, m.w < 100, true), " "))
	chosen := m.Chosen()
	note := t.muted.Render("nothing ticked yet")
	if len(chosen) > 0 {
		note = t.accent.Render(fmt.Sprintf("%d ticked · %s", len(chosen), sizeSummary(chosen)))
	}
	b.WriteString("\n " + note + t.muted.Render("  · space ticks · a ticks all · enter deletes the ticked ones · esc cancels") + "\n")
	if m.hint != "" {
		b.WriteString(" " + t.warn.Render(m.hint) + "\n")
	}
	return b.String()
}

// pickItems runs the list on the terminal and returns the ticked items, or canceled. in
// and out must be the real terminal.
func pickItems(in, out *os.File, t *Theme, ops []model.Opportunity) (picked []model.Opportunity, canceled bool, err error) {
	final, err := tea.NewProgram(NewItemPicker(t, ops), tea.WithInput(in), tea.WithOutput(out)).Run()
	if err != nil {
		return nil, false, err
	}
	m, ok := final.(ItemPicker)
	if !ok || m.canceled {
		return nil, true, nil
	}
	return m.Chosen(), false, nil
}

// chooseOnly is what `reclaim --choose` offers: everything that can be deleted, with
// advice-only and blocked items left out (and installed apps, which are for retire).
func chooseOnly(ops []model.Opportunity) []model.Opportunity {
	var out []model.Opportunity
	for _, op := range withoutInstalledApps(ops) {
		if op.Method == model.MethodAdvise || op.Risk == model.RiskAdvise || op.Risk == model.RiskBlocked {
			continue
		}
		out = append(out, op)
	}
	return out
}

// cleanItems is `reclaim --choose`: scan, list, pick, show the plan, y/N, delete.
func cleanItems(cmd *cobra.Command, g *GlobalFlags, o cleanOpts) error {
	out := cmd.OutOrStdout()
	home, err := UserHome()
	if err != nil {
		return err
	}
	if err := ensureFreshScan(cmd, home); err != nil {
		return err
	}
	res, _, err := loadOpportunities(context.Background(), minBytesFor(cmd))
	if err != nil {
		return err
	}
	res = forReclaim(res)
	ops := chooseOnly(forToolchain(res.Opportunities, o.toolchain))
	t := themeFor(out)
	if len(ops) == 0 {
		_, _ = fmt.Fprintln(out, t.done("Nothing here macdoc can delete."))
		return nil
	}
	picked, canceled, err := choosePicker(cmd, t, ops)
	if err != nil {
		return err
	}
	if canceled || len(picked) == 0 {
		_, _ = fmt.Fprintln(out, " "+t.done("Canceled. Nothing has been changed."))
		return nil
	}
	return sealAndDelete(cmd, g, picked)
}

// choosePicker shows the list. At a real terminal it is the tick list; otherwise (a faked
// terminal in tests) it falls back to a numbered list and the numbers typed.
func choosePicker(cmd *cobra.Command, t *Theme, ops []model.Opportunity) (picked []model.Opportunity, canceled bool, err error) {
	if in, ok := cmd.InOrStdin().(*os.File); ok {
		if out, ok := cmd.OutOrStdout().(*os.File); ok {
			return pickItems(in, out, t, ops)
		}
	}
	out := cmd.OutOrStdout()
	_, _ = fmt.Fprintln(out, "\n "+t.heading("Choose what to delete"))
	for i, op := range ops {
		_, _ = fmt.Fprintf(out, "  %3d  %-7s %9s  %s\n", i+1, op.Risk, sizeCell(op.Bytes), op.Title)
	}
	_, _ = fmt.Fprint(out, "\n Type the numbers to delete (for example 1 3), all, or press Enter to cancel: ")
	rd := bufio.NewReader(cmd.InOrStdin())
	cmd.SetIn(rd) // the y/N that follows reads from the same buffer, not a second one
	line, err := readLine(rd)
	if err != nil {
		return nil, false, errors.New("no answer; nothing has been changed")
	}
	idx, ok := parsePick(line, len(ops))
	if !ok {
		return nil, true, nil
	}
	for _, i := range idx {
		picked = append(picked, ops[i])
	}
	return picked, false, nil
}

// parsePick reads the numbers typed (1-based, separated by spaces or commas), or all. ok
// is false for an empty line, a number out of range or anything else: nothing is chosen.
func parsePick(line string, n int) (idx []int, ok bool) {
	line = strings.ToLower(strings.TrimSpace(line))
	if line == "" {
		return nil, false
	}
	if line == "all" || line == "a" {
		for i := 0; i < n; i++ {
			idx = append(idx, i)
		}
		return idx, n > 0
	}
	seen := map[int]bool{}
	for _, f := range strings.FieldsFunc(line, func(r rune) bool { return r == ' ' || r == ',' }) {
		v, err := strconv.Atoi(f)
		if err != nil || v < 1 || v > n {
			return nil, false
		}
		if !seen[v-1] {
			seen[v-1] = true
			idx = append(idx, v-1)
		}
	}
	slices.Sort(idx)
	return idx, len(idx) > 0
}

// sealAndDelete seals a plan from exactly the ticked items, shows it, and deletes it
// after a y/N. A plan with an administrator step keeps apply's own confirmation.
func sealAndDelete(cmd *cobra.Command, g *GlobalFlags, picked []model.Opportunity) error {
	out := cmd.OutOrStdout()
	ids := make([]string, len(picked))
	system := false
	for i, op := range picked {
		ids[i] = op.ID
		system = system || op.Method == model.MethodSystem
	}
	plan, home, err := sealPlan(g, planner.Selector{IDs: ids})
	if err != nil {
		return finishPlan(out, plan, err)
	}
	var b strings.Builder
	writeRefused(&b, themeFor(out), plan.Refused)
	_, _ = io.WriteString(out, b.String())
	confirm := ConfirmDelete
	if system {
		confirm = typedConfirm
	}
	return runApply(cmd, ApplyDeps{Home: home, Now: time.Now}, plan.Plan.ID, confirm, true)
}

// ConfirmDelete asks "Delete these N items? [y/N]" for a plan the person just ticked and
// was shown. y is the approval of every item, RISKY ones included, since each was ticked
// one by one; anything else cancels and changes nothing.
func ConfirmDelete(rd *bufio.Reader, out io.Writer, t *Theme, p model.Plan) (executor.Approvals, bool, error) {
	if p.Origin != model.OriginCLI && p.Origin != "" {
		return nil, false, errors.New("only a plan you made here is deleted on your answer")
	}
	for _, it := range p.Items {
		if it.Privileged || it.Method == model.MethodAdvise {
			return nil, false, fmt.Errorf("%s needs apply's own confirmation", it.OpportunityID)
		}
	}
	_, _ = fmt.Fprintf(out, "\n Delete these %d %s for good? [y/N] ", len(p.Items), plural(len(p.Items), "item", "items"))
	line, err := readLine(rd)
	if err != nil {
		return nil, false, errors.New("no answer; nothing has been changed")
	}
	if a := strings.ToLower(line); a != "y" && a != "yes" {
		_, _ = fmt.Fprintln(out, " "+t.done("Canceled. Nothing has been changed."))
		return nil, false, nil
	}
	approved := executor.Approvals{}
	for _, it := range p.Items {
		approved[it.OpportunityID] = executor.Approval{Individually: true}
	}
	_, _ = fmt.Fprintln(out)
	return approved, true, nil
}
