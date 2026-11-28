package cli

import (
	"fmt"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/sagnik26/macdoc/internal/model"
)

// TierPicker is the checkbox list `macdoc reclaim` shows at a real terminal:
// which risk types to clean. ↑↓ move, Enter or space ticks and unticks the
// highlighted type (any number can be ticked), a ticks everything that can be
// cleaned, and Esc cancels. Below the types is a separate Clean button: only
// Enter on that button starts the cleanup, so Enter on a type never does.
// SAFE starts ticked.
// ADVICE is listed but can never be ticked: macdoc has no action for it.
// RISKY can be ticked, but it still gets apply's full confirmation after this.
type TierPicker struct {
	t        *Theme
	rows     []TierChoice
	cursor   int
	on       map[model.Risk]bool
	hint     string
	done     bool
	canceled bool
}

// NewTierPicker returns a picker over rows with SAFE ticked when it has items.
func NewTierPicker(t *Theme, rows []TierChoice) TierPicker {
	m := TierPicker{t: t, rows: rows, on: map[model.Risk]bool{}}
	for _, r := range rows {
		if r.Risk == model.RiskSafe && len(r.Items) > 0 {
			m.on[model.RiskSafe] = true
		}
	}
	return m
}

// selectable reports whether a row can be ticked.
func (m TierPicker) selectable(i int) bool {
	r := m.rows[i]
	return r.Risk != model.RiskAdvise && len(r.Items) > 0
}

// Chosen returns the ticked risk types in SAFE, CAUTION, RISKY order.
func (m TierPicker) Chosen() []model.Risk {
	var out []model.Risk
	for _, r := range m.rows {
		if m.on[r.Risk] {
			out = append(out, r.Risk)
		}
	}
	return out
}

// Done reports whether the person submitted or canceled.
func (m TierPicker) Done() bool { return m.done }

// Canceled reports whether the person canceled.
func (m TierPicker) Canceled() bool { return m.canceled }

// Init implements tea.Model.
func (m TierPicker) Init() tea.Cmd { return nil }

// Update implements tea.Model.
func (m TierPicker) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	m.hint = ""
	switch k.String() {
	case "up", "k":
		m.cursor = max(0, m.cursor-1)
	case "down", "j":
		m.cursor = min(len(m.rows), m.cursor+1) // the last stop is the Clean button
	case "space", "x":
		if m.cursor < len(m.rows) {
			m = m.toggle(m.cursor)
		}
	case "a":
		m = m.toggleAll()
	case "enter":
		if m.cursor < len(m.rows) { // on a type, Enter ticks it; it never starts the cleanup
			m = m.toggle(m.cursor)
			return m, nil
		}
		if len(m.Chosen()) == 0 {
			m.hint = "Tick at least one risk type first (enter or space), or press esc to cancel."
			return m, nil
		}
		m.done = true
		return m, tea.Quit
	case "esc", "q", "ctrl+c":
		m.canceled, m.done = true, true
		return m, tea.Quit
	}
	return m, nil
}

// toggle ticks or unticks row i, explaining why it can't be when it can't.
func (m TierPicker) toggle(i int) TierPicker {
	r := m.rows[i]
	switch {
	case r.Risk == model.RiskAdvise:
		m.hint = "macdoc can't remove advice-only items: it has no action for them."
	case len(r.Items) == 0:
		m.hint = "There is nothing to clean in " + string(r.Risk) + "."
	default:
		m.on[r.Risk] = !m.on[r.Risk]
	}
	return m
}

// toggleAll ticks every row that can be cleaned, or unticks them all when they
// already are.
func (m TierPicker) toggleAll() TierPicker {
	all := true
	for i, r := range m.rows {
		if m.selectable(i) && !m.on[r.Risk] {
			all = false
		}
	}
	for i, r := range m.rows {
		if m.selectable(i) {
			m.on[r.Risk] = !all
		}
	}
	return m
}

// View implements tea.Model.
// View implements tea.Model: the picker, drawn in place (no alternate screen).
func (m TierPicker) View() tea.View { return tea.NewView(m.Screen()) }

// Screen is what the picker shows, as a string, with no escape sequence when
// the theme draws no color.
func (m TierPicker) Screen() string {
	s := m.screen()
	if !m.t.color {
		s = ansi.Strip(s)
	}
	return s
}

func (m TierPicker) screen() string {
	t := m.t
	if m.done {
		if m.canceled {
			return ""
		}
		return " " + t.muted.Render("Chosen: "+joinRisks(m.Chosen())) + "\n"
	}
	var b strings.Builder
	b.WriteString("\n " + t.heading("What do you want to clean?") + "\n")
	for i, r := range m.rows {
		cursor := "  "
		if i == m.cursor {
			cursor = t.accent.Render("▸ ")
		}
		box := "[ ]"
		switch {
		case r.Risk == model.RiskAdvise:
			box = "[-]"
		case m.on[r.Risk]:
			box = "[x]"
		}
		label := string(r.Risk)
		if r.Risk == model.RiskAdvise {
			label = "ADVICE" // as the dashboard calls it
		}
		count := fmt.Sprintf("%d %s", len(r.Items), plural(len(r.Items), "item", "items"))
		line := fmt.Sprintf("%s %s %s %s  %s", box, t.riskStyle(r.Risk).Render(fmt.Sprintf("%-8s", label)),
			t.text.Render(fmt.Sprintf("%-9s", count)), t.size.Render(fmt.Sprintf("%-18s", sizeSummary(r.Items))), t.muted.Render(r.Note))
		fmt.Fprintf(&b, "  %s%s\n", cursor, line)
	}
	b.WriteString("\n" + m.cleanButton() + "\n")
	b.WriteString("\n " + t.muted.Render("↑↓ move · enter/space tick · a all · enter on Clean applies · esc cancel") + "\n")
	if m.hint != "" {
		b.WriteString(" " + t.warn.Render(m.hint) + "\n")
	}
	return b.String()
}

// pickTiers runs the checkbox list on the terminal and returns the ticked risk
// types, or canceled. in and out must be the real terminal.
func pickTiers(in, out *os.File, t *Theme, rows []TierChoice) (tiers []model.Risk, canceled bool, err error) {
	final, err := tea.NewProgram(NewTierPicker(t, rows), tea.WithInput(in), tea.WithOutput(out)).Run()
	if err != nil {
		return nil, false, err
	}
	m, ok := final.(TierPicker)
	if !ok || m.canceled {
		return nil, true, nil
	}
	return m.Chosen(), false, nil
}

// cleanButton is the separate row below the types that starts the cleanup. It
// says what it would clean, and is dimmed while nothing is ticked.
func (m TierPicker) cleanButton() string {
	t := m.t
	var items []model.Opportunity
	for _, r := range m.rows {
		if m.on[r.Risk] {
			items = append(items, r.Items...)
		}
	}
	focused := m.cursor == len(m.rows)
	label, color, border := "Clean: nothing ticked", colMuted, lipgloss.RoundedBorder()
	if len(items) > 0 {
		label = fmt.Sprintf("Clean %d %s · %s", len(items), plural(len(items), "item", "items"), sizeSummary(items))
		color = colBrand
	}
	if focused && len(items) > 0 {
		border = lipgloss.ThickBorder() // a heavier frame while the cursor is on it
	}
	// A framed button in dark violet (its own color, so it isn't mistaken for a
	// row); the arrow marks the middle line when the cursor is on it.
	st := t.style().Border(border).BorderForeground(t.c(color)).Foreground(t.c(color)).Padding(0, 2)
	if focused {
		st = st.Bold(true)
	}
	lines := strings.Split(st.Render(label), "\n")
	pad := []string{"  ", "  ", "  "}
	if focused {
		pad[len(lines)/2] = t.accent.Render("▸ ")
	}
	for i, l := range lines {
		lines[i] = "  " + pad[min(i, 2)] + l
	}
	return strings.Join(lines, "\n")
}
