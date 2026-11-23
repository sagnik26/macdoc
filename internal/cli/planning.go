package cli

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/sagnik26/macdoc/internal/engine"
	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/planner"
)

// NowFunc is the dashboard's clock (tests fix it).
var NowFunc = time.Now

// toggleMark marks or unmarks the selected suggestion for a plan.
// Advice-only items can't be marked: macdoc takes no action on them.
func (m *TuiModel) toggleMark() {
	ops := m.Items()
	if m.Cursor >= len(ops) {
		return
	}
	op := ops[m.Cursor]
	if op.Method == model.MethodAdvise || op.Risk == model.RiskAdvise {
		m.Flash = "Advice-only items can't go into a plan: macdoc takes no action on them."
		return
	}
	if m.Marked[op.ID] {
		delete(m.Marked, op.ID)
	} else {
		m.Marked[op.ID] = true
	}
	m.moveCursor("down")
}

// markCleanable marks the suggestions that the installed programs' cleanups
// produce, and opens them in the Suggestions tab. Advice-only items are left
// out, as toggleMark leaves them out. Nothing is planned or removed here.
func (m TuiModel) markCleanable() (tea.Model, tea.Cmd) {
	if m.software == nil || m.Data == nil {
		m.Flash = "Nothing to mark yet: read the installed software first."
		return m, nil
	}
	sum := softwareCleanups(display(*m.software), m.Items())
	if len(sum.Items) == 0 {
		m.Flash = "No installed program has a cleanup that can be marked."
		return m, nil
	}
	if m.Marked == nil {
		m.Marked = map[string]bool{}
	}
	for _, id := range sum.Items {
		m.Marked[id] = true
	}
	m.Overlay = OverlayNone
	m.setTab(TabSuggestions)
	m.Flash = fmt.Sprintf("Marked %d suggestions from installed software (%s, + %d unknown). Nothing is removed yet: press p to plan it.",
		len(sum.Items), engine.DecimalBytes(sum.Bytes), sum.Unknown)
	return m, nil
}

// preMarkSafe marks the SAFE suggestions of a fresh scan, so the picker
// starts with the items that are safest to remove (Phase 10). Advice-only
// items can't be marked. Nothing is planned or removed by marking: a plan
// still needs p, and removal still needs its confirmation.
func (m *TuiModel) preMarkSafe() {
	for _, op := range m.Items() {
		if op.Risk == model.RiskSafe && op.Method != model.MethodAdvise {
			m.Marked[op.ID] = true
		}
	}
}

// MarkedSelector plans the marked suggestions in ranked order, or the
// selected one if none is marked. It returns nil (with a flash) if there is
// nothing to plan.
func (m *TuiModel) MarkedSelector() *planner.Selector {
	var ids []string
	for _, op := range m.Items() {
		if m.Marked[op.ID] {
			ids = append(ids, op.ID)
		}
	}
	if len(ids) == 0 {
		ops := m.Items()
		if m.Cursor >= len(ops) {
			m.Flash = "Nothing to plan."
			return nil
		}
		op := ops[m.Cursor]
		if op.Method == model.MethodAdvise || op.Risk == model.RiskAdvise {
			m.Flash = "That one is advice only. Mark items with space, then press p."
			return nil
		}
		ids = []string{op.ID}
	}
	return &planner.Selector{IDs: ids}
}

// markedTotal is the number of marked items and their known bytes.
func (m TuiModel) markedTotal() (int, int64) {
	n, sum := 0, int64(0)
	for _, op := range m.Items() {
		if m.Marked[op.ID] {
			n++
			if op.Bytes.Source != model.SourceUnknown {
				sum += op.Bytes.Value
			}
		}
	}
	return n, sum
}

// markedNote is the list header's marked summary, "" if nothing is marked.
func (m TuiModel) markedNote() string {
	n, sum := m.markedTotal()
	if n == 0 {
		return ""
	}
	return m.t.accent.Render(fmt.Sprintf("✓ %d marked · %s", n, engine.DecimalBytes(sum))) + m.t.muted.Render(" · enter remove")
}
