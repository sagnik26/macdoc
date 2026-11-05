package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"macdoc/internal/engine"
	"macdoc/internal/executor"
	"macdoc/internal/model"
	"macdoc/internal/planner"
	"macdoc/internal/store"
)

// Removing from the dashboard: Enter on a suggestion (or on marked ones)
// seals a plan for it and asks once. SAFE and CAUTION items are removed
// after a second Enter; a RISKY item needs the plan's code typed. The
// executor then re-checks every item, removes it and deletes it at once
// (Config.DeleteNow), so the space is freed.

// removeReadyMsg is the plan sealed for a removal.
type removeReadyMsg struct {
	Res engine.PlanResult
	Err error
}

// removeProgressMsg is an item starting or finishing.
type removeProgressMsg executor.ItemEvent

// removedMsg is the end of a removal.
type removedMsg struct {
	rep executor.Report
	Err error
}

// removeState is the confirmation box and, once confirmed, its progress.
type removeState struct {
	Res       engine.PlanResult // what the executor acts on
	view      engine.PlanResult // the same, cleaned for the terminal (display)
	code      string            // typed so far, when a RISKY item needs it
	confirmed int               // RISKY items confirmed so far, in plan order
	running   bool
	lines     []string // finished items
	current   string   // the item being removed
}

// riskyItems returns the plan's RISKY items, in order.
func (r *removeState) riskyItems() []model.PlanItem {
	var out []model.PlanItem
	for _, it := range r.Res.Plan.Items {
		if it.Risk == model.RiskRisky {
			out = append(out, it)
		}
	}
	return out
}

// needsCode reports whether any item is RISKY.
func (r *removeState) needsCode() bool { return len(r.riskyItems()) > 0 }

// nextRisky returns the RISKY item still waiting for its own confirmation.
// Each one is confirmed by typing its ID, so none is approved as a group
// (P6-R1 Q2).
func (r *removeState) nextRisky() (model.PlanItem, bool) {
	rs := r.riskyItems()
	if r.confirmed < len(rs) {
		return rs[r.confirmed], true
	}
	return model.PlanItem{}, false
}

// executorRemove is the real removal: the executor, with every check, and
// each item deleted right after it's moved.
func executorRemove(d *TuiDeps, p model.Plan, onItem func(executor.ItemEvent)) (executor.Report, error) {
	if !RealTTY(os.Stdin, os.Stdout) {
		return executor.Report{}, errors.New("removing needs you at a Terminal")
	}
	st, err := store.Open(store.DefaultPath(d.Home))
	if err != nil {
		return executor.Report{}, err
	}
	defer func() { _ = st.Close() }()
	// SAFE and CAUTION items go with the one Enter on the confirmation box, a
	// group approval. A RISKY item was confirmed on its own: its ID was typed
	// before this runs (removeKey only gets here once every one is typed).
	approved := executor.Approvals{}
	for _, it := range p.Items {
		approved[it.OpportunityID] = executor.Approval{Individually: it.Risk == model.RiskRisky}
	}
	return executor.Apply(context.Background(), executor.Config{
		Store: st, StorePath: store.DefaultPath(d.Home), Home: d.Home, Catalog: d.cat, Runner: Runner, Now: time.Now,
		DeleteNow: true,
		FreeSpace: func(ctx context.Context) (int64, error) { return engine.FreeSpace(ctx, Runner) },
		OnItem:    onItem,
	}, p, approved)
}

// startRemove seals a plan for the marked suggestions (or the selected one)
// and opens the confirmation box. Advice-only items have nothing to remove,
// so Enter explains them instead.
func (m TuiModel) startRemove() (tea.Model, tea.Cmd) {
	// Enter on an advice row always explains it, even when other items are
	// marked (the picker pre-marks SAFE items, so marks are the usual case).
	ops := m.Items()
	if m.Cursor < len(ops) {
		if op := ops[m.Cursor]; op.Method == model.MethodAdvise || op.Risk == model.RiskAdvise {
			m.Detail = !m.Detail
			m.Flash = "macdoc can't remove this one. The steps to do it yourself are below."
			return m, nil
		}
	}
	if n, _ := m.markedTotal(); n == 0 && m.Cursor >= len(ops) {
		return m, nil
	}
	sel := m.MarkedSelector()
	if sel == nil || m.Planning {
		return m, nil
	}
	if m.Data == nil || m.Deps.MakePlan == nil {
		m.Flash = "Check your Mac first."
		return m, nil
	}
	m.Planning = true
	m.Flash = "Checking…"
	deps, pre, s := m.Deps, m.Data.PlanOps, *sel
	return m, func() tea.Msg {
		res, err := deps.MakePlan(deps, s, pre)
		return removeReadyMsg{Res: res, Err: err}
	}
}

func (m TuiModel) removeReady(msg removeReadyMsg) (tea.Model, tea.Cmd) {
	m.Planning = false
	switch {
	case msg.Err == nil:
		m.removing = &removeState{Res: msg.Res, view: display(msg.Res)}
		m.Overlay, m.scroll = OverlayRemove, 0
		clear(m.Marked)
	case errors.Is(msg.Err, planner.ErrNothing):
		reason := "none of them can be removed right now"
		if len(msg.Res.Refused) > 0 {
			reason = msg.Res.Refused[0].Reason
		}
		m.Flash = "Can't remove it: " + reason
	default:
		m.Flash = "Can't remove it: " + msg.Err.Error()
	}
	return m, nil
}

// removeKey handles keys in the confirmation box.
func (m TuiModel) removeKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	r := m.removing
	if r.running {
		return m, nil // it finishes on its own; every item is checked first
	}
	switch msg.Type {
	case tea.KeyEsc:
		m.Overlay, m.removing = OverlayNone, nil
		m.Flash = "Canceled. Nothing was removed."
		return m, nil
	case tea.KeyEnter:
		if want, pending := r.nextRisky(); pending {
			if !strings.EqualFold(strings.TrimSpace(r.code), want.OpportunityID) {
				m.Flash = "Type " + want.OpportunityID + " first, then press Enter (esc cancels)."
				return m, nil
			}
			r.confirmed++
			r.code = ""
			if _, more := r.nextRisky(); more {
				m.Flash = ""
				return m, nil
			}
		}
		return m.runRemove()
	case tea.KeyBackspace:
		if s := []rune(r.code); len(s) > 0 {
			r.code = string(s[:len(s)-1])
		}
	case tea.KeyRunes:
		if r.needsCode() && len(r.code) < 8 {
			r.code += string(msg.Runes)
		}
	default:
		m.scrollBy(msg.String())
	}
	return m, nil
}

func (m TuiModel) runRemove() (tea.Model, tea.Cmd) {
	r := m.removing
	r.running = true
	run := m.Deps.Remove
	if run == nil {
		run = executorRemove
	}
	deps, plan := m.Deps, r.Res.Plan
	return m, func() tea.Msg {
		rep, err := run(deps, plan, func(ev executor.ItemEvent) { deps.post(removeProgressMsg(ev)) })
		return removedMsg{rep: rep, Err: err}
	}
}

func (m TuiModel) removeProgress(msg removeProgressMsg) (tea.Model, tea.Cmd) {
	ev := display(executor.ItemEvent(msg))
	r := m.removing
	if r == nil {
		return m, nil
	}
	if ev.Result == nil {
		r.current = ev.Item.Title
		return m, nil
	}
	r.current = ""
	r.lines = append(r.lines, m.resultLine(*ev.Result))
	return m, nil
}

func (m TuiModel) resultLine(res executor.ItemResult) string {
	t := m.t
	switch res.Outcome {
	case executor.OutcomeDone:
		if res.QuarantineID != "" && !res.Deleted {
			return t.warn.Render("◐ ") + res.Item.Title + t.muted.Render(" moved aside as "+res.QuarantineID+", not deleted")
		}
		return t.Ok.Render("✓ ") + res.Item.Title + t.muted.Render(" removed")
	case executor.OutcomeSkipped:
		return t.warn.Render("– ") + res.Item.Title + t.muted.Render(" left alone: "+res.Reason)
	}
	return t.bad.Render("✗ ") + res.Item.Title + t.muted.Render(" failed: "+res.Reason)
}

// removed closes the box, drops what was removed from the list and says
// how much space came back.
func (m TuiModel) removed(msg removedMsg) (tea.Model, tea.Cmd) {
	m.Overlay, m.removing, m.scroll = OverlayNone, nil, 0
	msg.rep = display(msg.rep) // only shown from here on; IDs are unchanged
	if msg.Err != nil {
		m.Flash = "Nothing was removed: " + msg.Err.Error()
		return m, nil
	}
	rep := msg.rep
	done := map[string]bool{}
	for _, it := range rep.Items {
		if it.Outcome == executor.OutcomeDone {
			done[it.Item.OpportunityID] = true
		}
	}
	if m.Data != nil {
		ops := m.Data.Res.Opportunities.Opportunities
		kept := ops[:0:0]
		for _, op := range ops {
			if !done[op.ID] {
				kept = append(kept, op)
			}
		}
		m.Data.Res.Opportunities.Opportunities = kept
		if p := m.Data.PlanOps; p != nil {
			raw := *p
			raw.Opportunities = nil
			for _, op := range p.Opportunities {
				if !done[op.ID] {
					raw.Opportunities = append(raw.Opportunities, op)
				}
			}
			m.Data.PlanOps = &raw
		}
		m.Cursor = max(0, min(m.Cursor, len(kept)-1))
	}
	var parts []string
	n := rep.Count(executor.OutcomeDone)
	parts = append(parts, fmt.Sprintf("Removed %d of %d", n, len(rep.Items)))
	if freed, ok := rep.Freed(); ok && freed > 0 {
		parts = append(parts, "freed "+engine.DecimalBytes(freed))
	}
	for _, it := range rep.Items {
		if it.Outcome != executor.OutcomeDone {
			parts = append(parts, it.Item.Title+": "+it.Reason)
			break
		}
	}
	parts = append(parts, rep.Warnings...)
	m.Flash = strings.Join(parts, " · ") + ". Press r to rescan."
	return m, nil
}

// removeView is the confirmation box.
func (m TuiModel) removeView() string {
	t := m.t
	r := m.removing
	if r == nil {
		return ""
	}
	var b strings.Builder
	items := r.view.Plan.Items
	b.WriteString("\n " + t.heading(fmt.Sprintf("Remove %d item%s and free the space?", len(items), plural(len(items), "", "s"))) + "\n\n")
	var total int64
	known := true
	for _, it := range items {
		sz := model.Bytes{Value: it.ExpectedBytes, Source: it.BytesSource}
		if it.BytesSource == model.SourceUnknown {
			known = false
		} else {
			total += it.ExpectedBytes
		}
		where := it.Target
		if where == "" {
			where = strings.Join(it.Argv, " ")
		}
		fmt.Fprintf(&b, "   %s %s  %s  %s\n", t.riskStyle(it.Risk).Render(fmt.Sprintf("%-7s", it.Risk)),
			t.size.Render(fmt.Sprintf("%9s", sizeCell(sz))), t.text.Render(it.Title), t.muted.Render(TildePath(m.Deps.Home, where)))
	}
	for _, rf := range r.view.Refused {
		fmt.Fprintf(&b, "   %s %s\n", t.warn.Render("not included:"), t.muted.Render(rf.ID+" "+rf.Reason))
	}
	sum := engine.DecimalBytes(total)
	switch {
	case !known && total == 0:
		sum = "unknown (the tool reports it when it's done)"
	case !known:
		sum = "at least " + sum
	}
	fmt.Fprintf(&b, "\n   %s %s\n", t.muted.Render("Total:"), t.size.Render(sum))

	switch {
	case r.running:
		b.WriteString("\n")
		for _, l := range r.lines {
			b.WriteString("   " + l + "\n")
		}
		if r.current != "" {
			b.WriteString("   " + t.muted.Render("… removing "+r.current) + "\n")
		}
	case r.needsCode():
		b.WriteString("\n   " + t.warn.Render("A RISKY item is included. It is deleted for good and can't be brought back.") + "\n")
		if want, ok := r.nextRisky(); ok {
			fmt.Fprintf(&b, "   Type %s and press Enter (%d of %d RISKY): %s%s\n", t.Id.Render(want.OpportunityID),
				r.confirmed+1, len(r.riskyItems()), t.accent.Render(r.code), t.brand.Render("▏"))
		}
		b.WriteString("   " + t.muted.Render("esc cancels") + "\n")
	default:
		b.WriteString("\n   " + t.muted.Render("Deleted for good. Each item is checked again right before it's removed.") + "\n")
		b.WriteString("   " + t.accent.Render("Enter") + t.muted.Render(" remove · ") + t.accent.Render("esc") + t.muted.Render(" cancel") + "\n")
	}
	return b.String()
}
