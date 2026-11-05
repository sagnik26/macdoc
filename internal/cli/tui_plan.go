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

// NowFunc is the clock for plan expiry shown in the dashboard (tests fix it).
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

// markedSelector plans the marked suggestions in ranked order, or the
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

// startPlan seals a plan in the background. Making a plan writes only the
// plan file; nothing on the Mac changes.
func (m TuiModel) startPlan(sel *planner.Selector) (tea.Model, tea.Cmd) {
	if sel == nil || m.Planning {
		return m, nil
	}
	if m.Data == nil || m.Deps.MakePlan == nil {
		m.Flash = "Check your Mac first: plans are made from the latest scan."
		return m, nil
	}
	m.Planning = true
	m.Flash = "Sealing a dry-run plan…"
	deps, pre, s := m.Deps, m.Data.PlanOps, *sel
	return m, func() tea.Msg {
		res, err := deps.MakePlan(deps, s, pre)
		return PlanMsg{Res: res, Err: err}
	}
}

// planned shows a sealed plan, or why none was made.
func (m TuiModel) planned(msg PlanMsg) (tea.Model, tea.Cmd) {
	m.Planning = false
	switch {
	case msg.Err == nil:
		m.Plan = &msg.Res
		m.Overlay, m.scroll = OverlayPlan, 0
		m.Flash = "Plan " + msg.Res.Plan.ID + " sealed. Nothing has been changed. Press a to apply it (you confirm in the terminal), esc to close."
		clear(m.Marked)
	case errors.Is(msg.Err, planner.ErrNothing):
		reason := "none of them can be acted on"
		if len(msg.Res.Refused) > 0 {
			reason = msg.Res.Refused[0].ID + ": " + msg.Res.Refused[0].Reason
		}
		m.Flash = "Nothing to plan: " + reason
	default:
		m.Flash = "Couldn't make a plan: " + msg.Err.Error()
	}
	return m, nil
}

// planView is the sealed plan's dry run.
func (m TuiModel) planView() string {
	if m.Plan == nil {
		return ""
	}
	var buf strings.Builder
	saved := m.t.width
	m.t.width = m.w - 2
	_ = RenderPlan(&themedWriter{Writer: &buf, t: m.t}, *m.Plan, m.Deps.Home, NowFunc())
	m.t.width = saved
	return "\n" + indentLines(buf.String(), " ")
}

// markedNote is the list header's marked summary, "" if nothing is marked.
func (m TuiModel) markedNote() string {
	n, sum := m.markedTotal()
	if n == 0 {
		return ""
	}
	return m.t.accent.Render(fmt.Sprintf("✓ %d marked · %s", n, engine.DecimalBytes(sum))) + m.t.muted.Render(" · enter remove")
}
