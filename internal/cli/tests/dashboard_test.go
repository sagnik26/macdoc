package tests

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/sagnik26/macdoc/internal/cli"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/sagnik26/macdoc/internal/engine"
	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/projects"
)

// tuiFixture is dashboard data built from the shared test samples.
func tuiFixture() *cli.TuiData {
	opps, _ := sampleWins()
	toolchains := []string{"xcode", "docker", "android", "system", "system"}
	for i := range opps.Opportunities {
		opps.Opportunities[i].Toolchain = toolchains[i]
	}
	res := engine.OverviewResult{Health: sampleResult(), Scan: opps.Scan, ScanAge: "2h ago", Opportunities: opps}
	slow := engine.SlowResult{Health: res.Health, Level: engine.SeverityWarn, Verdict: "Memory is tight.", Summary: "Cursor 1.0 GB on 8.0 GB of RAM (12% together).",
		Swap: engine.SwapHistory{Days: 7, Samples: 1}}
	for _, g := range res.Health.Report.TopProcesses {
		slow.Groups = append(slow.Groups, engine.SlowGroup{ProcessGroup: g, Explain: "Explained " + g.Name + "."})
	}
	gradle := engine.Range{Low: 6 * gb, High: 6 * gb}
	trend := &engine.TrendResult{KeptFloor: 100 << 20, To: model.ScanInfo{StartedAt: time.Unix(0, 0).Add(30 * 24 * time.Hour)}, From: model.ScanInfo{StartedAt: time.Unix(0, 0)},
		Areas: []engine.TrendArea{{Name: "Gradle", Before: engine.Range{Low: 4 * gb, High: 4 * gb}, After: engine.Range{Low: 10 * gb, High: 10 * gb}, Change: gradle},
			{Name: "Downloads", Before: engine.Range{Low: gb, High: gb}, After: engine.Range{Low: gb, High: gb}}}}
	return &cli.TuiData{Res: res, Dev: cli.BuildDevResult(opps, ""), Slow: slow, Trend: trend, Projects: engine.ProjectsResult{
		Projects: []projects.Project{
			{Root: "/Users/me/code/app", IdleDays: 120, ActivitySource: projects.ActivityCommit, Git: projects.GitDirty, ArtifactBytes: 800_000_000, ReclaimableBytes: 800_000_000,
				Artifacts: []projects.Artifact{{Rel: "node_modules", Bytes: 500_000_000}, {Rel: "ios/Pods", Bytes: 300_000_000}}},
			{Root: "/Users/me/Desktop/site", IdleDays: -1, Git: projects.GitClean, Protected: true, ArtifactBytes: 2_000_000, ReclaimableBytes: 2_000_000,
				Artifacts: []projects.Artifact{{Rel: ".next", Bytes: 2_000_000}}},
		},
		DerivedData: []projects.DerivedData{{Workspace: "/Users/me/old/Gone.xcodeproj", Bytes: 900_000_000, Missing: true}},
	}}
}

func newTestTUI(t *testing.T, w, h int) cli.TuiModel {
	t.Helper()
	deps := &cli.TuiDeps{Home: "/Users/me", Load: func(context.Context, *cli.TuiDeps, bool) (*cli.TuiData, error) { return tuiFixture(), nil }}
	m := cli.NewTUIModel(deps, cli.PlainLayoutTheme())
	m.InitCmd = m.StartLoad(false)
	return step(t, m, tea.WindowSizeMsg{Width: w, Height: h})
}

func step(t *testing.T, m cli.TuiModel, msgs ...tea.Msg) cli.TuiModel {
	t.Helper()
	for _, msg := range msgs {
		next, _ := m.Update(msg)
		m = next.(cli.TuiModel)
	}
	return m
}

// keys turns "tab", "enter", "esc", "down" or literal text into key messages.
func keys(ks ...string) []tea.Msg {
	named := map[string]tea.KeyType{"tab": tea.KeyTab, "enter": tea.KeyEnter, "esc": tea.KeyEsc,
		"down": tea.KeyDown, "up": tea.KeyUp, "shift+tab": tea.KeyShiftTab, "pgdown": tea.KeyPgDown}
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

func loaded(t *testing.T, w, h int) cli.TuiModel {
	return step(t, newTestTUI(t, w, h), cli.LoadedMsg{Data: tuiFixture()})
}

func TestTUILoadingSteps(t *testing.T) {
	m := newTestTUI(t, 100, 30)
	if !m.Loading {
		t.Fatal("the first load starts before the program runs")
	}
	m = step(t, m, cli.StepMsg(engine.StepHealth), cli.StepMsg(engine.StepScan),
		cli.ProgressMsg{Files: 123456, Bytes: 9_600_000_000, CurrentDir: "/Users/me/Library"})
	out := m.View()
	mustContain(t, out, "✓ Checking disk, memory and swap", "Scanning your Mac…", "9.6 GB", "123,456 files", "/Users/me/Library",
		"background priority", "esc stop", "q quit")
}

func TestTUIOverviewAndTabs(t *testing.T) {
	m := loaded(t, 100, 40)
	clear(m.Marked) // layout test: the cursor row shows its own marker
	out := m.View()
	mustContain(t, out, "◆ macdoc", "1 Overview", "6 Health", "asks before removing", "scan 2h ago", "What matters most", "Reclaimable",
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
	if m.Tab != cli.TabSuggestions {
		t.Fatalf("tab = %d", m.Tab)
	}
	mustContain(t, m.View(), "│ ▶ │ O-ab12cd", "│ ID", "│ Suggestion", "│ Restore", "SAFE 25.2 GB", "item 1 of 5", "enter remove", "e explain")
	m = step(t, m, keys("down", "down")...)
	if m.Cursor != 2 {
		t.Fatalf("cursor = %d", m.Cursor)
	}
	mustContain(t, m.View(), "│ ▶ │ O-z4n1ty", "item 3 of 5")

	// Narrow: e swaps the list for the detail; esc goes back.
	m = step(t, m, keys("e")...)
	mustContain(t, m.View(), "O-z4n1ty · Android system image API 29", "sdkmanager --uninstall", "esc back")
	m = step(t, m, keys("esc")...)
	if m.Detail {
		t.Error("esc should close the detail")
	}
	m = step(t, m, keys("6")...)
	mustContain(t, m.View(), "Is memory slowing it down?", "Memory is tight.", "Biggest users: Cursor 1.0 GB",
		"History builds up", "│ Why it matters")
	m = step(t, m, keys("pgdown", "pgdown")...)
	mustContain(t, m.View(), "Top memory users", "│ App or tool", "│ What it is", "Explained Cursor.")
	m = step(t, m, keys("5")...)
	mustContain(t, m.View(), "Reclaimable, by category", "What grew", "in the last 30 days", "│ Gradle", "+6.0 GB")
	if strings.Contains(m.View(), "│ Downloads") {
		t.Error("an unchanged area is listed under What grew")
	}
	m = step(t, m, keys("4")...)
	mustContain(t, m.View(), "│ Project", "~/code/app", "120 d · git commit", "uncommitted", "~/Desktop/site", "(protected)",
		"ios/Pods\u00a0300\u00a0MB", "DerivedData of deleted projects", "~/old/Gone.xcodeproj")
	m = step(t, m, keys("3")...)
	mustContain(t, m.View(), "│ Toolchain", "xcode", "docker")
}

func TestTUIWideShowsDetailBesideList(t *testing.T) {
	m := step(t, loaded(t, 140, 30), keys("2")...)
	clear(m.Marked) // layout test: the cursor row shows its own marker
	out := m.View()
	mustContain(t, out, "│ ▶ │ O-ab12cd", "SAFE  O-ab12cd · Xcode DerivedData · MyApp", "rebuilds on next build")
}

func TestTUISlashCommands(t *testing.T) {
	m := loaded(t, 100, 30)
	m = step(t, m, keys("/", "sp")...)
	out := m.View()
	mustContain(t, out, "▸ /space", "› /sp", "enter run", "tab complete")
	m = step(t, m, keys("enter")...)
	if m.Tab != cli.TabSpace || m.InputMode != 0 {
		t.Fatalf("/sp should run /space: tab=%d mode=%q", m.Tab, m.InputMode)
	}

	m = step(t, m, keys("/", "explain O-z4n1ty", "enter")...)
	if m.Tab != cli.TabSuggestions || !m.Detail || m.Items()[m.Cursor].ID != "O-z4n1ty" {
		t.Fatalf("/explain did not open the item: tab=%d detail=%v cursor=%d", m.Tab, m.Detail, m.Cursor)
	}
	m = step(t, m, keys("/", "explain O-nope", "enter")...)
	mustContain(t, m.View(), "O-nope is not in the latest scan")

	m = step(t, m, keys("esc", "/", "json", "enter")...)
	mustContain(t, m.View(), `"id": "O-ab12cd"`)
	m = step(t, m, keys("esc", "/", "mcp", "enter")...)
	mustContain(t, m.View(), "claude mcp add macdoc -- macdoc mcp", "only you apply them")
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
	if !m.Loading {
		t.Fatal("r should rescan")
	}
	m = step(t, m, keys("esc")...)
	if !m.Stopping {
		t.Fatal("esc should stop the scan")
	}
	partial := &cli.TuiData{Res: engine.OverviewResult{ScanAge: "just now"}}
	m = step(t, m, cli.LoadedMsg{Data: partial})
	if m.Data == partial || m.Data.Res.ScanAge != "2h ago" {
		t.Error("results of a stopped scan must not replace the previous ones")
	}
	mustContain(t, m.View(), "Scan stopped. Showing the previous results.")

	// With nothing to fall back on, say so.
	m = newTestTUI(t, 100, 30)
	m = step(t, m, keys("esc")...)
	m = step(t, m, cli.LoadedMsg{Err: context.Canceled})
	mustContain(t, m.View(), "Press r (or /scan) to scan again")
}

func TestTUIQuitWhileLoadingWaitsForEngine(t *testing.T) {
	m := newTestTUI(t, 100, 30)
	next, cmd := m.Update(keys("q")[0])
	m = next.(cli.TuiModel)
	if cmd != nil || !m.Quitting {
		t.Fatal("q while loading should stop the engine first")
	}
	_, cmd = m.Update(cli.LoadedMsg{Err: context.Canceled})
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
	plain := cli.HighlightJSON(cli.PlainTheme(), src)
	for _, want := range []string{`"id": "O-1"`, `"bytes": 12`, "true", "null"} {
		if !strings.Contains(plain, want) {
			t.Errorf("plain JSON lost %q", want)
		}
	}
	r := lipgloss.NewRenderer(nil)
	r.SetColorProfile(termenv.TrueColor)
	r.SetHasDarkBackground(true)
	colored := cli.HighlightJSON(cli.ColorTheme(r, 80), src)
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
		th := cli.ColorTheme(r, 80)
		if got := th.Badge(model.RiskSafe) + th.Ok.Render("ok"); !strings.Contains(got, c.want) {
			t.Errorf("profile %v: %q lacks %q", c.p, got, c.want)
		}
	}
	// Plain output has no escapes and no decorations.
	pt := cli.PlainTheme()
	if s := pt.Card("x") + pt.Badge(model.RiskSafe) + pt.Dot(model.RiskSafe) + pt.Command([]string{"brew", "cleanup"}); strings.Contains(s, "\x1b") || strings.Contains(s, "╭") {
		t.Errorf("plain theme decorated: %q", s)
	}
	if got := pt.Meter(50, 100, 10); got != cli.Bar(50, 100, 10) {
		t.Errorf("plain meter = %q", got)
	}
}

func TestFitColumnsKeepsShortColumnsWhole(t *testing.T) {
	cols := []cli.Column{{Title: "ID"}, {Title: "Size", Right: true}, {Title: "Risk"}, {Title: "Suggestion", Flex: true}, {Title: "Why", Wrap: true}}
	rows := [][]string{{"O-ab12cd", "10.7 MB", "CAUTION", "A very long suggestion title that will not fit", "a long explanation that has to wrap onto lines"}}
	out := cli.PlainLayoutTheme().Grid(cols, rows, 70, nil)
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
		t.Errorf("the wrap cli.Column should wrap:\n%s", out)
	}
}

func TestTUINarrowSuggestionsKeepSizes(t *testing.T) {
	m := step(t, loaded(t, 80, 30), keys("2")...)
	mustContain(t, m.View(), "14.2 GB", "~11.0 GB", "RISKY", "│ Restore")
}

// newIntroTUI is the dashboard as `macdoc` opens it: on the welcome screen.
func newIntroTUI(t *testing.T, w, h int, st cli.IntroStatus) (cli.TuiModel, *[]bool) {
	t.Helper()
	var loads []bool
	deps := &cli.TuiDeps{Load: func(_ context.Context, _ *cli.TuiDeps, rescan bool) (*cli.TuiData, error) {
		loads = append(loads, rescan)
		return tuiFixture(), nil
	}}
	m := cli.NewTUIModel(deps, cli.PlainLayoutTheme())
	m.Intro, m.IntroStatus = true, st
	return step(t, m, tea.WindowSizeMsg{Width: w, Height: h}), &loads
}

func TestTUIWelcomeScreen(t *testing.T) {
	m, _ := newIntroTUI(t, 110, 45, cli.IntroStatus{HasScan: true, Age: "2h ago", Bytes: 128_000_000_000, Files: 1_031_292})
	if m.Init() != nil {
		t.Fatal("nothing may run before the user chooses")
	}
	out := m.View()
	mustContain(t, out, "███╗   ███╗", "Why is my Mac full and slow?", "Last scan 2h ago · 128 GB in 1,031,292 files",
		"▶ enter", "Check my Mac", "Fresh scan first", "Keys and commands", "How it works", "Explore", "Clean up",
		"Read-only unless you confirm", "Nothing leaves your Mac", "From the shell", "macdoc doctor scan",
		"enter start", "s fresh scan", "q quit")
	if strings.Contains(out, "1 Overview") {
		t.Error("the welcome screen has no tabs")
	}
	if n := strings.Count(out, "\n"); n != 44 {
		t.Errorf("view is %d lines; want the window height", n+1)
	}

	// First run: says a scan will happen.
	m, _ = newIntroTUI(t, 70, 24, cli.IntroStatus{})
	mustContain(t, m.View(), "◆ macdoc", "No scan yet", "First run: scans your disk once")
}

func TestTUIWelcomeActions(t *testing.T) {
	m, loads := newIntroTUI(t, 110, 45, cli.IntroStatus{HasScan: true, Age: "1h ago"})
	next, cmd := m.Update(keys("enter")[0])
	m = next.(cli.TuiModel)
	if m.Intro || !m.Loading || cmd == nil {
		t.Fatal("enter should leave the welcome screen and load")
	}
	runBatch(cmd)
	if len(*loads) != 1 || (*loads)[0] {
		t.Errorf("enter should load the latest scan, not rescan: %v", *loads)
	}

	m, loads = newIntroTUI(t, 110, 45, cli.IntroStatus{})
	_, cmd = m.Update(keys("s")[0])
	runBatch(cmd)
	if len(*loads) != 1 || !(*loads)[0] {
		t.Errorf("s should rescan: %v", *loads)
	}

	// Arrow down to "Keys and commands", enter opens help; esc returns.
	m, _ = newIntroTUI(t, 110, 45, cli.IntroStatus{})
	m = step(t, m, keys("down", "down", "enter")...)
	mustContain(t, m.View(), "Keys", "Commands", "Safety")
	m = step(t, m, keys("esc")...)
	mustContain(t, m.View(), "How it works")

	// A view command starts the dashboard on that view; /json needs results.
	m, _ = newIntroTUI(t, 110, 45, cli.IntroStatus{})
	m = step(t, m, keys("/", "json", "enter")...)
	mustContain(t, m.View(), "Choose “Check my Mac” first")
	m = step(t, m, keys("/", "space", "enter")...)
	if m.Intro || m.Tab != cli.TabSpace || !m.Loading {
		t.Errorf("/space from the welcome screen: intro=%v tab=%d loading=%v", m.Intro, m.Tab, m.Loading)
	}

	m, _ = newIntroTUI(t, 110, 45, cli.IntroStatus{})
	if _, cmd := m.Update(keys("q")[0]); cmd == nil || cmd() != tea.Quit() {
		t.Error("q quits from the welcome screen")
	}
}

// runBatch runs a command and the commands it batches (the engine load and
// a spinner tick).
func runBatch(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	if b, ok := cmd().(tea.BatchMsg); ok {
		for _, c := range b {
			if c != nil {
				c()
			}
		}
	}
}

func TestFitColumnsKeepsWordsWhole(t *testing.T) {
	cols := []cli.Column{{Title: "Project", Wrap: true}, {Title: "Folders", Wrap: true}}
	rows := [][]string{{"~/code/a-rather-long-project-name and more", "node_modules 416 MB · ios/Pods 300 MB"}}
	out := cli.PlainLayoutTheme().Grid(cols, rows, 60, nil)
	mustContain(t, out, "~/code/a-rather-long-project-name", "node_modules 416 MB")
	for _, l := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if w := lipgloss.Width(l); w > 60 {
			t.Errorf("line is %d wide: %q", w, l)
		}
	}
	// Too narrow for whole words: it still fits, splitting words.
	out = cli.PlainLayoutTheme().Grid(cols, rows, 40, nil)
	for _, l := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if w := lipgloss.Width(l); w > 40 {
			t.Errorf("narrow: line is %d wide: %q", w, l)
		}
	}
}

// Every / command is reachable in the menu: all shown when there's room,
// scrolled into view on short windows.
func TestTUIPaletteShowsEveryCommand(t *testing.T) {
	m := step(t, loaded(t, 100, 40), keys("/")...)
	out := m.View()
	for _, c := range cli.SlashCommands {
		mustContain(t, out, "/"+c.Name)
	}
	if !strings.Contains(out, "/scan") || strings.Index(out, "/scan") > strings.Index(out, "/wins") {
		t.Error("/scan should be listed near the top")
	}

	m = step(t, loaded(t, 100, 12), keys("/")...)
	if strings.Contains(m.View(), "/quit") {
		t.Error("a short window can't show every command at once")
	}
	for range cli.SlashCommands {
		m = step(t, m, keys("down")...)
	}
	mustContain(t, m.View(), "▸ /quit")
	if n := strings.Count(m.View(), "\n"); n != 11 { // 12 rows
		t.Errorf("view is %d lines; want the window height", n+1)
	}
}

func TestWelcomePaletteFitsWindow(t *testing.T) {
	for _, h := range []int{14, 24, 40} {
		m, _ := newIntroTUI(t, 100, h, cli.IntroStatus{})
		m = step(t, m, keys("/")...)
		if n := strings.Count(m.View(), "\n"); n != h-1 {
			t.Errorf("h=%d: view is %d lines", h, n+1)
		}
		mustContain(t, m.View(), "/scan")
	}
}

// Phase 10: a fresh scan pre-marks its SAFE suggestions, and only those, so
// the picker starts with the safest items marked.
func TestPicker_PreMarksSafeOnly(t *testing.T) {
	m := loaded(t, 120, 40)
	saw := 0
	for _, op := range m.Items() {
		want := op.Risk == model.RiskSafe && op.Method != model.MethodAdvise
		if got := m.Marked[op.ID]; got != want {
			t.Errorf("%s (%s, %s): marked = %v, want %v", op.ID, op.Risk, op.Method, got, want)
		}
		if want {
			saw++
		}
	}
	if saw == 0 {
		t.Fatal("fixture has no SAFE suggestion; the test checks nothing")
	}
}
