package cli

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"macdoc/internal/engine"
	"macdoc/internal/model"
)

// tuiFixture is dashboard data built from the shared test samples.
func tuiFixture() *tuiData {
	opps, _ := sampleWins()
	toolchains := []string{"xcode", "docker", "android", "system", "system"}
	for i := range opps.Opportunities {
		opps.Opportunities[i].Toolchain = toolchains[i]
	}
	res := engine.OverviewResult{Health: sampleResult(), Scan: opps.Scan, ScanAge: "2h ago", Opportunities: opps}
	return &tuiData{res: res, dev: devResult(opps, "")}
}

func newTestTUI(t *testing.T, w, h int) tuiModel {
	t.Helper()
	deps := &tuiDeps{load: func(context.Context, *tuiDeps, bool) (*tuiData, error) { return tuiFixture(), nil }}
	m := newTUIModel(deps, plainLayoutTheme())
	m.initCmd = m.startLoad(false)
	return step(t, m, tea.WindowSizeMsg{Width: w, Height: h})
}

func step(t *testing.T, m tuiModel, msgs ...tea.Msg) tuiModel {
	t.Helper()
	for _, msg := range msgs {
		next, _ := m.Update(msg)
		m = next.(tuiModel)
	}
	return m
}

// keys turns "tab", "enter", "esc", "down" or literal text into key messages.
func keys(ks ...string) []tea.Msg {
	named := map[string]tea.KeyType{"tab": tea.KeyTab, "enter": tea.KeyEnter, "esc": tea.KeyEsc,
		"down": tea.KeyDown, "up": tea.KeyUp, "shift+tab": tea.KeyShiftTab}
	var out []tea.Msg
	for _, k := range ks {
		if kt, ok := named[k]; ok {
			out = append(out, tea.KeyMsg{Type: kt})
			continue
		}
		for _, r := range k {
			out = append(out, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		}
	}
	return out
}

func loaded(t *testing.T, w, h int) tuiModel {
	return step(t, newTestTUI(t, w, h), loadedMsg{data: tuiFixture()})
}

func TestTUILoadingSteps(t *testing.T) {
	m := newTestTUI(t, 100, 30)
	if !m.loading {
		t.Fatal("the first load starts before the program runs")
	}
	m = step(t, m, stepMsg(engine.StepHealth), stepMsg(engine.StepScan),
		progressMsg{Files: 123456, Bytes: 9_600_000_000, CurrentDir: "/Users/me/Library"})
	out := m.View()
	mustContain(t, out, "✓ Checking disk, memory and swap", "Scanning your Mac…", "9.6 GB", "123,456 files", "/Users/me/Library",
		"background priority", "esc stop", "q quit")
}

func TestTUIOverviewAndTabs(t *testing.T) {
	m := loaded(t, 100, 40)
	out := m.View()
	mustContain(t, out, "◆ macdoc", "1 Overview", "5 Health", "read-only", "scan 2h ago", "What matters most", "Reclaimable",
		"tab switch view", "/ commands", "? help",
		// Tables: headers, text risk labels, severities spelled out.
		"│ Metric", "│ Risk", "│ Tier", "Size │ Items", "SAFE", "RISKY", "ADVICE", "Critical", "╭", "╰")
	if strings.Contains(out, "@") {
		t.Error("the @ toolchain shortcut was removed")
	}
	if strings.Contains(out, "More: macdoc doctor scan") {
		t.Error("the dashboard should not print the CLI's More line")
	}
	if n := strings.Count(out, "\n"); n != 39 {
		t.Errorf("view is %d lines; want exactly the window height", n+1)
	}

	m = step(t, m, keys("tab")...)
	if m.tab != tabSuggestions {
		t.Fatalf("tab = %d", m.tab)
	}
	mustContain(t, m.View(), "│ ▶ │ O-ab12cd", "│ ID", "│ Suggestion", "│ Restore", "SAFE 25.2 GB", "item 1 of 5", "enter explain")
	m = step(t, m, keys("down", "down")...)
	if m.cursor != 2 {
		t.Fatalf("cursor = %d", m.cursor)
	}
	mustContain(t, m.View(), "│ ▶ │ O-z4n1ty", "item 3 of 5")

	// Narrow: enter swaps the list for the detail; esc goes back.
	m = step(t, m, keys("enter")...)
	mustContain(t, m.View(), "O-z4n1ty · Android system image API 29", "sdkmanager --uninstall", "esc back")
	m = step(t, m, keys("esc")...)
	if m.detail {
		t.Error("esc should close the detail")
	}
	m = step(t, m, keys("5")...)
	mustContain(t, m.View(), "Top memory users", "│ App or tool", "│ Why it matters")
	m = step(t, m, keys("4")...)
	mustContain(t, m.View(), "Reclaimable, by category")
	m = step(t, m, keys("3")...)
	mustContain(t, m.View(), "│ Toolchain", "xcode", "docker")
}

func TestTUIWideShowsDetailBesideList(t *testing.T) {
	m := step(t, loaded(t, 140, 30), keys("2")...)
	out := m.View()
	mustContain(t, out, "│ ▶ │ O-ab12cd", "SAFE  O-ab12cd · Xcode DerivedData · MyApp", "rebuilds on next build")
}

func TestTUISlashCommands(t *testing.T) {
	m := loaded(t, 100, 30)
	m = step(t, m, keys("/", "sp")...)
	out := m.View()
	mustContain(t, out, "▸ /space", "› /sp", "enter run", "tab complete")
	m = step(t, m, keys("enter")...)
	if m.tab != tabSpace || m.inputMode != 0 {
		t.Fatalf("/sp should run /space: tab=%d mode=%q", m.tab, m.inputMode)
	}

	m = step(t, m, keys("/", "explain O-z4n1ty", "enter")...)
	if m.tab != tabSuggestions || !m.detail || m.items()[m.cursor].ID != "O-z4n1ty" {
		t.Fatalf("/explain did not open the item: tab=%d detail=%v cursor=%d", m.tab, m.detail, m.cursor)
	}
	m = step(t, m, keys("/", "explain O-nope", "enter")...)
	mustContain(t, m.View(), "O-nope is not in the latest scan")

	m = step(t, m, keys("esc", "/", "json", "enter")...)
	mustContain(t, m.View(), `"id": "O-ab12cd"`)
	m = step(t, m, keys("esc", "/", "mcp", "enter")...)
	mustContain(t, m.View(), "Phase 8", "Only you apply them")
	m = step(t, m, keys("/", "bogus", "enter")...)
	mustContain(t, m.View(), "Unknown command /bogus")

	m = step(t, m, keys("/", "quit")...)
	_, cmd := m.Update(keys("enter")[0])
	if cmd == nil || cmd() != tea.Quit() {
		t.Error("/quit should quit")
	}
}

func TestTUIStopKeepsPreviousResults(t *testing.T) {
	m := loaded(t, 100, 30)
	m = step(t, m, keys("r")...)
	if !m.loading {
		t.Fatal("r should rescan")
	}
	m = step(t, m, keys("esc")...)
	if !m.stopping {
		t.Fatal("esc should stop the scan")
	}
	partial := &tuiData{res: engine.OverviewResult{ScanAge: "just now"}}
	m = step(t, m, loadedMsg{data: partial})
	if m.data == partial || m.data.res.ScanAge != "2h ago" {
		t.Error("results of a stopped scan must not replace the previous ones")
	}
	mustContain(t, m.View(), "Scan stopped. Showing the previous results.")

	// With nothing to fall back on, say so.
	m = newTestTUI(t, 100, 30)
	m = step(t, m, keys("esc")...)
	m = step(t, m, loadedMsg{err: context.Canceled})
	mustContain(t, m.View(), "Press r (or /scan) to scan again")
}

func TestTUIQuitWhileLoadingWaitsForEngine(t *testing.T) {
	m := newTestTUI(t, 100, 30)
	next, cmd := m.Update(keys("q")[0])
	m = next.(tuiModel)
	if cmd != nil || !m.quitting {
		t.Fatal("q while loading should stop the engine first")
	}
	_, cmd = m.Update(loadedMsg{err: context.Canceled})
	if cmd == nil || cmd() != tea.Quit() {
		t.Error("quit after the engine returns")
	}
}

func TestHighlightJSON(t *testing.T) {
	src := `{
  "id": "O-1",
  "bytes": 12,
  "ok": true,
  "x": null
}`
	plain := highlightJSON(plainTheme(), src)
	for _, want := range []string{`"id": "O-1"`, `"bytes": 12`, "true", "null"} {
		if !strings.Contains(plain, want) {
			t.Errorf("plain JSON lost %q", want)
		}
	}
	r := lipgloss.NewRenderer(nil)
	r.SetColorProfile(termenv.TrueColor)
	r.SetHasDarkBackground(true)
	colored := highlightJSON(colorTheme(r, 80), src)
	if !strings.Contains(colored, "\x1b[38;2;") {
		t.Error("truecolor JSON has no colors")
	}
}

func TestThemeDegrades(t *testing.T) {
	for _, c := range []struct {
		p    termenv.Profile
		want string
	}{
		{termenv.TrueColor, "\x1b[38;2;"},
		{termenv.ANSI256, "\x1b[38;5;"},
		{termenv.ANSI, "\x1b[9"},
	} {
		r := lipgloss.NewRenderer(nil)
		r.SetColorProfile(c.p)
		r.SetHasDarkBackground(true)
		th := colorTheme(r, 80)
		if got := th.badge(model.RiskSafe) + th.ok.Render("ok"); !strings.Contains(got, c.want) {
			t.Errorf("profile %v: %q lacks %q", c.p, got, c.want)
		}
	}
	// Plain output has no escapes and no decorations.
	pt := plainTheme()
	if s := pt.card("x") + pt.badge(model.RiskSafe) + pt.dot(model.RiskSafe) + pt.command([]string{"brew", "cleanup"}); strings.Contains(s, "\x1b") || strings.Contains(s, "╭") {
		t.Errorf("plain theme decorated: %q", s)
	}
	if got := pt.meter(50, 100, 10); got != bar(50, 100, 10) {
		t.Errorf("plain meter = %q", got)
	}
}

func TestFitColumnsKeepsShortColumnsWhole(t *testing.T) {
	cols := []column{{title: "ID"}, {title: "Size", right: true}, {title: "Risk"}, {title: "Suggestion", flex: true}, {title: "Why", wrap: true}}
	rows := [][]string{{"O-ab12cd", "10.7 MB", "CAUTION", "A very long suggestion title that will not fit", "a long explanation that has to wrap onto lines"}}
	out := plainLayoutTheme().grid(cols, rows, 70, nil)
	for _, want := range []string{"O-ab12cd", "10.7 MB", "CAUTION", "…"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	for _, l := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if w := lipgloss.Width(l); w > 70 {
			t.Errorf("line is %d wide: %q", w, l)
		}
	}
	if strings.Count(out, "\n") < 6 {
		t.Errorf("the wrap column should wrap:\n%s", out)
	}
}

func TestTUINarrowSuggestionsKeepSizes(t *testing.T) {
	m := step(t, loaded(t, 80, 30), keys("2")...)
	mustContain(t, m.View(), "14.2 GB", "~11.0 GB", "RISKY", "│ Restore")
}
