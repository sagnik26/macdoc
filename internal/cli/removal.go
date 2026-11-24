package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sagnik26/macdoc/internal/engine"
	"github.com/sagnik26/macdoc/internal/executor"
	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/planner"
	"github.com/sagnik26/macdoc/internal/store"
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
	// Retire and RetireID name the program when this is a Retire deletion (retire.go).
	Retire, RetireID string
}

// removeProgressMsg is an item starting or finishing.
type removeProgressMsg executor.Event

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
	finished  bool // the run is over but some items were left; the box stays so the reasons can be read
	left      []executor.ItemResult
	summary   string       // the one-line result, shown on the page when the box closes
	lines     []string     // finished items
	lineRisk  []model.Risk // the risk type of each finished item, in step with lines
	current   string       // the item being removed
	curRisk   model.Risk   // its risk type
	retire    string       // the program a Retire deletion removes; empty for every other removal
	retireID  string       // its software ID
	auto      bool         // /reclaim: started with no prompt; esc stops it between items
	riskyN    int          // RISKY suggestions /reclaim left alone, offered once it is done
	riskySize string       // their size, as shown
	cancel    context.CancelFunc
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
func executorRemove(d *TuiDeps, p model.Plan, onItem func(executor.Event)) (executor.Report, error) {
	if !RealTTY(os.Stdin, os.Stdout) {
		return executor.Report{}, errors.New("removing needs you at a Terminal")
	}
	st, err := store.Open(store.DefaultPath(d.Home))
	if err != nil {
		return executor.Report{}, err
	}
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	if d.removeCtx != nil {
		ctx = d.removeCtx
	}
	// SAFE and CAUTION items go with the one Enter on the confirmation box, a
	// group approval. A RISKY item was confirmed on its own: its ID was typed
	// before this runs (removeKey only gets here once every one is typed).
	approved := executor.Approvals{}
	for _, it := range p.Items {
		approved[it.OpportunityID] = executor.Approval{Individually: it.Risk == model.RiskRisky}
	}
	return executor.Apply(ctx, executor.Config{
		Store: st, StorePath: store.DefaultPath(d.Home), Home: d.Home, Catalog: d.catalog(), Runner: Runner, Now: time.Now,
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
	// marked.
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
		m.removing = &removeState{Res: msg.Res, view: display(msg.Res), retire: msg.Retire, retireID: msg.RetireID}
		m.Overlay, m.scroll = OverlayRemove, 0 // marks stay: esc must give them back
		if m.reclaimAuto {
			m.reclaimAuto = false
			m.removing.auto = true
			if !m.removing.needsCode() { // only SAFE and CAUTION are ever marked, so this always holds
				m.Flash = ""
				return m.runRemove()
			}
		}
	case errors.Is(msg.Err, planner.ErrNothing):
		m.Deps.retire = nil
		reason := "none of them can be removed right now"
		if len(msg.Res.Refused) > 0 {
			reason = msg.Res.Refused[0].Reason
		}
		m.Flash = "Can't remove it: " + reason
	default:
		m.Deps.retire = nil
		m.Flash = "Can't remove it: " + msg.Err.Error()
	}
	m.reclaimAuto = false
	return m, nil
}

// removeKey handles keys in the confirmation box.
func (m TuiModel) removeKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	r := m.removing
	if r.running {
		if r.auto && r.cancel != nil && msg.String() == "esc" {
			r.cancel() // the item in progress finishes; the rest are left alone
			m.Flash = "Stopping after the current item…"
		}
		return m, nil // it finishes on its own; every item is checked first
	}
	if r.finished {
		switch msg.String() {
		case "d":
			if r.riskyN == 0 {
				return m, nil
			}
			// The normal removal box: every RISKY item listed, and each one's ID
			// typed before anything is deleted. Nothing here is automatic.
			m.Overlay, m.removing, m.scroll = OverlayNone, nil, 0
			return m.markChosenAndRemove([]model.Risk{model.RiskRisky})
		case "esc", "enter", "q":
			m.Overlay, m.removing, m.scroll = m.closedOverlay(r), nil, 0
			m.Deps.retire = nil
			m.Flash = r.summary
		default:
			m.scrollBy(msg.String())
		}
		return m, nil
	}
	switch msg.String() {
	case "esc", "n":
		if msg.String() == "n" && r.retire == "" {
			break // a typed letter for the code
		}
		m.Overlay, m.removing = m.closedOverlay(r), nil
		m.Deps.retire = nil
		m.Flash = "Canceled. Nothing was removed."
		return m, nil
	case "y":
		if r.retire != "" {
			return m.runRemove()
		}
	case "enter":
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
	case "backspace":
		if s := []rune(r.code); len(s) > 0 {
			r.code = string(s[:len(s)-1])
		}
	default:
		if text := msg.Key().Text; text != "" { // a typed character: part of the code, if one is wanted
			if r.needsCode() && text != " " && len(r.code) < 8 {
				r.code += text
			}
			return m, nil
		}
		m.scrollBy(msg.String())
	}
	return m, nil
}

func (m TuiModel) runRemove() (tea.Model, tea.Cmd) {
	r := m.removing
	r.running = true
	if r.auto {
		ctx, cancel := context.WithCancel(context.Background())
		r.cancel, m.Deps.removeCtx = cancel, ctx
	}
	run := m.Deps.Remove
	if run == nil {
		run = executorRemove
	}
	deps, plan := m.Deps, r.Res.Plan
	work := func() tea.Msg {
		rep, err := run(deps, plan, func(ev executor.Event) { deps.post(removeProgressMsg(ev)) })
		return removedMsg{rep: rep, Err: err}
	}
	return m, tea.Batch(work, m.spin.Tick) // the tick keeps the spinner turning
}

func (m TuiModel) removeProgress(msg removeProgressMsg) (tea.Model, tea.Cmd) {
	ev := display(executor.Event(msg))
	r := m.removing
	if r == nil {
		return m, nil
	}
	if ev.Result == nil {
		r.current, r.curRisk = ev.Item.Title, ev.Item.Risk
		return m, nil
	}
	r.current = ""
	r.lines = append(r.lines, m.resultLine(*ev.Result))
	r.lineRisk = append(r.lineRisk, ev.Result.Item.Risk)
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
	box := m.removing
	m.Overlay, m.removing, m.scroll = m.closedOverlay(box), nil, 0
	m.Deps.retire = nil
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
			delete(m.Marked, it.Item.OpportunityID) // gone from the list; the rest stay marked
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
	if box != nil && box.retire != "" && len(rep.Items) > 0 && rep.Count(executor.OutcomeDone) == len(rep.Items) {
		m.dropRetired(box.retireID)
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
	summary := strings.Join(parts, " · ") + ". Press r to rescan."
	var left []executor.ItemResult
	for _, it := range rep.Items {
		if it.Outcome != executor.OutcomeDone {
			left = append(left, it)
		}
	}
	var risky []model.Opportunity
	if box != nil && box.auto {
		risky = m.riskyLeft()
	}
	if (len(left) > 0 || len(risky) > 0) && box != nil {
		// Some items need something from the person (an app to quit, a tool to
		// start), or /reclaim left RISKY items alone and offers them. Stay on
		// this screen and say what, instead of dropping to the page with the
		// reason squeezed into the footer.
		box.running, box.finished, box.left, box.summary, box.current = false, true, left, summary, ""
		box.riskyN, box.riskySize = len(risky), tierTotal(knownTotal(risky), risky)
		box.lines, box.lineRisk = box.lines[:0], box.lineRisk[:0]
		for _, it := range rep.Items {
			box.lines = append(box.lines, m.resultLine(it))
			box.lineRisk = append(box.lineRisk, it.Item.Risk)
		}
		m.Overlay, m.removing = OverlayRemove, box
		return m, nil
	}
	m.Flash = summary
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
	if r.auto && r.running {
		return m.autoView(r, items)
	}
	if r.retire != "" {
		return m.retireView(r, items)
	}
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
	case r.finished:
		b.WriteString("\n")
		for _, l := range r.lines {
			b.WriteString("   " + l + "\n")
		}
		if len(r.left) > 0 {
			b.WriteString("\n   " + t.warn.Render("Not removed. Do this, then run it again:") + "\n")
			for _, it := range r.left {
				b.WriteString(t.Wrap("   • "+it.Item.Title+": "+it.Reason, 5) + "\n")
			}
		}
		if r.riskyN > 0 {
			b.WriteString("\n   " + t.warn.Render(fmt.Sprintf("%d RISKY item%s left alone (%s).", r.riskyN, plural(r.riskyN, "", "s"), r.riskySize)) + "\n")
			b.WriteString(t.Wrap("   They may include app data and are deleted for good. Press d to review them: each one needs its ID typed before it is removed.", 3) + "\n")
		}
		b.WriteString("\n   " + t.muted.Render(r.summary) + "\n")
		keys := t.accent.Render("Enter") + t.muted.Render(" or ") + t.accent.Render("esc") + t.muted.Render(" close")
		if r.riskyN > 0 {
			keys = t.accent.Render("d") + t.muted.Render(" review RISKY · ") + keys
		}
		b.WriteString("   " + keys + "\n")
	case r.running:
		b.WriteString("\n" + m.categorySteps(r, items))
	case r.needsCode():
		b.WriteString("\n   " + t.warn.Render("A RISKY item is included. It is deleted for good and can't be brought back.") + "\n")
		if want, ok := r.nextRisky(); ok {
			fmt.Fprintf(&b, "   Type %s and press Enter (%d of %d RISKY): %s%s\n", t.ID.Render(want.OpportunityID),
				r.confirmed+1, len(r.riskyItems()), t.accent.Render(r.code), t.brand.Render("▏"))
		}
		b.WriteString("   " + t.muted.Render("esc cancels") + "\n")
	default:
		b.WriteString("\n   " + t.muted.Render("Deleted for good. Each item is checked again right before it's removed.") + "\n")
		b.WriteString("   " + t.accent.Render("Enter") + t.muted.Render(" remove · ") + t.accent.Render("esc") + t.muted.Render(" cancel") + "\n")
	}
	return b.String()
}

// categorySteps shows a running removal one category (risk type) at a time:
// a header with how many of its items are done, the finished items under it,
// and a spinner on the item being removed. A category not reached yet shows
// as waiting.
func (m TuiModel) categorySteps(r *removeState, items []model.PlanItem) string {
	t := m.t
	var b strings.Builder
	for _, tier := range engine.TierOrder {
		total := 0
		for _, it := range items {
			if it.Risk == tier {
				total++
			}
		}
		if total == 0 {
			continue
		}
		var lines []string
		for i, l := range r.lines {
			if i < len(r.lineRisk) && r.lineRisk[i] == tier {
				lines = append(lines, l)
			}
		}
		active := r.current != "" && r.curRisk == tier
		mark, state := t.faint.Render("○"), t.faint.Render("waiting")
		switch {
		case len(lines) >= total:
			mark, state = t.Ok.Render("✓"), t.muted.Render("done")
		case active || len(lines) > 0:
			mark, state = m.spin.View(), t.muted.Render(fmt.Sprintf("%d of %d done", len(lines), total))
		}
		fmt.Fprintf(&b, "   %s %s %s\n", mark, t.riskStyle(tier).Bold(true).Render(riskLabel(tier)), state)
		for _, l := range lines {
			b.WriteString("       " + l + "\n")
		}
		if active {
			fmt.Fprintf(&b, "       %s %s\n", m.spin.View(), t.text.Render("Removing "+r.current+"…"))
		}
	}
	return b.String()
}

// autoView is /reclaim while it runs: the categories one after another, each
// with its own count (no second progress bar after the scan's).
func (m TuiModel) autoView(r *removeState, items []model.PlanItem) string {
	t := m.t
	var b strings.Builder
	b.WriteString("\n " + t.heading(fmt.Sprintf("Reclaiming %d item%s (SAFE and CAUTION)", len(items), plural(len(items), "", "s"))) + "\n\n")
	b.WriteString(m.categorySteps(r, items))
	b.WriteString("\n")
	b.WriteString("   " + t.muted.Render("Moved aside, so they can be restored. ") + t.accent.Render("esc") + t.muted.Render(" stops after the current item.") + "\n")
	return b.String()
}

// riskyLeft is the RISKY suggestions /reclaim never touches: the ones macdoc
// could remove (not advice, not an installed app, which is for retire).
func (m TuiModel) riskyLeft() []model.Opportunity {
	var out []model.Opportunity
	for _, op := range withoutInstalledApps(m.Items()) {
		if op.Risk == model.RiskRisky && op.Method != model.MethodAdvise {
			out = append(out, op)
		}
	}
	return out
}

// closedOverlay is where the screen goes when a removal box closes: back to the
// Retire list for a Retire deletion, otherwise to the page underneath.
func (m TuiModel) closedOverlay(r *removeState) overlay {
	if r != nil && r.retire != "" {
		return overlaySoftware
	}
	return OverlayNone
}

// dropRetired takes a deleted program out of the Retire list, so it is not offered
// again before the next scan.
func (m *TuiModel) dropRetired(id string) {
	if m.software == nil {
		return
	}
	res := *m.software
	res.Software = nil
	for _, sw := range m.software.Software {
		if sw.ID != id {
			res.Software = append(res.Software, sw)
		}
	}
	m.software = &res
	m.softwareCursor = max(0, min(m.softwareCursor, len(m.shownSoftware(res))-1))
}

// retireView is the Delete / Cancel box of a Retire deletion: what goes, its size,
// and the two choices. No risk labels and no code to type.
func (m TuiModel) retireView(r *removeState, items []model.PlanItem) string {
	t := m.t
	var b strings.Builder
	switch {
	case r.running:
		fmt.Fprintf(&b, "\n %s %s\n\n", m.spin.View(), t.heading("Deleting "+r.retire+"…"))
		for _, l := range r.lines {
			b.WriteString("   " + l + "\n")
		}
		if r.current != "" {
			b.WriteString("   " + t.muted.Render(r.current) + "\n")
		}
		return b.String()
	case r.finished:
		b.WriteString("\n " + t.heading(r.retire) + "\n\n")
		for _, l := range r.lines {
			b.WriteString("   " + l + "\n")
		}
		if len(r.left) > 0 {
			b.WriteString("\n   " + t.warn.Render("Not deleted:") + "\n")
			for _, it := range r.left {
				b.WriteString(t.Wrap("   • "+it.Item.Title+": "+it.Reason, 5) + "\n")
			}
		}
		b.WriteString("\n   " + t.muted.Render(r.summary) + "\n")
		b.WriteString("   " + t.accent.Render("Enter") + t.muted.Render(" or ") + t.accent.Render("esc") + t.muted.Render(" close") + "\n")
		return b.String()
	}
	b.WriteString("\n " + t.heading("Delete "+r.retire+"?") + "\n\n")
	var total int64
	known := true
	for _, it := range items {
		sz := model.Bytes{Value: it.ExpectedBytes, Source: it.BytesSource}
		if it.BytesSource == model.SourceUnknown {
			known = false
		} else {
			total += it.ExpectedBytes
		}
		fmt.Fprintf(&b, "   %s  %s\n", t.size.Render(fmt.Sprintf("%9s", sizeCell(sz))), t.text.Render(TildePath(m.Deps.Home, it.Target)))
	}
	for _, rf := range r.view.Refused {
		fmt.Fprintf(&b, "   %s %s\n", t.warn.Render("not included:"), t.muted.Render(rf.Title+": "+rf.Reason))
	}
	sum := engine.DecimalBytes(total)
	if !known {
		sum = "at least " + sum
	}
	fmt.Fprintf(&b, "\n   %s %s\n", t.muted.Render("Total:"), t.size.Render(sum))
	b.WriteString("\n   " + t.warn.Render("Deleted for good. This can't be undone.") + "\n")
	b.WriteString("   " + t.accent.Render("Enter") + t.muted.Render(" delete · ") + t.accent.Render("esc") + t.muted.Render(" cancel") + "\n")
	return b.String()
}
