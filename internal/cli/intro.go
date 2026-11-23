package cli

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/sagnik26/macdoc/internal/engine"
)

// The welcome screen: what macdoc is, how to start, and what it promises.
// Nothing runs until the user picks an action.

// IntroStatus is what the welcome screen knows without running anything:
// the latest scan, read from macdoc's own store.
type IntroStatus struct {
	HasScan bool
	Age     string // "2h ago"
	Bytes   int64
	Files   int64
}

func loadIntroStatus(home string) IntroStatus {
	info, age, err := engine.LastScan(home, time.Now())
	if err != nil {
		return IntroStatus{}
	}
	return IntroStatus{HasScan: true, Age: age, Bytes: info.TotalBytes, Files: info.TotalFiles}
}

type introAction struct {
	cmd, desc string
}

// introActions are the commands the welcome screen offers, each with what it
// does in a few words. Enter runs the highlighted one.
func (m TuiModel) introActions() []introAction {
	scan := "Check your whole Mac: health, where space went, ranked suggestions"
	if !m.IntroStatus.HasScan {
		scan = "First run: scans your disk once (1–2 min), then shows everything"
	}
	return []introAction{
		{"scan", scan},
		{"reclaim", "See what can be cleaned and choose which risk types to clean"},
		{"retire", "Installed software, its data folders and cleanups"},
		{"explore", "Where the space is, as a folder tree you can open"},
		{"vitals", "Live CPU, memory, disk, network and battery"},
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

// logoShades are the logo's six rows, top to bottom, along the gradient.
func logoShades() []duo {
	var out []duo
	for _, h := range gradient(6) {
		out = append(out, duo{h, h})
	}
	return out
}

func (m TuiModel) introView() string {
	t := m.t
	var blocks []string

	// Logo (or a compact title on small screens).
	logo := macdocLogo()
	if m.w >= lipgloss.Width(logo[0])+4 && m.h >= 34 {
		var lb strings.Builder
		shades := logoShades()
		for i, l := range logo {
			lb.WriteString(t.fg(shades[i]).Bold(true).Render(l) + "\n")
		}
		blocks = append(blocks, strings.TrimRight(lb.String(), "\n"))
	} else {
		blocks = append(blocks, t.brand.Render("◆ macdoc"))
	}
	blocks = append(blocks, t.text.Render("Why is my Mac full and slow? macdoc explains it,")+"\n"+
		t.text.Render("and helps you get space back without surprises."))

	compact := m.w < 104

	// Promises.
	promises := []string{"Read-only unless you confirm", "Nothing leaves your Mac", "Every cleanup can be explained first"}
	var pp []string
	for _, p := range promises {
		pp = append(pp, t.Ok.Render("✓ ")+t.text.Render(p))
	}
	sep := t.faint.Render("   ")
	if compact {
		sep = "\n"
	}
	blocks = append(blocks, strings.Join(pp, sep))

	// What macdoc already knows.
	st := m.IntroStatus
	status := t.warn.Render("● ") + t.muted.Render("No scan yet. Your first check scans the disk once.")
	if st.HasScan {
		status = t.Ok.Render("● ") + t.muted.Render(fmt.Sprintf("Last scan %s · %s in %s files", st.Age,
			engine.DecimalBytes(st.Bytes), groupDigits(st.Files)))
	}
	blocks = append(blocks, status)

	// Commands, each with a short explanation; ↑↓ choose and Enter runs one.
	// Narrow screens show only the highlighted command's explanation, under the
	// box.
	var ab strings.Builder
	for i, a := range m.introActions() {
		name := fmt.Sprintf("%-9s", a.cmd)
		desc := ""
		if !compact {
			desc = "  " + a.desc
		}
		if i == m.introPick {
			fmt.Fprintf(&ab, "%s %s%s\n", t.brand.Render("▶"), t.Title.Render(name), t.text.Render(desc))
		} else {
			fmt.Fprintf(&ab, "  %s%s\n", t.accent.Render(name), t.muted.Render(desc))
		}
	}
	actions := strings.TrimRight(t.Card(strings.Split(strings.TrimRight(ab.String(), "\n"), "\n")...), "\n")
	if compact {
		actions += "\n " + t.muted.Render(Truncate(m.introActions()[m.introPick].desc, m.w-4))
	}
	blocks = append(blocks, actions)

	// Center the Column of blocks horizontally.
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
func (m TuiModel) introHeight() int {
	h := m.h - 1
	if m.InputMode != 0 {
		start, end := m.paletteWindow()
		h -= 2 + end - start
	} else if m.Flash != "" {
		h--
	}
	return max(3, h)
}

func (m TuiModel) updateIntroKey(k string) (tea.Model, tea.Cmd) {
	n := len(m.introActions())
	switch k {
	case "up", "k", "shift+tab":
		m.introPick = (m.introPick + n - 1) % n
	case "down", "j", "tab":
		m.introPick = (m.introPick + 1) % n
	case "enter", " ":
		return m.RunCommand(m.introActions()[m.introPick].cmd)
	case "?":
		m.Overlay, m.scroll = overlayHelp, 0
	case "/":
		m.InputMode, m.pick = '/', 0
		m.input.SetValue("")
		m.input.Focus()
	}
	return m, nil
}

// leaveIntro starts the dashboard: load the latest scan (scanning only if
// there is none), or rescan first.
func (m *TuiModel) leaveIntro(rescan bool) tea.Cmd {
	m.Intro = false
	return m.StartLoad(rescan)
}

// goHome returns from the scan view to the welcome screen, with its last-scan
// line brought up to date. Esc does this from any tab.
func (m TuiModel) goHome() (tea.Model, tea.Cmd) {
	m.Intro, m.Overlay, m.Detail, m.scroll, m.InputMode = true, OverlayNone, false, 0, 0
	m.IntroStatus = loadIntroStatus(m.Deps.Home)
	return m, nil
}

func (m TuiModel) introFooter() string {
	keys := [][2]string{{"enter", "run"}, {"↑↓", "choose"}, {"/", "commands"}, {"?", "help"}, {"q", "quit"}}
	var parts []string
	for _, k := range keys {
		parts = append(parts, m.t.accent.Render(k[0])+" "+m.t.muted.Render(k[1]))
	}
	return " " + strings.Join(parts, m.t.faint.Render(" • "))
}
