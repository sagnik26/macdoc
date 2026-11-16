package cli

import (
	"fmt"
	"slices"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sagnik26/macdoc/internal/engine"
	"github.com/sagnik26/macdoc/internal/model"
)

// /reclaim in the dashboard opens its own page, the suggestions table with the same "What do
// you want to clean?" checkbox list that `macdoc reclaim` shows (TierPicker).
// Enter marks the suggestions of the ticked risk types and hands them to the
// dashboard's own removal: a plan is sealed and the confirmation box opens, so
// nothing is removed until the second Enter there, and RISKY items still need
// their typed IDs. ADVICE can't be ticked.

// openReclaimPicker is the /reclaim command.
func (m TuiModel) openReclaimPicker() (tea.Model, tea.Cmd) {
	if m.Data == nil {
		m.Flash = "Check your Mac first: /reclaim works from the latest scan."
		return m, nil
	}
	rows := TierChoices(m.Items())
	if len(rows[0].Items)+len(rows[1].Items)+len(rows[2].Items) == 0 {
		m.Flash = "Nothing here macdoc can clean."
		return m, nil
	}
	p := NewTierPicker(m.t, rows)
	m.picker = &p
	m.Overlay, m.scroll = overlayReclaim, 0
	return m, nil
}

// reclaimKey sends a key to the checkbox list. Enter starts the removal of what
// is ticked, and Esc closes the list.
func (m TuiModel) reclaimKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.picker == nil {
		m.Overlay = OverlayNone
		return m, nil
	}
	// The picker asks to quit when it finishes; that command is dropped so it
	// can't end the dashboard.
	next, _ := m.picker.Update(msg)
	p, ok := next.(TierPicker)
	if !ok {
		return m, nil
	}
	m.picker = &p
	if !p.Done() {
		return m, nil
	}
	m.Overlay, m.picker = OverlayNone, nil
	if p.Canceled() {
		return m, nil
	}
	return m.markChosenAndRemove(p.Chosen())
}

// markChosenAndRemove marks the actionable suggestions of the chosen risk types
// (and only those) and opens the removal confirmation for them.
func (m TuiModel) markChosenAndRemove(tiers []model.Risk) (tea.Model, tea.Cmd) {
	if m.Marked == nil {
		m.Marked = map[string]bool{}
	}
	clear(m.Marked)
	first := -1
	for i, op := range m.Items() {
		if op.Method == model.MethodAdvise || !slices.Contains(tiers, op.Risk) {
			continue
		}
		m.Marked[op.ID] = true
		if first < 0 {
			first = i
		}
	}
	if first < 0 {
		m.Flash = "Nothing in what you chose."
		return m, nil
	}
	m.Cursor = first // startRemove treats an advice row under the cursor as the whole request
	return m.startRemove()
}

// reclaimBody is the page: the checkbox list and its Clean button first,
// because choosing is the job, then the full table of every suggestion with
// the risk totals above it. The table is for reading only: no cursor and no
// ticks, because the list is what the keys move and tick.
func (m TuiModel) reclaimBody() string {
	if m.picker == nil {
		return ""
	}
	pick := strings.TrimRight(m.picker.View(), "\n")
	ops := m.Items()
	rowsFit := m.bodyHeight() - strings.Count(pick, "\n") - 1 - 6 // pick's own lines, plus blank, totals and the table's frame
	if len(ops) == 0 || rowsFit < 2 {                             // too short for a useful table: just the list
		return pick
	}
	t := m.t
	var totals []string
	for _, tier := range engine.TierOrder {
		if tierOps := opsInTier(ops, tier); len(tierOps) > 0 {
			totals = append(totals, t.riskStyle(tier).Bold(true).Render(riskLabel(tier))+" "+
				t.size.Render(tierTotal(m.Data.Res.Opportunities.Totals[tier], tierOps)))
		}
	}
	shown := ops[:min(len(ops), rowsFit)]
	more := ""
	if len(shown) < len(ops) {
		more = "   " + t.muted.Render(fmt.Sprintf("showing %d of %d", len(shown), len(ops)))
	}
	m.Marked = nil
	return pick + "\n\n " + strings.Join(totals, t.faint.Render("  ·  ")) + more + "\n" +
		indentLines(m.opsTable(shown, -1, m.w-2, false), " ")
}
