package cli

import (
	"fmt"
	"image/color"
	"io"
	"os"
	"strings"
	"sync"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/sagnik26/macdoc/internal/engine"
	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/textsafe"
)

// Theme styles human output. A plain theme (pipes, files, tests, NO_COLOR,
// TERM=dumb) renders every style as the bare text with no escape sequence, so
// plain output is stable and machine-friendly; decorations such as the header
// card only appear when `on` is set.
type Theme struct {
	on      bool // draw decorations (cards, badges, rules)
	color   bool // emit color; false for the plain theme and the plain layout
	dark    bool
	profile colorprofile.Profile // how many colors the terminal shows
	width   int                  // terminal width for boxes; 0 = unknown

	brand, Title, text, muted, faint, accent, ID, size lipgloss.Style
	Ok, warn, bad, info, risky                         lipgloss.Style
	cmdName, cmdFlag, cmdArg                           lipgloss.Style
	jsonKey, jsonString, jsonNumber, jsonLiteral       lipgloss.Style
}

// duo is one palette entry: a truecolor value for a dark and for a light
// background. Terminals with fewer colors get the nearest one (see Theme.c).
type duo struct{ dark, light string }

var (
	colBrand      = duo{"#E8875F", "#C15F3C"}
	colText       = duo{"#ECECEC", "#1C1C1C"}
	colMuted      = duo{"#9A9A9A", "#6C6C6C"}
	colFaint      = duo{"#4E4E4E", "#C6C6C6"}
	colCyan       = duo{"#67D4F1", "#00799E"}
	colGreen      = duo{"#6BD68A", "#1E8A4C"}
	colYellow     = duo{"#F2CC60", "#9A6D00"}
	colOrange     = duo{"#F4A261", "#C2570C"}
	colRed        = duo{"#F26B6B", "#C62828"}
	colDarkViolet = duo{"#8B5CF6", "#4C1D95"}
	colBlue       = duo{"#8FB3FF", "#2457C5"}
	colViolet     = duo{"#C3A6FF", "#6B3FC4"}
	// colSelect is the selected-row background: a quiet tint that keeps
	// every foreground color readable.
	colSelect = duo{"#2F3340", "#E3E7F2"}
	// colOnBadge is the text on a colored badge: near-black in both themes.
	colOnBadge = duo{"#141414", "#FFFFFF"}
)

// c is the color of palette entry d for this theme: none for a theme that
// draws no color, otherwise the dark or light value reduced to the colors the
// terminal can show.
func (t *Theme) c(d duo) color.Color {
	if !t.color {
		return lipgloss.NoColor{}
	}
	v := d.light
	if t.dark {
		v = d.dark
	}
	return t.profile.Convert(lipgloss.Color(v))
}

// style is a fresh style. Every style in the theme starts here, so the plain
// themes are one place.
func (t *Theme) style() lipgloss.Style { return lipgloss.NewStyle() }

// fg is a style with foreground d (none for a plain theme).
func (t *Theme) fg(d duo) lipgloss.Style {
	if !t.color {
		return lipgloss.NewStyle()
	}
	return lipgloss.NewStyle().Foreground(t.c(d))
}

// newTheme picks colors for w: on only for a terminal that supports color
// (honoring NO_COLOR and CLICOLOR_FORCE).
//
// Themes for terminals are cached per file: detecting a light or dark
// background asks the terminal once, and a full report renders many views.
func newTheme(w io.Writer) *Theme {
	f, ok := w.(*os.File)
	if !ok || !IsTerminal(w) {
		return PlainTheme()
	}
	themeMu.Lock()
	defer themeMu.Unlock()
	if t, ok := themeCache[f.Fd()]; ok {
		return t
	}
	t := PlainTheme()
	if p := colorprofile.Detect(f, os.Environ()); p > colorprofile.Ascii {
		t = ColorTheme(isDarkBackground(), p, termWidth(f))
	}
	themeCache[f.Fd()] = t
	return t
}

var (
	themeMu    sync.Mutex
	themeCache = map[uintptr]*Theme{}
)

// isDarkBackground applies MACDOC_THEME=light|dark if set; otherwise it asks
// the terminal for its background color.
func isDarkBackground() bool {
	switch strings.ToLower(os.Getenv("MACDOC_THEME")) {
	case "light":
		return false
	case "dark":
		return true
	}
	return lipgloss.HasDarkBackground(os.Stdin, os.Stdout)
}

// PlainTheme returns a theme that renders exactly the uncolored text: no color
// and no attributes (no bold, no underline), so no escape sequence is ever
// written.
func PlainTheme() *Theme {
	p := lipgloss.NewStyle()
	return &Theme{
		brand: p, Title: p, text: p, muted: p, faint: p, accent: p, ID: p, size: p,
		Ok: p, warn: p, bad: p, info: p, risky: p,
		cmdName: p, cmdFlag: p, cmdArg: p,
		jsonKey: p, jsonString: p, jsonNumber: p, jsonLiteral: p,
	}
}

// ForcedColorTheme is a truecolor theme for a dark or light background that
// does not depend on the terminal it runs in. It exists so tests and
// screenshots can render the colored look anywhere.
func ForcedColorTheme(dark bool, width int) *Theme {
	return ColorTheme(dark, colorprofile.TrueColor, width)
}

// ColorTheme builds the colored styles for a dark or light background and a
// terminal that shows profile's colors.
func ColorTheme(dark bool, profile colorprofile.Profile, width int) *Theme {
	t := &Theme{on: true, color: true, dark: dark, profile: profile, width: width}
	t.brand, t.Title = t.fg(colBrand).Bold(true), t.fg(colText).Bold(true)
	t.text, t.muted, t.faint = t.fg(colText), t.fg(colMuted), t.fg(colFaint)
	t.accent, t.ID = t.fg(colCyan), t.fg(colCyan)
	t.size = t.fg(colText).Bold(true)

	t.Ok, t.warn, t.bad, t.info, t.risky = t.fg(colGreen), t.fg(colYellow), t.fg(colRed), t.fg(colBlue), t.fg(colOrange)

	t.cmdName, t.cmdFlag, t.cmdArg = t.fg(colGreen).Bold(true), t.fg(colYellow), t.fg(colText)

	t.jsonKey, t.jsonString, t.jsonNumber, t.jsonLiteral = t.fg(colCyan), t.fg(colGreen), t.fg(colYellow), t.fg(colViolet)
	return t
}

// termWidth is the terminal's Column count, or 0 if unknown.
func termWidth(f *os.File) int {
	w, _, err := termSize(f)
	if err != nil || w <= 0 {
		return 0
	}
	return w
}

// riskColor is the palette entry of a risk tier.
func riskColor(r model.Risk) duo {
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
func (t *Theme) riskStyle(r model.Risk) lipgloss.Style {
	return t.fg(riskColor(r))
}

// Badge is a tier's short label as a colored pill (plain: the bare label).
func (t *Theme) Badge(r model.Risk) string {
	label := string(r)
	if !t.on {
		return label
	}
	if !t.color {
		return " " + label + " "
	}
	return t.style().Background(t.c(riskColor(r))).Foreground(t.c(colOnBadge)).Bold(true).Render(" " + label + " ")
}

// Dot is "● " in the tier color when color is on, else "".
func (t *Theme) Dot(r model.Risk) string {
	if !t.on {
		return ""
	}
	return t.riskStyle(r).Render("●") + " "
}

// rule draws a horizontal rule of n cells.
func (t *Theme) rule(n int) string {
	return t.faint.Render(strings.Repeat("─", max(0, n)))
}

// usageStyle colors a used/total ratio: green, then yellow from 80%, red
// from 90%.
func (t *Theme) usageStyle(used, total int64) lipgloss.Style {
	if total <= 0 {
		return t.muted
	}
	switch f := float64(used) / float64(total); {
	case f >= 0.90:
		return t.bad
	case f >= 0.80:
		return t.warn
	}
	return t.Ok
}

// Meter is a slim colored usage bar (plain: the classic block bar).
func (t *Theme) Meter(used, total int64, width int) string {
	if !t.on {
		return Bar(used, total, width)
	}
	filled := 0
	if total > 0 {
		filled = max(0, min(width, int(float64(width)*float64(used)/float64(total))))
	}
	return t.usageStyle(used, total).Render(strings.Repeat("━", filled)) + t.faint.Render(strings.Repeat("━", width-filled))
}

// sev renders a finding's severity mark.
func (t *Theme) sev(s engine.Severity) string {
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

// Command highlights a native command: tool name, flags, arguments.
func (t *Theme) Command(argv []string) string {
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

// Card draws a rounded box around lines with one space of padding, sized to
// the widest line (and at most the terminal width). Plain themes return the
// lines unboxed.
func (t *Theme) Card(lines ...string) string {
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
func (t *Theme) heading(s string) string { return t.Title.Render(s) }

// done is the closing reassurance line.
func (t *Theme) done(s string) string {
	if !t.on {
		return s
	}
	return t.Ok.Render("✓") + " " + t.muted.Render(s)
}

// themedWriter carries a fixed theme to the renderers (the TUI renders into
// a buffer but wants the terminal's colors).
type themedWriter struct {
	io.Writer
	t *Theme
}

// themeFor returns the theme for w: the carried one, or one detected from w.
func themeFor(w io.Writer) *Theme {
	if tw, ok := w.(*themedWriter); ok {
		return tw.t
	}
	return newTheme(w)
}

// pressure colors a memory-pressure level.
func (t *Theme) pressure(p string) string {
	if !t.on {
		return p
	}
	switch strings.ToLower(p) {
	case "critical":
		return t.bad.Bold(true).Render("● " + p)
	case "warn", "warning":
		return t.warn.Render("● " + p)
	}
	return t.Ok.Render("● " + p)
}

// commandHeader is the first line of a section view, e.g.
// "macdoc reclaim · scan S-… · 2 hours ago".
func (t *Theme) commandHeader(section, scanID, age string) string {
	if !t.on {
		return fmt.Sprintf("macdoc %s · scan %s · %s\n\n", section, scanID, age)
	}
	return t.Card(t.brand.Render("◆ macdoc")+" "+t.Title.Render(section)+
		t.muted.Render(" · scan ")+t.ID.Render(scanID)+t.muted.Render(" · "+age)) + "\n"
}

// section renders " Name ─────── total" to a fixed width; name is drawn in
// style s.
func (t *Theme) section(s lipgloss.Style, name, total string) string {
	const width = 72
	dashes := max(3, width-lipgloss.Width(name)-len([]rune(total))-3)
	if !t.on {
		return fmt.Sprintf(" %s %s %s\n", name, strings.Repeat("─", dashes), total)
	}
	return fmt.Sprintf(" %s %s %s\n", s.Bold(true).Render(name), t.rule(dashes), t.size.Render(total))
}

// footer is the closing line of a section view.
func (t *Theme) footer(next string) string {
	if !t.on {
		return " " + next + " · nothing has been changed\n"
	}
	return " " + t.accent.Render(next) + t.faint.Render(" · ") + t.done("nothing has been changed") + "\n"
}

// Wrap soft-wraps s, which starts at Column indent, to the theme width and
// indents the continuation lines to match. Without a known width it returns
// s unchanged.
func (t *Theme) Wrap(s string, indent int) string {
	if t.width <= indent+10 {
		return s
	}
	lines := strings.Split(wrapANSI(s, t.width-indent), "\n")
	return strings.Join(lines, "\n"+strings.Repeat(" ", indent))
}

// display returns a copy of v that is safe to print: every string in it,
// names from the disk included, with terminal escapes and invisible
// characters removed and newlines turned into spaces (textsafe.ForTerminal).
// Every human renderer starts from it. It is for showing only: never plan or
// act on the copy.
func display[T any](v T) T { return textsafe.Copy(v, textsafe.Terminal) }
