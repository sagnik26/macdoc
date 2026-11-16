package cli

import (
	"fmt"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/sagnik26/macdoc/internal/engine"
	"github.com/sagnik26/macdoc/internal/store"
)

// The dashboard has no page of plans: a person cleans by pressing Enter on a
// suggestion, and the plan that makes behind the scenes is not something to
// browse. The one plan a person did not make themselves is one Claude
// proposed over MCP. While one is waiting, the Suggestions tab says so in one
// line, and `a` hands it to `macdoc apply <plan>` in Terminal, where it is
// shown again and confirmed (ADR 0001). Nothing here removes anything.

// plansShown is how many recent plans are read to look for Claude's.
const plansShown = 20

// plans reads the stored plans, newest first. Tests replace it through
// TuiDeps.Plans.
func (d *TuiDeps) plans() ([]store.PlanRecord, error) {
	if d.Plans != nil {
		return d.Plans(d)
	}
	return engine.ListPlans(engine.OpportunityOptions{Home: d.Home}, plansShown)
}

// planReady reports whether a plan can still be applied.
func planReady(r store.PlanRecord, now time.Time) bool {
	return r.Status == store.PlanPending && now.Before(r.ExpiresAt)
}

// refreshClaudePlans looks for plans Claude proposed that can still be
// applied, newest first. A store that can't be read means none are shown.
func (m *TuiModel) refreshClaudePlans() {
	m.claude = nil
	recs, err := m.Deps.plans()
	if err != nil {
		return
	}
	now := NowFunc()
	for _, r := range recs {
		if r.Origin == "claude" && planReady(r, now) {
			m.claude = append(m.claude, r)
		}
	}
}

// claudeLine is the one line on the Suggestions tab about waiting plans, or
// "" when there are none.
func (m TuiModel) claudeLine() string {
	if len(m.claude) == 0 {
		return ""
	}
	t := m.t
	r := m.claude[0]
	what := fmt.Sprintf("Claude proposed a plan: %d %s", r.Items, plural(r.Items, "item", "items"))
	if r.TotalBytes > 0 {
		what += ", " + engine.DecimalBytes(r.TotalBytes)
	}
	if n := len(m.claude); n > 1 {
		what = fmt.Sprintf("Claude proposed %d plans (newest: %d %s)", n, r.Items, plural(r.Items, "item", "items"))
	}
	return " " + t.warn.Render("●") + " " + t.text.Render(what) + t.muted.Render(" · ") + t.accent.Render("a") + " " + t.muted.Render("apply it in Terminal")
}

// banner is a boxed message with a colored border: a title and some text.
func (t *Theme) banner(c lipgloss.TerminalColor, mark, title, text string) string {
	if !t.on {
		return " " + mark + " " + title + "\n   " + text + "\n"
	}
	w := 72
	if t.width > 0 {
		w = max(30, min(w, t.width-6))
	}
	body := lipgloss.NewStyle().Foreground(c).Bold(true).Render(mark+" "+title) + "\n" +
		t.text.Width(w).Render(text)
	box := t.r.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(c).Padding(0, 1).Render(body)
	return indentLines(box, " ") + "\n"
}
