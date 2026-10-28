package cli

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"macdoc/internal/engine"
)

// The welcome screen: what macdoc is, how to start, and what it promises.
// Nothing runs until the user picks an action.

// introStatus is what the welcome screen knows without running anything:
// the latest scan, read from macdoc's own store.
type introStatus struct {
	hasScan bool
	age     string // "2h ago"
	bytes   int64
	files   int64
}

func loadIntroStatus(home string) introStatus {
	info, age, err := engine.LastScan(home, time.Now())
	if err != nil {
		return introStatus{}
	}
	return introStatus{hasScan: true, age: age, bytes: info.TotalBytes, files: info.TotalFiles}
}

type introAction struct {
	key, label, desc string
}

func (m tuiModel) introActions() []introAction {
	check := "Health, where your space went, and ranked cleanup suggestions"
	if !m.introStatus.hasScan {
		check = "First run: scans your disk once (about 1–2 min), then shows everything"
	}
	return []introAction{
		{"enter", "Check my Mac", check},
		{"s", "Fresh scan first", "Rescan the disk, then show everything (about 1–2 min)"},
		{"?", "Keys and commands", "Everything you can do in the dashboard"},
		{"q", "Quit", "Leave without doing anything"},
	}
}

// macdocLogo is "macdoc" in the ANSI Shadow figlet style, built from glyphs.
func macdocLogo() []string {
	glyphs := map[rune][]string{
		'm': {"███╗   ███╗", "████╗ ████║", "██╔████╔██║", "██║╚██╔╝██║", "██║ ╚═╝ ██║", "╚═╝     ╚═╝"},
		'a': {" █████╗ ", "██╔══██╗", "███████║", "██╔══██║", "██║  ██║", "╚═╝  ╚═╝"},
		'c': {" ██████╗", "██╔════╝", "██║     ", "██║     ", "╚██████╗", " ╚═════╝"},
		'd': {"██████╗ ", "██╔══██╗", "██║  ██║", "██║  ██║", "██████╔╝", "╚═════╝ "},
		'o': {" ██████╗ ", "██╔═══██╗", "██║   ██║", "██║   ██║", "╚██████╔╝", " ╚═════╝ "},
	}
	rows := make([]string, 6)
	for _, r := range "macdoc" {
		for i := range rows {
			rows[i] += glyphs[r][i]
		}
	}
	return rows
}

// logoShades run from the brand coral to warm yellow, top to bottom.
var logoShades = []lipgloss.TerminalColor{
	pc("#E8875F", "173", "3", "#C15F3C", "167", "1"),
	pc("#EA9460", "173", "3", "#C4663A", "167", "1"),
	pc("#EDA261", "179", "3", "#C76E37", "173", "1"),
	pc("#EFB061", "179", "11", "#B8742F", "172", "3"),
	pc("#F1BE60", "221", "11", "#A97A22", "136", "3"),
	pc("#F2CC60", "221", "11", "#9A6D00", "136", "3"),
}

func (m tuiModel) introView() string {
	t := m.t
	var blocks []string

	// Logo (or a compact title on small screens).
	logo := macdocLogo()
	if m.w >= lipgloss.Width(logo[0])+4 && m.h >= 34 {
		var lb strings.Builder
		for i, l := range logo {
			lb.WriteString(t.r.NewStyle().Foreground(logoShades[i]).Bold(true).Render(l) + "\n")
		}
		blocks = append(blocks, strings.TrimRight(lb.String(), "\n"))
	} else {
		blocks = append(blocks, t.brand.Render("◆ macdoc"))
	}
	blocks = append(blocks, t.text.Render("Why is my Mac full and slow? macdoc explains it,")+"\n"+
		t.text.Render("and helps you get space back without surprises."))

	// What macdoc already knows.
	st := m.introStatus
	status := t.warn.Render("● ") + t.muted.Render("No scan yet. Your first check scans the disk once.")
	if st.hasScan {
		status = t.ok.Render("● ") + t.muted.Render(fmt.Sprintf("Last scan %s · %s in %s files", st.age,
			engine.DecimalBytes(st.bytes), groupDigits(st.files)))
	}
	blocks = append(blocks, status)

	// Actions. Narrow screens show only the selected action's description,
	// under the box.
	compact := m.w < 104
	var ab strings.Builder
	for i, a := range m.introActions() {
		k := t.accent.Render(fmt.Sprintf("%-6s", a.key))
		desc := ""
		if !compact {
			desc = "  " + a.desc
		}
		if i == m.introPick {
			fmt.Fprintf(&ab, "%s %s %s%s\n", t.brand.Render("▶"), k, t.title.Render(fmt.Sprintf("%-18s", a.label)), t.text.Render(desc))
		} else {
			fmt.Fprintf(&ab, "  %s %s%s\n", k, t.text.Render(fmt.Sprintf("%-18s", a.label)), t.muted.Render(desc))
		}
	}
	actions := strings.TrimRight(t.card(strings.Split(strings.TrimRight(ab.String(), "\n"), "\n")...), "\n")
	if compact {
		actions += "\n " + t.muted.Render(truncate(m.introActions()[m.introPick].desc, m.w-4))
	}
	blocks = append(blocks, actions)

	// How it works.
	steps := []struct{ n, title, desc string }{
		{"1", "Check", "health, where your space went, what's safe to reclaim"},
		{"2", "Explore", "Overview · Suggestions · Toolchains · Projects · Space · Health"},
		{"3", "Understand", "enter on a suggestion: what it is, its risk, how to undo"},
		{"4", "Clean up", "soon: dry-run plans you confirm yourself in Terminal"},
	}
	var sb strings.Builder
	sb.WriteString(t.heading("How it works") + "\n")
	for _, s := range steps {
		fmt.Fprintf(&sb, " %s %s %s\n", t.r.NewStyle().Background(colBrand).Foreground(colOnBadge).Bold(true).Render(" "+s.n+" "),
			t.title.Render(fmt.Sprintf("%-11s", s.title)), t.muted.Render(truncate(s.desc, max(10, m.w-20))))
	}
	blocks = append(blocks, strings.TrimRight(sb.String(), "\n"))

	// Promises.
	promises := []string{"Read-only unless you confirm", "Nothing leaves your Mac", "Every cleanup can be explained first"}
	var pp []string
	for _, p := range promises {
		pp = append(pp, t.ok.Render("✓ ")+t.text.Render(p))
	}
	sep := t.faint.Render("   ")
	if compact {
		sep = "\n"
	}
	blocks = append(blocks, strings.Join(pp, sep))

	// From the shell.
	shell := t.heading("From the shell") + "\n" +
		" " + t.command([]string{"macdoc", "doctor"}) + t.muted.Render("              printed summary") + "\n" +
		" " + t.command([]string{"macdoc", "doctor", "scan"}) + t.muted.Render("         rescan + full report") + "\n" +
		" " + t.command([]string{"macdoc", "doctor", "explain", "<id>"}) + t.muted.Render(" one suggestion in detail") + "\n" +
		" " + t.command([]string{"macdoc", "--json"}) + t.muted.Render("              machine-readable output")
	if m.h >= 40 {
		blocks = append(blocks, shell)
	}

	// Center the column of blocks horizontally.
	body := strings.Join(blocks, "\n\n")
	width := 0
	for _, l := range strings.Split(body, "\n") {
		width = max(width, lipgloss.Width(l))
	}
	pad := strings.Repeat(" ", max(1, (m.w-width)/2))
	top := max(0, (m.introHeight()-strings.Count(body, "\n")-1)/2)
	return strings.Repeat("\n", top) + indentLines(body, pad)
}

// introHeight is the space above the footer on the welcome screen.
func (m tuiModel) introHeight() int {
	h := m.h - 1
	if m.inputMode != 0 {
		start, end := m.paletteWindow()
		h -= 2 + end - start
	} else if m.flash != "" {
		h--
	}
	return max(3, h)
}

func (m tuiModel) updateIntroKey(k string) (tea.Model, tea.Cmd) {
	n := len(m.introActions())
	switch k {
	case "up", "k", "shift+tab":
		m.introPick = (m.introPick + n - 1) % n
	case "down", "j", "tab":
		m.introPick = (m.introPick + 1) % n
	case "enter", " ":
		return m.introChoose(m.introActions()[m.introPick].key)
	case "s", "?":
		return m.introChoose(k)
	case "/":
		m.inputMode, m.pick = '/', 0
		m.input.SetValue("")
		m.input.Focus()
	}
	return m, nil
}

func (m tuiModel) introChoose(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "enter":
		return m, m.leaveIntro(false)
	case "s":
		return m, m.leaveIntro(true)
	case "?":
		m.overlay, m.scroll = overlayHelp, 0
	case "q":
		return m, tea.Quit
	}
	return m, nil
}

// leaveIntro starts the dashboard: load the latest scan (scanning only if
// there is none), or rescan first.
func (m *tuiModel) leaveIntro(rescan bool) tea.Cmd {
	m.intro = false
	return m.startLoad(rescan)
}

func (m tuiModel) introFooter() string {
	keys := [][2]string{{"enter", "start"}, {"↑↓", "choose"}, {"s", "fresh scan"}, {"/", "commands"}, {"?", "help"}, {"q", "quit"}}
	var parts []string
	for _, k := range keys {
		parts = append(parts, m.t.accent.Render(k[0])+" "+m.t.muted.Render(k[1]))
	}
	return " " + strings.Join(parts, m.t.faint.Render(" • "))
}
