package cli

import (
	"errors"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"macdoc/internal/engine"
	"macdoc/internal/model"
	"macdoc/internal/planner"
)

// nowFunc is the clock for plan expiry shown in the dashboard (tests fix it).
var nowFunc = time.Now

// toggleMark marks or unmarks the selected suggestion for a plan.
// Advice-only items can't be marked: macdoc takes no action on them.
func (m *tuiModel) toggleMark() {
	ops := m.items()
	if m.cursor >= len(ops) {
		return
	}
	op := ops[m.cursor]
	if op.Method == model.MethodAdvise || op.Risk == model.RiskAdvise {
		m.flash = "Advice-only items can't go into a plan: macdoc takes no action on them."
		return
	}
	if m.marked[op.ID] {
		delete(m.marked, op.ID)
	} else {
		m.marked[op.ID] = true
	}
	m.moveCursor("down")
}

// markedSelector plans the marked suggestions in ranked order, or the
// selected one if none is marked. It returns nil (with a flash) if there is
// nothing to plan.
func (m *tuiModel) markedSelector() *planner.Selector {
	var ids []string
	for _, op := range m.items() {
		if m.marked[op.ID] {
			ids = append(ids, op.ID)
		}
	}
	if len(ids) == 0 {
		ops := m.items()
		if m.cursor >= len(ops) {
			m.flash = "Nothing to plan."
			return nil
		}
		op := ops[m.cursor]
		if op.Method == model.MethodAdvise || op.Risk == model.RiskAdvise {
			m.flash = "That one is advice only. Mark items with space, then press p."
			return nil
		}
		ids = []string{op.ID}
	}
	return &planner.Selector{IDs: ids}
}

// markedTotal is the number of marked items and their known bytes.
func (m tuiModel) markedTotal() (int, int64) {
	n, sum := 0, int64(0)
	for _, op := range m.items() {
		if m.marked[op.ID] {
			n++
			if op.Bytes.Source != model.SourceUnknown {
				sum += op.Bytes.Value
			}
		}
	}
	return n, sum
}

// startPlan seals a plan in the background. Making a plan writes only the
// plan file; nothing on the Mac changes.
func (m tuiModel) startPlan(sel *planner.Selector) (tea.Model, tea.Cmd) {
	if sel == nil || m.planning {
		return m, nil
	}
	if m.data == nil || m.deps.makePlan == nil {
		m.flash = "Check your Mac first: plans are made from the latest scan."
		return m, nil
	}
	m.planning = true
	m.flash = "Sealing a dry-run plan…"
	deps, pre, s := m.deps, m.data.planOps, *sel
	return m, func() tea.Msg {
		res, err := deps.makePlan(deps, s, pre)
		return planMsg{res: res, err: err}
	}
}

// planned shows a sealed plan, or why none was made.
func (m tuiModel) planned(msg planMsg) (tea.Model, tea.Cmd) {
	m.planning = false
	switch {
	case msg.err == nil:
		m.plan = &msg.res
		m.overlay, m.scroll = overlayPlan, 0
		m.flash = "Plan " + msg.res.Plan.ID + " sealed. Nothing has been changed. Press a to apply it (you confirm in the terminal), esc to close."
		clear(m.marked)
	case errors.Is(msg.err, planner.ErrNothing):
		reason := "none of them can be acted on"
		if len(msg.res.Refused) > 0 {
			reason = msg.res.Refused[0].ID + ": " + msg.res.Refused[0].Reason
		}
		m.flash = "Nothing to plan: " + reason
	default:
		m.flash = "Couldn't make a plan: " + msg.err.Error()
	}
	return m, nil
}

// planView is the sealed plan's dry run.
func (m tuiModel) planView() string {
	if m.plan == nil {
		return ""
	}
	var buf strings.Builder
	saved := m.t.width
	m.t.width = m.w - 2
	_ = renderPlan(&themedWriter{Writer: &buf, t: m.t}, *m.plan, m.deps.home, nowFunc())
	m.t.width = saved
	return "\n" + indentLines(buf.String(), " ")
}

// markedNote is the list header's marked summary, "" if nothing is marked.
func (m tuiModel) markedNote() string {
	n, sum := m.markedTotal()
	if n == 0 {
		return ""
	}
	return m.t.accent.Render(fmt.Sprintf("✓ %d marked · %s", n, engine.DecimalBytes(sum))) + m.t.muted.Render(" · enter remove")
}
