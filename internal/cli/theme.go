package cli

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"macdoc/internal/engine"
	"macdoc/internal/model"
)

// theme styles human output. A plain theme (pipes, files, tests, NO_COLOR,
// TERM=dumb) renders every style as the bare text, so plain output is stable
// and machine-friendly; decorations such as the header card only appear when
// color is on.
type theme struct {
	on    bool
	dark  bool
	r     *lipgloss.Renderer
	width int // terminal width for boxes; 0 = unknown

	brand, title, text, muted, faint, accent, id, size lipgloss.Style
	ok, warn, bad, info, risky                         lipgloss.Style
	cmdName, cmdFlag, cmdArg                           lipgloss.Style
	jsonKey, jsonString, jsonNumber, jsonLiteral       lipgloss.Style
}

// palette entries degrade from truecolor to 256 to 16 colors, with separate
// values for light and dark terminals.
func pc(dark, dark256, dark16, light, light256, light16 string) lipgloss.TerminalColor {
	return lipgloss.CompleteAdaptiveColor{
		Dark:  lipgloss.CompleteColor{TrueColor: dark, ANSI256: dark256, ANSI: dark16},
		Light: lipgloss.CompleteColor{TrueColor: light, ANSI256: light256, ANSI: light16},
	}
}

var (
	colBrand  = pc("#E8875F", "173", "3", "#C15F3C", "167", "1")
	colText   = pc("#ECECEC", "255", "15", "#1C1C1C", "234", "0")
	colMuted  = pc("#9A9A9A", "247", "7", "#6C6C6C", "242", "8")
	colFaint  = pc("#4E4E4E", "239", "8", "#C6C6C6", "251", "7")
	colCyan   = pc("#67D4F1", "81", "14", "#00799E", "31", "6")
	colGreen  = pc("#6BD68A", "78", "10", "#1E8A4C", "29", "2")
	colYellow = pc("#F2CC60", "221", "11", "#9A6D00", "136", "3")
	colOrange = pc("#F4A261", "215", "9", "#C2570C", "166", "1")
	colRed    = pc("#F26B6B", "203", "9", "#C62828", "160", "1")
	colBlue   = pc("#8FB3FF", "111", "12", "#2457C5", "26", "4")
	colViolet = pc("#C3A6FF", "183", "13", "#6B3FC4", "98", "5")
	// colSelect is the selected-row background: a quiet tint that keeps
	// every foreground color readable.
	colSelect = pc("#2F3340", "236", "8", "#E3E7F2", "254", "7")
	// colOnBadge is the text on a colored badge: near-black in both themes.
	colOnBadge = pc("#141414", "233", "0", "#FFFFFF", "231", "15")
)

// newTheme picks colors for w: on only for a terminal that supports color
// (honoring NO_COLOR and CLICOLOR_FORCE).
//
// Themes for terminals are cached per file: detecting a light or dark
// background asks the terminal once, and a full report renders many views.
func newTheme(w io.Writer) *theme {
	f, ok := w.(*os.File)
	if !ok || !isTerminal(w) {
		return plainTheme()
	}
	themeMu.Lock()
	defer themeMu.Unlock()
	if t, ok := themeCache[f.Fd()]; ok {
		return t
	}
	t := plainTheme()
	if r := lipgloss.NewRenderer(f); r.ColorProfile() != termenv.Ascii {
		setBackground(r)
		t = colorTheme(r, termWidth(f))
	}
	themeCache[f.Fd()] = t
	return t
}

var (
	themeMu    sync.Mutex
	themeCache = map[uintptr]*theme{}
)

// setBackground applies MACDOC_THEME=light|dark if set; otherwise lipgloss
// asks the terminal for its background color.
func setBackground(r *lipgloss.Renderer) {
	switch strings.ToLower(os.Getenv("MACDOC_THEME")) {
	case "light":
		r.SetHasDarkBackground(false)
	case "dark":
		r.SetHasDarkBackground(true)
	}
}

func plainTheme() *theme {
	r := lipgloss.NewRenderer(io.Discard)
	r.SetColorProfile(termenv.Ascii)
	t := colorTheme(r, 0)
	t.on = false
	return t
}

// colorTheme builds styles on renderer r (whose profile decides the depth).
func colorTheme(r *lipgloss.Renderer, width int) *theme {
	s := func(c lipgloss.TerminalColor) lipgloss.Style { return r.NewStyle().Foreground(c) }
	return &theme{
		on:    true,
		dark:  r.HasDarkBackground(),
		r:     r,
		width: width,

		brand:  s(colBrand).Bold(true),
		title:  s(colText).Bold(true),
		text:   s(colText),
		muted:  s(colMuted),
		faint:  s(colFaint),
		accent: s(colCyan),
		id:     s(colCyan),
		size:   s(colText).Bold(true),

		ok:    s(colGreen),
		warn:  s(colYellow),
		bad:   s(colRed),
		info:  s(colBlue),
		risky: s(colOrange),

		cmdName: s(colGreen).Bold(true),
		cmdFlag: s(colYellow),
		cmdArg:  s(colText),

		jsonKey:     s(colCyan),
		jsonString:  s(colGreen),
		jsonNumber:  s(colYellow),
		jsonLiteral: s(colViolet),
	}
}

// termWidth is the terminal's column count, or 0 if unknown.
func termWidth(f *os.File) int {
	w, _, err := termSize(f)
	if err != nil || w <= 0 {
		return 0
	}
	return w
}

// riskColor is the color of a risk tier.
func riskColor(r model.Risk) lipgloss.TerminalColor {
	switch r {
	case model.RiskSafe:
		return colGreen
	case model.RiskCaution:
		return colYellow
	case model.RiskRisky:
		return colOrange
	case model.RiskBlocked:
		return colRed
	default:
		return colBlue
	}
}

// riskStyle is the text style of a risk tier.
func (t *theme) riskStyle(r model.Risk) lipgloss.Style {
	return t.r.NewStyle().Foreground(riskColor(r))
}

// badge is a tier's short label as a colored pill (plain: the bare label).
func (t *theme) badge(r model.Risk) string {
	label := string(r)
	if !t.on {
		return label
	}
	return t.r.NewStyle().Background(riskColor(r)).Foreground(colOnBadge).Bold(true).Render(" " + label + " ")
}

// dot is "● " in the tier color when color is on, else "".
func (t *theme) dot(r model.Risk) string {
	if !t.on {
		return ""
	}
	return t.riskStyle(r).Render("●") + " "
}

// rule draws a horizontal rule of n cells.
func (t *theme) rule(n int) string {
	return t.faint.Render(strings.Repeat("─", max(0, n)))
}

// usageStyle colors a used/total ratio: green, then yellow from 80%, red
// from 90%.
func (t *theme) usageStyle(used, total int64) lipgloss.Style {
	if total <= 0 {
		return t.muted
	}
	switch f := float64(used) / float64(total); {
	case f >= 0.90:
		return t.bad
	case f >= 0.80:
		return t.warn
	}
	return t.ok
}

// meter is a slim colored usage bar (plain: the classic block bar).
func (t *theme) meter(used, total int64, width int) string {
	if !t.on {
		return bar(used, total, width)
	}
	filled := 0
	if total > 0 {
		filled = max(0, min(width, int(float64(width)*float64(used)/float64(total))))
	}
	return t.usageStyle(used, total).Render(strings.Repeat("━", filled)) + t.faint.Render(strings.Repeat("━", width-filled))
}

// sev renders a finding's severity mark.
func (t *theme) sev(s engine.Severity) string {
	m := severityMark(s)
	if !t.on {
		return m
	}
	switch s {
	case engine.SeverityCritical:
		return t.bad.Bold(true).Render("▲")
	case engine.SeverityWarn:
		return t.warn.Render("▲")
	}
	return t.muted.Render("•")
}

// command highlights a native command: tool name, flags, arguments.
func (t *theme) command(argv []string) string {
	if !t.on {
		return strings.Join(argv, " ")
	}
	parts := make([]string, len(argv))
	for i, a := range argv {
		switch {
		case i == 0:
			parts[i] = t.cmdName.Render(a)
		case strings.HasPrefix(a, "-"):
			parts[i] = t.cmdFlag.Render(a)
		default:
			parts[i] = t.cmdArg.Render(a)
		}
	}
	return strings.Join(parts, " ")
}

// card draws a rounded box around lines with one space of padding, sized to
// the widest line (and at most the terminal width). Plain themes return the
// lines unboxed.
func (t *theme) card(lines ...string) string {
	if !t.on {
		return strings.Join(lines, "\n") + "\n"
	}
	inner := 0
	for _, l := range lines {
		inner = max(inner, lipgloss.Width(l))
	}
	if t.width > 4 {
		inner = min(inner, t.width-4)
	}
	var b strings.Builder
	b.WriteString(t.faint.Render("╭"+strings.Repeat("─", inner+2)+"╮") + "\n")
	for _, l := range lines {
		l = truncateANSI(l, inner)
		b.WriteString(t.faint.Render("│") + " " + l + strings.Repeat(" ", inner-lipgloss.Width(l)) + " " + t.faint.Render("│") + "\n")
	}
	b.WriteString(t.faint.Render("╰"+strings.Repeat("─", inner+2)+"╯") + "\n")
	return b.String()
}

// heading renders a bold section heading.
func (t *theme) heading(s string) string { return t.title.Render(s) }

// done is the closing reassurance line.
func (t *theme) done(s string) string {
	if !t.on {
		return s
	}
	return t.ok.Render("✓") + " " + t.muted.Render(s)
}

// themedWriter carries a fixed theme to the renderers (the TUI renders into
// a buffer but wants the terminal's colors).
type themedWriter struct {
	io.Writer
	t *theme
}

// themeFor returns the theme for w: the carried one, or one detected from w.
func themeFor(w io.Writer) *theme {
	if tw, ok := w.(*themedWriter); ok {
		return tw.t
	}
	return newTheme(w)
}

// pressure colors a memory-pressure level.
func (t *theme) pressure(p string) string {
	if !t.on {
		return p
	}
	switch strings.ToLower(p) {
	case "critical":
		return t.bad.Bold(true).Render("● " + p)
	case "warn", "warning":
		return t.warn.Render("● " + p)
	}
	return t.ok.Render("● " + p)
}

// commandHeader is the first line of a section view, e.g.
// "macdoc doctor wins · scan S-… · 2 hours ago".
func (t *theme) commandHeader(section, scanID, age string) string {
	if !t.on {
		return fmt.Sprintf("macdoc doctor %s · scan %s · %s\n\n", section, scanID, age)
	}
	return t.card(t.brand.Render("◆ macdoc")+" "+t.title.Render("doctor "+section)+
		t.muted.Render(" · scan ")+t.id.Render(scanID)+t.muted.Render(" · "+age)) + "\n"
}

// section renders " Name ─────── total" to a fixed width; name is drawn in
// style s.
func (t *theme) section(s lipgloss.Style, name, total string) string {
	const width = 72
	dashes := max(3, width-lipgloss.Width(name)-len([]rune(total))-3)
	if !t.on {
		return fmt.Sprintf(" %s %s %s\n", name, strings.Repeat("─", dashes), total)
	}
	return fmt.Sprintf(" %s %s %s\n", s.Bold(true).Render(name), t.rule(dashes), t.size.Render(total))
}

// footer is the closing line of a section view.
func (t *theme) footer(next string) string {
	if !t.on {
		return " " + next + " · nothing has been changed\n"
	}
	return " " + t.accent.Render(next) + t.faint.Render(" · ") + t.done("nothing has been changed") + "\n"
}

// wrap soft-wraps s, which starts at column indent, to the theme width and
// indents the continuation lines to match. Without a known width it returns
// s unchanged.
func (t *theme) wrap(s string, indent int) string {
	if t.width <= indent+10 {
		return s
	}
	lines := strings.Split(wrapANSI(s, t.width-indent), "\n")
	return strings.Join(lines, "\n"+strings.Repeat(" ", indent))
}
