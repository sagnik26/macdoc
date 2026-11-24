package cli

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/planner"
)

// The /clean list (ADR 0018): the scan only shows, and this is where one item at a
// time is deleted. It lists what the latest scan found that macdoc can act on, with
// nothing ticked at the start; space ticks a row, a ticks all, and Enter opens the
// same Delete / Cancel box as Retire for exactly the ticked rows. A RISKY row shows its
// label, and ticking it and pressing Delete is its own approval, so no code is typed.

// chooseItems are the suggestions /clean offers: everything the scan found that can be
// acted on. Advice-only and blocked items, and installed apps (those are for Retire),
// are left out, as /reclaim leaves them out.
func (m TuiModel) chooseItems() []model.Opportunity {
	var out []model.Opportunity
	for _, op := range m.Items() {
		if op.Method == model.MethodAdvise || op.Risk == model.RiskAdvise || op.Risk == model.RiskBlocked || installedAppRecipes[op.RecipeID] {
			continue
		}
		out = append(out, op)
	}
	return out
}

// openChoose opens the /clean list with nothing ticked.
func (m TuiModel) openChoose() (tea.Model, tea.Cmd) {
	if m.Data == nil {
		m.Flash = "No scan yet. Run /scan first."
		return m, nil
	}
	if len(m.chooseItems()) == 0 {
		m.Flash = "Nothing to choose from: the last scan found nothing macdoc can delete."
		return m, nil
	}
	if m.Marked == nil {
		m.Marked = map[string]bool{}
	}
	clear(m.Marked)
	m.Overlay, m.scroll, m.chooseCursor = overlayChoose, 0, 0
	m.Flash = ""
	return m, nil
}

// chooseKey handles the keys of the /clean list. ok is false for keys it leaves to the
// overlay's usual handling (esc and q close it).
func (m TuiModel) chooseKey(k string) (tea.Model, tea.Cmd, bool) {
	ops := m.chooseItems()
	n := len(ops)
	switch k {
	case "space":
		if m.chooseCursor < n {
			m.toggleTick(ops[m.chooseCursor].ID)
			m.chooseCursor++
		}
	case "a":
		m.tickAll(ops)
	case "enter":
		next, cmd := m.startChoose()
		return next, cmd, true
	case "esc", "q":
		return m, nil, false
	default:
		m.chooseCursor = chooseMove(m.chooseCursor, k, n)
	}
	m.chooseCursor = max(0, min(m.chooseCursor, n-1))
	return m, nil, true
}

// chooseMove is where the highlighted row goes for a movement key, before it is kept in range.
func chooseMove(cur int, k string, n int) int {
	switch k {
	case "up", "k":
		return cur - 1
	case "down", "j":
		return cur + 1
	case "pgup":
		return cur - 10
	case "pgdown":
		return cur + 10
	case "home", "g":
		return 0
	case "end", "G":
		return n - 1
	}
	return cur
}

// toggleTick ticks a row, or unticks it if it was ticked.
func (m TuiModel) toggleTick(id string) {
	if m.Marked[id] {
		delete(m.Marked, id)
	} else {
		m.Marked[id] = true
	}
}

// tickAll ticks every row, or unticks them all if every one was ticked.
func (m TuiModel) tickAll(ops []model.Opportunity) {
	all := true
	for _, op := range ops {
		all = all && m.Marked[op.ID]
	}
	for _, op := range ops {
		if all {
			delete(m.Marked, op.ID)
		} else {
			m.Marked[op.ID] = true
		}
	}
}

// startChoose seals a plan for the ticked rows, and only those, and opens the Delete /
// Cancel box. With nothing ticked it says so and plans nothing: Enter never deletes the
// highlighted row by itself.
func (m TuiModel) startChoose() (tea.Model, tea.Cmd) {
	if m.Planning {
		return m, nil
	}
	var ids []string
	for _, op := range m.chooseItems() {
		if m.Marked[op.ID] {
			ids = append(ids, op.ID)
		}
	}
	if len(ids) == 0 {
		m.Flash = "Tick something first (space), then press Enter."
		return m, nil
	}
	return m.sealRemoval(planner.Selector{IDs: ids}, true)
}

// chooseView is the /clean list: a table of the items with a Column of ticks, and what
// is ticked.
func (m TuiModel) chooseView() string {
	t := m.t
	ops := m.chooseItems()
	if len(ops) == 0 {
		return "\n " + t.muted.Render("Nothing left to choose. Press esc to go back.") + "\n"
	}
	m.chooseCursor = max(0, min(m.chooseCursor, len(ops)-1))
	rowsFit := max(1, m.bodyHeight()-8) // heading, blank, the table's four lines, the note, a blank
	start := max(0, min(m.chooseCursor-rowsFit/2, len(ops)-rowsFit))
	end := min(len(ops), start+rowsFit)
	var b strings.Builder
	b.WriteString("\n " + t.heading("Choose what to delete") + t.muted.Render(fmt.Sprintf("  · item %d of %d", m.chooseCursor+1, len(ops))) + "\n\n")
	b.WriteString(indentLines(m.opsTable(ops[start:end], m.chooseCursor-start, m.w-2, m.w < 100, true), " "))
	note := m.markedNote()
	if note == "" {
		note = t.muted.Render("nothing ticked yet")
	}
	b.WriteString("\n " + note + t.muted.Render("  · space ticks · a ticks all · enter deletes the ticked ones, after you confirm") + "\n")
	return b.String()
}
