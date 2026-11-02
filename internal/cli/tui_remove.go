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
	res engine.PlanResult
	err error
}

// removeProgressMsg is an item starting or finishing.
type removeProgressMsg executor.ItemEvent

// removedMsg is the end of a removal.
type removedMsg struct {
	rep executor.Report
	err error
}

// removeState is the confirmation box and, once confirmed, its progress.
type removeState struct {
	res     engine.PlanResult // what the executor acts on
	view    engine.PlanResult // the same, cleaned for the terminal (display)
	code    string            // typed so far, when a RISKY item needs it
	running bool
	lines   []string // finished items
	current string   // the item being removed
}

// needsCode reports whether any item is RISKY.
func (r *removeState) needsCode() bool {
	for _, it := range r.res.Plan.Items {
		if it.Risk == model.RiskRisky {
			return true
		}
	}
	return false
}

func (r *removeState) planCode() string { return strings.TrimPrefix(r.res.Plan.ID, "P-") }

// executorRemove is the real removal: the executor, with every check, and
// each item deleted right after it's moved.
func executorRemove(d *tuiDeps, p model.Plan, onItem func(executor.ItemEvent)) (executor.Report, error) {
	if !realTTY(os.Stdin, os.Stdout) {
		return executor.Report{}, errors.New("removing needs you at a Terminal")
	}
	st, err := store.Open(store.DefaultPath(d.home))
	if err != nil {
		return executor.Report{}, err
	}
	defer func() { _ = st.Close() }()
	approved := map[string]bool{}
	for _, it := range p.Items {
		approved[it.OpportunityID] = true
	}
	return executor.Apply(context.Background(), executor.Config{
		Store: st, StorePath: store.DefaultPath(d.home), Home: d.home, Catalog: d.cat, Runner: runner, Now: time.Now,
		DeleteNow: true,
		FreeSpace: func(ctx context.Context) (int64, error) { return engine.FreeSpace(ctx, runner) },
		OnItem:    onItem,
	}, p, approved)
}

// startRemove seals a plan for the marked suggestions (or the selected one)
// and opens the confirmation box. Advice-only items have nothing to remove,
// so Enter explains them instead.
func (m tuiModel) startRemove() (tea.Model, tea.Cmd) {
	if n, _ := m.markedTotal(); n == 0 {
		ops := m.items()
		if m.cursor >= len(ops) {
			return m, nil
		}
		if op := ops[m.cursor]; op.Method == model.MethodAdvise || op.Risk == model.RiskAdvise {
			m.detail = !m.detail
			m.flash = "macdoc can't remove this one. The steps to do it yourself are below."
			return m, nil
		}
	}
	sel := m.markedSelector()
	if sel == nil || m.planning {
		return m, nil
	}
	if m.data == nil || m.deps.makePlan == nil {
		m.flash = "Check your Mac first."
		return m, nil
	}
	m.planning = true
	m.flash = "Checking…"
	deps, pre, s := m.deps, m.data.planOps, *sel
	return m, func() tea.Msg {
		res, err := deps.makePlan(deps, s, pre)
		return removeReadyMsg{res: res, err: err}
	}
}

func (m tuiModel) removeReady(msg removeReadyMsg) (tea.Model, tea.Cmd) {
	m.planning = false
	switch {
	case msg.err == nil:
		m.removing = &removeState{res: msg.res, view: display(msg.res)}
		m.overlay, m.scroll = overlayRemove, 0
		clear(m.marked)
	case errors.Is(msg.err, planner.ErrNothing):
		reason := "none of them can be removed right now"
		if len(msg.res.Refused) > 0 {
			reason = msg.res.Refused[0].Reason
		}
		m.flash = "Can't remove it: " + reason
	default:
		m.flash = "Can't remove it: " + msg.err.Error()
	}
	return m, nil
}

// removeKey handles keys in the confirmation box.
func (m tuiModel) removeKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	r := m.removing
	if r.running {
		return m, nil // it finishes on its own; every item is checked first
	}
	switch msg.Type {
	case tea.KeyEsc:
		m.overlay, m.removing = overlayNone, nil
		m.flash = "Canceled. Nothing was removed."
		return m, nil
	case tea.KeyEnter:
		if r.needsCode() && !strings.EqualFold(strings.TrimSpace(r.code), r.planCode()) {
			m.flash = "Type " + r.planCode() + " first, then press Enter (esc cancels)."
			return m, nil
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

func (m tuiModel) runRemove() (tea.Model, tea.Cmd) {
	r := m.removing
	r.running = true
	run := m.deps.remove
	if run == nil {
		run = executorRemove
	}
	deps, plan := m.deps, r.res.Plan
	return m, func() tea.Msg {
		rep, err := run(deps, plan, func(ev executor.ItemEvent) { deps.post(removeProgressMsg(ev)) })
		return removedMsg{rep: rep, err: err}
	}
}

func (m tuiModel) removeProgress(msg removeProgressMsg) (tea.Model, tea.Cmd) {
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

func (m tuiModel) resultLine(res executor.ItemResult) string {
	t := m.t
	switch res.Outcome {
	case executor.OutcomeDone:
		if res.QuarantineID != "" && !res.Deleted {
			return t.warn.Render("◐ ") + res.Item.Title + t.muted.Render(" moved aside as "+res.QuarantineID+", not deleted")
		}
		return t.ok.Render("✓ ") + res.Item.Title + t.muted.Render(" removed")
	case executor.OutcomeSkipped:
		return t.warn.Render("– ") + res.Item.Title + t.muted.Render(" left alone: "+res.Reason)
	}
	return t.bad.Render("✗ ") + res.Item.Title + t.muted.Render(" failed: "+res.Reason)
}

// removed closes the box, drops what was removed from the list and says
// how much space came back.
func (m tuiModel) removed(msg removedMsg) (tea.Model, tea.Cmd) {
	m.overlay, m.removing, m.scroll = overlayNone, nil, 0
	msg.rep = display(msg.rep) // only shown from here on; IDs are unchanged
	if msg.err != nil {
		m.flash = "Nothing was removed: " + msg.err.Error()
		return m, nil
	}
	rep := msg.rep
	done := map[string]bool{}
	for _, it := range rep.Items {
		if it.Outcome == executor.OutcomeDone {
			done[it.Item.OpportunityID] = true
		}
	}
	if m.data != nil {
		ops := m.data.res.Opportunities.Opportunities
		kept := ops[:0:0]
		for _, op := range ops {
			if !done[op.ID] {
				kept = append(kept, op)
			}
		}
		m.data.res.Opportunities.Opportunities = kept
		if p := m.data.planOps; p != nil {
			raw := *p
			raw.Opportunities = nil
			for _, op := range p.Opportunities {
				if !done[op.ID] {
					raw.Opportunities = append(raw.Opportunities, op)
				}
			}
			m.data.planOps = &raw
		}
		m.cursor = max(0, min(m.cursor, len(kept)-1))
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
	m.flash = strings.Join(parts, " · ") + ". Press r to rescan."
	return m, nil
}

// removeView is the confirmation box.
func (m tuiModel) removeView() string {
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
			t.size.Render(fmt.Sprintf("%9s", sizeCell(sz))), t.text.Render(it.Title), t.muted.Render(tildePath(m.deps.home, where)))
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
		fmt.Fprintf(&b, "   Type %s and press Enter: %s%s\n", t.id.Render(r.planCode()), t.accent.Render(r.code), t.brand.Render("▏"))
		b.WriteString("   " + t.muted.Render("esc cancels") + "\n")
	default:
		b.WriteString("\n   " + t.muted.Render("Deleted for good. Each item is checked again right before it's removed.") + "\n")
		b.WriteString("   " + t.accent.Render("Enter") + t.muted.Render(" remove · ") + t.accent.Render("esc") + t.muted.Render(" cancel") + "\n")
	}
	return b.String()
}
