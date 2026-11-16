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
	"github.com/sagnik26/macdoc/internal/planner"
	"github.com/sagnik26/macdoc/internal/probe"
	"github.com/sagnik26/macdoc/internal/projects"
	"github.com/sagnik26/macdoc/internal/store"
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
		"down": tea.KeyDown, "up": tea.KeyUp, "shift+tab": tea.KeyShiftTab, "pgdown": tea.KeyPgDown, "backspace": tea.KeyBackspace,
		"left": tea.KeyLeft, "right": tea.KeyRight}
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
	mustContain(t, out, "◆ macdoc", "1 Overview", "5 Space", "asks before removing", "scan 2h ago", "What matters most", "Reclaimable",
		"tab switch view", "/ commands", "? help",
		// Tables: headers, text risk labels, severities spelled out.
		"│ Metric", "│ Risk", "│ Tier", "Size │ Items", "SAFE", "RISKY", "ADVICE", "Critical", "╭", "╰")
	if strings.Contains(out, "@") {
		t.Error("the @ toolchain shortcut was removed")
	}
	if strings.Contains(out, "6 Health") {
		t.Error("the Health tab moved to /vitals, a page of its own")
	}
	if strings.Contains(out, "More: macdoc scan") {
		t.Error("the dashboard should not print the CLI's More line")
	}
	if n := strings.Count(out, "\n"); n != 39 {
		t.Errorf("view is %d lines; want exactly the window height", n+1)
	}

	m = step(t, m, keys("tab")...)
	if m.Tab != cli.TabSuggestions {
		t.Fatalf("tab = %d", m.Tab)
	}
	mustContain(t, m.View(), "│ ▶ │", "Xcode DerivedData", "│ Suggestion", "│ Restore", "SAFE 25.2 GB", "item 1 of 5", "enter remove", "e explain")
	m = step(t, m, keys("down", "down")...)
	if m.Cursor != 2 {
		t.Fatalf("cursor = %d", m.Cursor)
	}
	mustContain(t, m.View(), "│ ▶ │", "Android system image", "item 3 of 5")

	// Narrow: e swaps the list for the detail; esc goes back.
	m = step(t, m, keys("e")...)
	mustContain(t, m.View(), "O-z4n1ty · Android system image API 29", "sdkmanager --uninstall", "esc back")
	m = step(t, m, keys("esc")...)
	if m.Detail {
		t.Error("esc should close the detail")
	}
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
	mustContain(t, out, "│ ▶ │", "SAFE  O-ab12cd · Xcode DerivedData · MyApp", "rebuilds on next build")
}

func TestTUISlashCommands(t *testing.T) {
	m := loaded(t, 100, 30)
	m = step(t, m, keys("/", "vi")...)
	out := m.View()
	mustContain(t, out, "▸ /vitals", "› /vi", "enter run", "tab complete")
	m = step(t, m, keys("enter")...)
	if m.Overlay == cli.OverlayNone || m.InputMode != 0 {
		t.Fatalf("/vi should open the /vitals page: overlay=%d mode=%q", m.Overlay, m.InputMode)
	}
	mustContain(t, m.View(), "◆ macdoc", "Vitals")
	if strings.Contains(m.View(), "1 Overview") {
		t.Error("the vitals page has no tabs")
	}
	m = step(t, m, keys("esc")...)

	// /explain is gone; the e key on a suggestion explains it instead.
	m = step(t, m, keys("/", "explain O-z4n1ty", "enter")...)
	mustContain(t, m.View(), "Unknown command /explain")

	m = step(t, m, keys("esc", "/", "json", "enter")...)
	mustContain(t, m.View(), "Unknown command /json")
	m = step(t, m, keys("esc", "/", "mcp", "enter")...)
	mustContain(t, m.View(), "Unknown command /mcp")
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
	}, Plans: func(*cli.TuiDeps) ([]store.PlanRecord, error) { return nil, nil }}
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
		"▶ scan", "reclaim", "retire", "refresh", "explore", "vitals",
		"Check your whole Mac", "See what can be cleaned", "Where the space is, as a folder tree",
		"Read-only unless you confirm", "Nothing leaves your Mac", "enter run", "q quit")
	for _, gone := range []string{"How it works", "From the shell", "1 Overview"} {
		if strings.Contains(out, gone) {
			t.Errorf("the welcome screen should not show %q", gone)
		}
	}
	if n := strings.Count(out, "\n"); n != 44 {
		t.Errorf("view is %d lines; want the window height", n+1)
	}

	// First run: says a scan will happen.
	m, _ = newIntroTUI(t, 70, 24, cli.IntroStatus{})
	mustContain(t, m.View(), "◆ macdoc", "No scan yet", "First run: scans your disk once")
}

func TestTUIWelcomeActions(t *testing.T) {
	// Enter on "scan" with an earlier scan asks fresh or last; enter keeps the
	// last data and loads it without rescanning.
	m, loads := newIntroTUI(t, 110, 45, cli.IntroStatus{HasScan: true, Age: "1h ago"})
	m = step(t, m, keys("enter")...)
	mustContain(t, m.View(), "Use the last scan", "taken 1h ago", "Scan again")
	next, cmd := m.Update(keys("enter")[0])
	m = next.(cli.TuiModel)
	if m.Intro || !m.Loading || cmd == nil {
		t.Fatal("enter should leave the welcome screen and load")
	}
	runBatch(cmd)
	if len(*loads) != 1 || (*loads)[0] {
		t.Errorf("enter should load the latest scan, not rescan: %v", *loads)
	}

	// With no scan yet, "scan" scans straight away.
	m, loads = newIntroTUI(t, 110, 45, cli.IntroStatus{})
	_, cmd = m.Update(keys("enter")[0])
	runBatch(cmd)
	if len(*loads) != 1 || !(*loads)[0] {
		t.Errorf("scan with nothing to reuse should rescan: %v", *loads)
	}

	// Arrow up from "scan" wraps to "vitals"; enter opens its page and esc
	// returns to the welcome screen.
	m, _ = newIntroTUI(t, 110, 45, cli.IntroStatus{})
	m = step(t, m, keys("up", "enter")...)
	mustContain(t, m.View(), "◆ macdoc", "Vitals")
	if strings.Contains(m.View(), "1 Overview") {
		t.Error("the vitals page has no tabs")
	}
	m = step(t, m, keys("esc")...)
	mustContain(t, m.View(), "reclaim", "vitals")

	// ? opens the help.
	m, _ = newIntroTUI(t, 110, 45, cli.IntroStatus{})
	m = step(t, m, keys("?")...)
	mustContain(t, m.View(), "Keys", "Commands", "Safety")

	// /vitals runs on its own: no scan, no loading, and the welcome screen is
	// still there when the page closes.
	m, loads = newIntroTUI(t, 110, 45, cli.IntroStatus{})
	m = step(t, m, keys("/", "vitals", "enter")...)
	if !m.Intro || m.Loading || m.Overlay == cli.OverlayNone || len(*loads) != 0 {
		t.Errorf("/vitals from the welcome screen: intro=%v loading=%v overlay=%d loads=%v", m.Intro, m.Loading, m.Overlay, *loads)
	}
	m = step(t, m, keys("esc")...)
	mustContain(t, m.View(), "▶ scan")

	m, _ = newIntroTUI(t, 110, 45, cli.IntroStatus{})
	if _, cmd := m.Update(keys("q")[0]); cmd == nil || cmd() != tea.Quit() {
		t.Error("q quits from the welcome screen")
	}
}

// /reclaim and /retire need the latest scan, and they read it on their own:
// they never tell the person to check the Mac first.
func TestTUIReclaimRunsOnItsOwn(t *testing.T) {
	m, loads := newIntroTUI(t, 110, 45, cli.IntroStatus{HasScan: true, Age: "1h ago"})
	next, cmd := m.RunCommand("/reclaim")
	got := next.(cli.TuiModel)
	if !got.Loading || cmd == nil || strings.Contains(got.Flash, "Check") {
		t.Fatalf("/reclaim with no data should load by itself: loading=%v flash=%q", got.Loading, got.Flash)
	}
	mustContain(t, got.View(), "Reclaim")
	if strings.Contains(got.View(), "1 Overview") {
		t.Error("loading for /reclaim shows no tabs")
	}
	runBatch(cmd)
	if len(*loads) != 1 || (*loads)[0] {
		t.Errorf("it should read the latest scan, not rescan: %v", *loads)
	}
	done := step(t, got, cli.LoadedMsg{Data: tuiFixture()})
	mustContain(t, done.View(), "What do you want to clean?", "[x] SAFE")
	if !done.Intro {
		t.Error("the welcome screen should stay underneath")
	}
	back := step(t, done, keys("esc")...)
	mustContain(t, back.View(), "▶ scan")
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
	if !strings.Contains(out, "/scan") || strings.Index(out, "/scan") > strings.Index(out, "/vitals") {
		t.Error("/scan should be listed near the top")
	}

	last := "/" + cli.SlashCommands[len(cli.SlashCommands)-1].Name
	m = step(t, loaded(t, 100, 12), keys("/")...)
	if strings.Contains(m.View(), last) {
		t.Error("a short window can't show every command at once")
	}
	for range cli.SlashCommands {
		m = step(t, m, keys("down")...)
	}
	mustContain(t, m.View(), "▸ "+last)
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

// /retire reads the software inventory in the background and shows it in
// an overlay; esc closes it. Reading is replaced by a fake here.
func TestTUIInstalledCommand(t *testing.T) {
	deps := &cli.TuiDeps{Home: "/Users/me",
		Load: func(context.Context, *cli.TuiDeps, bool) (*cli.TuiData, error) { return tuiFixture(), nil },
		Software: func(context.Context, *cli.TuiDeps) (engine.SoftwareResult, error) {
			return engine.SoftwareResult{
				Software:     []engine.SoftwareEntry{{ID: "app:com.figma.Desktop", Name: "Figma", Kind: "app", Version: "124.3", Bytes: 500, Folders: []engine.OwnedFolder{{Path: "/x", Bytes: 500}}, CoveredBy: []string{}}},
				Unattributed: []engine.OwnedFolder{}, New: []string{}, Sources: []engine.SourceState{{Source: "apps", State: "ok"}},
			}, nil
		}}
	m := cli.NewTUIModel(deps, cli.PlainLayoutTheme())
	m = step(t, m, tea.WindowSizeMsg{Width: 100, Height: 30}, cli.LoadedMsg{Data: tuiFixture()})

	m = openInstalled(t, m)
	mustContain(t, m.View(), "Installed software", "Figma")
	m = step(t, m, keys("esc")...)
	if strings.Contains(m.View(), "Figma") {
		t.Error("esc should close the software list")
	}
}

// softwareModel is the dashboard with a fixed software inventory: Docker has a
// cleanup, Notion has data but no cleanup, and pipx is a program with no data.
func softwareModel(t *testing.T) cli.TuiModel {
	t.Helper()
	deps := &cli.TuiDeps{Home: "/Users/me",
		Load: func(context.Context, *cli.TuiDeps, bool) (*cli.TuiData, error) { return tuiFixture(), nil },
		Software: func(context.Context, *cli.TuiDeps) (engine.SoftwareResult, error) {
			return engine.SoftwareResult{
				Software: []engine.SoftwareEntry{
					{ID: "bundle:com.docker.docker", Name: "Docker", Kind: "app", Version: "4.30", Bytes: 900_000_000,
						Folders:   []engine.OwnedFolder{{Path: "/Users/me/Library/Containers/com.docker.docker", Bytes: 900_000_000, Confidence: 0.9, Evidence: "bundle id"}},
						CoveredBy: []string{"docker.bc"}},
					{ID: "app:com.notion.id", Name: "Notion", Kind: "app", Version: "7.4", Bytes: 4_900_000_000,
						Folders:   []engine.OwnedFolder{{Path: "/Users/me/Library/Caches/notion", Bytes: 4_900_000_000, Confidence: 0.6, Evidence: "name"}},
						CoveredBy: []string{}},
					{ID: "tool:pipx", Name: "pipx", Kind: "tool", Folders: []engine.OwnedFolder{}, CoveredBy: []string{}},
				},
				Unattributed: []engine.OwnedFolder{{Path: "/Users/me/Downloads", Bytes: 723_000_000}},
				New:          []string{"tool:pipx"},
				Sources:      []engine.SourceState{{Source: "apps", State: "ok"}},
			}, nil
		}}
	m := cli.NewTUIModel(deps, cli.PlainLayoutTheme())
	return step(t, m, tea.WindowSizeMsg{Width: 120, Height: 60}, cli.LoadedMsg{Data: tuiFixture()})
}

// openInstalled runs /retire and feeds back the inventory it reads. The
// read comes with a spinner tick in a batch, as the program delivers it.
func openInstalled(t *testing.T, m cli.TuiModel) cli.TuiModel {
	t.Helper()
	next, cmd := m.RunCommand("/retire")
	if cmd == nil {
		t.Fatal("/retire should read the inventory")
	}
	m = next.(cli.TuiModel)
	msg := cmd()
	b, ok := msg.(tea.BatchMsg)
	if !ok {
		return step(t, m, msg)
	}
	for _, c := range b {
		if c == nil {
			continue
		}
		if sm, ok := c().(cli.SoftwareMsg); ok {
			m = step(t, m, sm)
		}
	}
	return m
}

// The summary counts cleanups and the filters narrow the list; new and all work.
func TestTUIInstalledSummaryAndFilters(t *testing.T) {
	m := openInstalled(t, softwareModel(t))
	mustContain(t, m.View(), "1 program has cleanups", "New since last scan: pipx", "with data · 2 of 3")
	m = step(t, m, keys("f")...)
	mustContain(t, m.View(), "cleanable · 1 of 3")
	m = step(t, m, keys("f")...)
	mustContain(t, m.View(), "new · 1 of 3")
	m = step(t, m, keys("f")...)
	mustContain(t, m.View(), "all · 3 of 3")
}

// c marks the suggestions the installed programs' cleanups produce, opens the
// Suggestions tab, and changes nothing else. Advice-only items stay unmarked.
func TestTUIInstalledCleanupLink(t *testing.T) {
	m := openInstalled(t, softwareModel(t))
	m = step(t, m, keys("c")...)
	if m.Tab != cli.TabSuggestions || !m.Marked["O-k3m9pq"] {
		t.Fatalf("c should mark the Docker cleanup and open Suggestions: tab=%d marked=%v", m.Tab, m.Marked)
	}
	mustContain(t, m.Flash, "Marked 1 suggestions", "Nothing is removed yet")
}

// enter opens a program's details, with its folders and cleanups; esc goes back.
func TestTUIInstalledDetails(t *testing.T) {
	m := openInstalled(t, softwareModel(t))
	m = step(t, m, keys("down")...)
	m = step(t, m, keys("enter")...)
	mustContain(t, m.View(), "Program", "Notion", "Data folders", "~/Library/Caches/notion", "No cleanup recipe covers this program")
	m = step(t, m, keys("esc")...)
	if strings.Contains(m.View(), "Data folders") || !strings.Contains(m.View(), "Installed software") {
		t.Error("esc should go back to the list, not close the view")
	}
	m = step(t, m, keys("esc")...)
	if strings.Contains(m.View(), "Installed software") {
		t.Error("a second esc should close the view")
	}
}

// Regression: Enter in a program's details must not leave the view. It used
// to close the overlay and land on Overview.
func TestTUIInstalledEnterInDetailsStaysInView(t *testing.T) {
	m := openInstalled(t, softwareModel(t))
	m = step(t, m, keys("enter")...)
	m = step(t, m, keys("enter")...)
	if m.Overlay == cli.OverlayNone || !strings.Contains(m.View(), "Data folders") {
		t.Errorf("Enter in the details left the view: overlay=%v", m.Overlay)
	}
	if m.Tab != 0 { // 0 is the Overview tab
		t.Errorf("tab changed to %d", m.Tab)
	}
}

// Regression: while the inventory is read the view opens at once, shows a
// spinner and the time so far, and esc closes it. The result is kept, but the
// view doesn't reopen by itself.
func TestTUIInstalledShowsLoadingState(t *testing.T) {
	m := softwareModel(t)
	next, cmd := m.RunCommand("/retire")
	if cmd == nil {
		t.Fatal("/retire should start the read")
	}
	m = next.(cli.TuiModel)
	mustContain(t, m.View(), "Reading installed software", "esc closes this view")
	m = step(t, m, keys("esc")...)
	if m.Overlay != cli.OverlayNone {
		t.Errorf("esc should close the view while reading: overlay=%v", m.Overlay)
	}
}

// The tab row is left out of overlays, where its keys don't work, and comes
// back when the overlay closes.
func TestTUITabsHiddenInOverlays(t *testing.T) {
	m := openInstalled(t, softwareModel(t))
	if strings.Contains(m.View(), "2 Suggestions") {
		t.Error("the tab row should not show over the installed view")
	}
	m = step(t, m, keys("esc")...)
	mustContain(t, m.View(), "2 Suggestions")
}

// Regression: in a short window the arrow keys must reach the tables below the
// program list. They used to move the cursor only, so "Folders no software
// explains" and "Discovery" could not be scrolled to.
func TestTUIInstalledArrowsReachLowerTables(t *testing.T) {
	m := softwareModel(t)
	m = step(t, m, tea.WindowSizeMsg{Width: 120, Height: 14})
	m = openInstalled(t, m)
	if !strings.Contains(m.View(), "Installed software") || strings.Contains(m.View(), "Notion") {
		t.Fatal("the window is too short to show the list at once; the test needs its last row below the fold")
	}
	for i := 0; i < 40; i++ {
		m = step(t, m, keys("down")...)
	}
	mustContain(t, m.View(), "Notion")
	for i := 0; i < 40; i++ {
		m = step(t, m, keys("up")...)
	}
	mustContain(t, m.View(), "Installed software")
}

// exploreModel is the dashboard with a two-level fake scan: home holds a
// folder, a file and a remainder; the folder holds one file.
func exploreModel(t *testing.T) cli.TuiModel {
	t.Helper()
	size := func(n int64) *int64 { return &n }
	home := engine.ExploreResult{Path: "/Users/me", Parent: "/Users", Bytes: size(900), Status: "complete",
		Entries: []engine.ExploreEntry{
			{Name: "Caches", Path: "/Users/me/Caches", Kind: "dir", Bytes: 500, Share: 0.55},
			{Name: "big.bin", Path: "/Users/me/big.bin", Kind: "file", Bytes: 300, Share: 0.33},
			{Name: "smaller items", Kind: "other", Bytes: 100, Share: 0.11},
		},
		LargestFiles: []engine.ExploreEntry{{Name: "big.bin", Path: "/Users/me/big.bin", Kind: "file", Bytes: 300}}}
	caches := engine.ExploreResult{Path: "/Users/me/Caches", Parent: "/Users/me", Bytes: size(500), Status: "complete",
		Entries:      []engine.ExploreEntry{{Name: "inner.db", Path: "/Users/me/Caches/inner.db", Kind: "file", Bytes: 500, Share: 1}},
		LargestFiles: []engine.ExploreEntry{}}
	deps := &cli.TuiDeps{Home: "/Users/me",
		Load: func(context.Context, *cli.TuiDeps, bool) (*cli.TuiData, error) { return tuiFixture(), nil },
		Explore: func(_ context.Context, _ *cli.TuiDeps, path string) (engine.ExploreResult, error) {
			if path == "/Users/me/Caches" {
				return caches, nil
			}
			return home, nil
		}}
	m := cli.NewTUIModel(deps, cli.PlainLayoutTheme())
	return step(t, m, tea.WindowSizeMsg{Width: 120, Height: 40}, cli.LoadedMsg{Data: tuiFixture()})
}

// Enter on the Space tab opens the explorer at home as a tree: enter or → opens
// a folder in place, ← closes it, Enter on a file explains, Backspace goes up,
// esc closes. There is no "biggest files" table, and no tabs.
func TestTUIExplorerNavigation(t *testing.T) {
	m := exploreModel(t)
	m = step(t, m, keys("5", "enter")...)
	mustContain(t, m.View(), "◆ macdoc", "Explore", "▸ Caches/", "big.bin", "smaller items", "├─", "└─", "55.0%")
	for _, gone := range []string{"Biggest files below", "1 Overview"} {
		if strings.Contains(m.View(), gone) {
			t.Errorf("the explorer should not show %q", gone)
		}
	}
	m = step(t, m, keys("enter")...) // open the folder in place
	mustContain(t, m.View(), "▾ Caches/", "inner.db", "big.bin", "smaller items", "│")
	m = step(t, m, keys("down", "enter")...) // on the file inside
	mustContain(t, m.Flash, "Only folders open")
	m = step(t, m, keys("left")...) // a file: jump to its folder
	m = step(t, m, keys("left")...) // close it
	if strings.Contains(m.View(), "inner.db") {
		t.Error("← should close the folder")
	}
	m = step(t, m, keys("backspace")...)
	mustContain(t, m.View(), "smaller items")
	m = step(t, m, keys("esc")...)
	if strings.Contains(m.View(), "big.bin") {
		t.Error("esc should close the explorer")
	}
}

// statusModel is the dashboard with a fake live reading.
func statusModel(t *testing.T) (cli.TuiModel, *int) {
	t.Helper()
	reads := 0
	deps := &cli.TuiDeps{Home: "/Users/me",
		Load: func(context.Context, *cli.TuiDeps, bool) (*cli.TuiData, error) { return tuiFixture(), nil },
		Status: func(context.Context, *cli.TuiDeps) engine.StatusResult {
			reads++
			return engine.StatusResult{Score: 83, Reasons: []string{"-10 memory pressure is warn", "-7 something else"},
				CPU: &probe.CPU{UsagePercent: 42, Load1: 1.5, LogicalCores: 8}}
		}}
	m := cli.NewTUIModel(deps, cli.PlainLayoutTheme())
	return step(t, m, tea.WindowSizeMsg{Width: 120, Height: 50}, cli.LoadedMsg{Data: tuiFixture()}), &reads
}

// deliverStatus runs the commands batched with a key press and feeds back the
// live reading, as the program does.
func deliverStatus(t *testing.T, m cli.TuiModel, cmd tea.Cmd) cli.TuiModel {
	t.Helper()
	if cmd == nil {
		return m
	}
	msg := cmd()
	b, ok := msg.(tea.BatchMsg)
	if !ok { // a batch of one command is that command
		if sm, ok := msg.(cli.StatusMsg); ok {
			return step(t, m, sm)
		}
		return m
	}
	for _, c := range b {
		if c == nil {
			continue
		}
		if sm, ok := c().(cli.StatusMsg); ok {
			m = step(t, m, sm)
		}
	}
	return m
}

// openVitals types /vitals and returns the model and the command that takes
// the first live reading.
func openVitals(t *testing.T, m cli.TuiModel) (cli.TuiModel, tea.Cmd) {
	t.Helper()
	m = step(t, m, keys("/", "vitals")...)
	next, cmd := m.Update(keys("enter")[0])
	return next.(cli.TuiModel), cmd
}

// Opening the vitals page takes one live reading and shows its score and
// reasons; a second update while one is running does not start another. The
// page has no findings table and no memory question.
func TestTUIVitalsPageShowsALiveReading(t *testing.T) {
	m, reads := statusModel(t)
	m, cmd := openVitals(t, m)
	if cmd == nil {
		t.Fatal("opening the vitals page should start a live reading")
	}
	if _, again := m.Update(tea.WindowSizeMsg{Width: 120, Height: 50}); again != nil {
		t.Error("a reading is already running; no second one should start")
	}
	m = deliverStatus(t, m, cmd)
	mustContain(t, m.View(), "Vitals", "Health 83 / 100", "memory pressure is warn", "CPU")
	for _, gone := range []string{"Findings", "Is memory slowing it down?", "1 Overview"} {
		if strings.Contains(m.View(), gone) {
			t.Errorf("the vitals page should not show %q", gone)
		}
	}
	if strings.Contains(m.View(), "slowing your Mac down") {
		t.Error("with normal memory there is no alert")
	}
	if *reads != 1 {
		t.Errorf("took %d readings, want 1", *reads)
	}
}

// Memory is shown as a red alert only when it is what slows the Mac down.
func TestTUIVitalsAlertsOnlyWhenMemoryIsTight(t *testing.T) {
	for _, tc := range []struct {
		pressure probe.Pressure
		alert    bool
	}{{probe.PressureNormal, false}, {probe.PressureWarn, true}, {probe.PressureCritical, true}} {
		deps := &cli.TuiDeps{Home: "/Users/me", Status: func(context.Context, *cli.TuiDeps) engine.StatusResult {
			return engine.StatusResult{Score: 70, Report: probe.Report{Memory: probe.Memory{TotalBytes: 8 << 30, FreePercent: 10, Pressure: tc.pressure}}}
		}}
		m := step(t, cli.NewTUIModel(deps, cli.PlainLayoutTheme()), tea.WindowSizeMsg{Width: 120, Height: 50})
		m, cmd := openVitals(t, m)
		m = deliverStatus(t, m, cmd)
		got := strings.Contains(m.View(), "Memory is slowing your Mac down")
		if got != tc.alert {
			t.Errorf("pressure %s: alert shown = %v, want %v\n%s", tc.pressure, got, tc.alert, m.View())
		}
	}
}

// Other tabs never poll.
func TestTUIOtherTabsDoNotPollTheLiveStatus(t *testing.T) {
	m, reads := statusModel(t)
	for _, k := range []string{"1", "2", "3", "4", "5"} {
		next, cmd := m.Update(keys(k)[0])
		m = next.(cli.TuiModel)
		if cmd != nil {
			t.Errorf("tab %s started a command; only the Health tab reads the live status", k)
		}
	}
	if *reads != 0 {
		t.Errorf("took %d readings on other tabs", *reads)
	}
}

// A reading is followed by a wait, not by the next reading: right after one
// arrives, the only command is the tick that sleeps, never another read.
func TestTUILiveStatusWaitsBetweenReadings(t *testing.T) {
	m, reads := statusModel(t)
	m, cmd := openVitals(t, m)
	m = deliverStatus(t, m, cmd)
	_, after := m.Update(cli.StatusMsg{Res: engine.StatusResult{Score: 90}})
	if after == nil {
		t.Fatal("a reading should schedule the next one")
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- after() }()
	select {
	case msg := <-done:
		if _, isBatch := msg.(tea.BatchMsg); isBatch {
			t.Error("a reading started the next one at once; it must wait for the tick")
		}
	case <-time.After(300 * time.Millisecond): // the tick is sleeping: correct
	}
	if *reads != 1 {
		t.Errorf("took %d readings, want 1", *reads)
	}
}

// The palette lists the new features, and each opens from the / command:
// /status shows the Health tab's live block, /explore opens the explorer
// (with an optional folder), /refresh lists the steps with their IDs.
func TestTUIPaletteOpensTheNewFeatures(t *testing.T) {
	names := map[string]bool{}
	for _, c := range cli.SlashCommands {
		names[c.Name] = true
	}
	for _, want := range []string{"vitals", "explore", "retire", "refresh"} {
		if !names[want] {
			t.Errorf("/%s is missing from the palette", want)
		}
	}

	m, _ := statusModel(t)
	next, _ := m.RunCommand("/vitals")
	mustContain(t, next.(cli.TuiModel).View(), "Vitals")

	em := exploreModel(t)
	next, _ = em.RunCommand("/explore")
	mustContain(t, next.(cli.TuiModel).View(), "▸ Caches/", "smaller items")
	next, _ = em.RunCommand("/explore /Users/me/Caches")
	mustContain(t, next.(cli.TuiModel).View(), "inner.db")

	deps := &cli.TuiDeps{Home: "/Users/me",
		Load: func(context.Context, *cli.TuiDeps, bool) (*cli.TuiData, error) { return tuiFixture(), nil },
		Maintenance: func(context.Context, *cli.TuiDeps) (engine.OpportunityResult, error) {
			return engine.OpportunityResult{Opportunities: []model.Opportunity{
				{ID: "O-dnsaaa", RecipeID: "maintenance.flush_dns", Title: "Flush the DNS cache"}}}, nil
		}}
	mm := step(t, cli.NewTUIModel(deps, cli.PlainLayoutTheme()), tea.WindowSizeMsg{Width: 120, Height: 40}, cli.LoadedMsg{Data: tuiFixture()})
	next, _ = mm.RunCommand("/refresh")
	mustContain(t, next.(cli.TuiModel).View(), "Refresh", "│ ID", "│ Step", "Mac password", "│ Action", "O-dnsaaa", "Flush the DNS cache", "Apply")
	if strings.Contains(next.(cli.TuiModel).View(), "1 Overview") {
		t.Error("the refresh page has no tabs")
	}
}

// Explore, refresh, apply and vitals run on their own: with no dashboard data
// they open their page instead of asking for a check first.
func TestTUIStandaloneCommandsNeedNoScan(t *testing.T) {
	size := int64(10)
	deps := &cli.TuiDeps{Home: "/Users/me",
		Explore: func(context.Context, *cli.TuiDeps, string) (engine.ExploreResult, error) {
			return engine.ExploreResult{Path: "/Users/me", Bytes: &size, Status: "complete"}, nil
		},
		Maintenance: func(context.Context, *cli.TuiDeps) (engine.OpportunityResult, error) {
			return engine.OpportunityResult{}, nil
		},
		Plans: func(*cli.TuiDeps) ([]store.PlanRecord, error) { return nil, nil }}
	m := step(t, cli.NewTUIModel(deps, cli.PlainLayoutTheme()), tea.WindowSizeMsg{Width: 110, Height: 30})
	for c, want := range map[string]string{"/explore": "Explore", "/refresh": "No maintenance steps", "/vitals": "Vitals"} {
		next, _ := m.RunCommand(c)
		got := next.(cli.TuiModel)
		if got.Overlay == cli.OverlayNone || strings.Contains(got.Flash, "Check") {
			t.Errorf("%s: overlay %d flash %q; it should open without a scan", c, got.Overlay, got.Flash)
			continue
		}
		mustContain(t, got.View(), want)
	}
}

// /reclaim opens its own page. /help and /quit are not in the menu;
// the footer shows them while typing, and typing them still works.
func TestTUIReclaimAndHelpQuitInTheFooter(t *testing.T) {
	m := loaded(t, 100, 40)
	next, _ := m.RunCommand("/reclaim")
	if got := next.(cli.TuiModel); got.Overlay == cli.OverlayNone || strings.Contains(got.View(), "1 Overview") {
		t.Errorf("/reclaim should open its own page without tabs: overlay %d", got.Overlay)
	}

	typing := step(t, m, keys("/")...)
	out := typing.View()
	for _, c := range cli.SlashCommands {
		if c.Name == "help" || c.Name == "quit" {
			t.Errorf("/%s should not be a menu entry", c.Name)
		}
	}
	mustContain(t, out, "/reclaim", "/help help", "/quit quit")

	next, _ = m.RunCommand("/help")
	if next.(cli.TuiModel).Overlay == cli.OverlayNone {
		t.Error("typing /help should still open the help")
	}
	if _, cmd := m.RunCommand("/quit"); cmd == nil || cmd() != tea.Quit() {
		t.Error("typing /quit should still quit")
	}
}

// /overview is not a menu entry (the 1 key opens the Overview tab), but typing
// it still works.
func TestTUIOverviewIsNotInTheMenuButStillTypes(t *testing.T) {
	for _, c := range cli.SlashCommands {
		if c.Name == "overview" {
			t.Error("/overview should not be a menu entry")
		}
	}
	m := step(t, loaded(t, 100, 40), keys("5")...) // away from Overview
	next, _ := m.RunCommand("/overview")
	if got := next.(cli.TuiModel).Tab; got != 0 { // 0 is the Overview tab
		t.Errorf("typed /overview opened tab %d, want Overview (0)", got)
	}
}

// /scan asks whether to scan again or keep the last data, when there is a
// last scan. f scans again, enter keeps the data, esc cancels.
func TestTUIScanAsksFreshOrLast(t *testing.T) {
	m := loaded(t, 100, 40)
	next, cmd := m.RunCommand("/scan")
	asked := next.(cli.TuiModel)
	if cmd != nil || asked.Overlay == cli.OverlayNone || asked.Loading {
		t.Fatalf("/scan should ask first: overlay %d loading %v", asked.Overlay, asked.Loading)
	}
	mustContain(t, asked.View(), "Use the last scan", "recommended", "Scan again")

	fresh := step(t, asked, keys("f")...)
	if !fresh.Loading || fresh.Overlay != cli.OverlayNone {
		t.Errorf("f should start a fresh scan: loading %v overlay %d", fresh.Loading, fresh.Overlay)
	}
	keep := step(t, asked, keys("enter")...)
	if keep.Loading || keep.Overlay != cli.OverlayNone || !strings.Contains(keep.Flash, "Using the last scan") {
		t.Errorf("enter should keep the last data: loading %v overlay %d flash %q", keep.Loading, keep.Overlay, keep.Flash)
	}
	again := step(t, asked, keys("down", "enter")...)
	if !again.Loading || again.Overlay != cli.OverlayNone {
		t.Errorf("down then enter should scan again: loading %v overlay %d", again.Loading, again.Overlay)
	}
	cancel := step(t, asked, keys("esc")...)
	if cancel.Loading || cancel.Overlay != cli.OverlayNone {
		t.Error("esc should cancel without scanning")
	}
}

// From the welcome screen the same choice appears when a scan exists, and
// /scan scans straight away when there is nothing to reuse.
func TestTUIScanChoiceFromTheWelcomeScreen(t *testing.T) {
	m, _ := newIntroTUI(t, 110, 45, cli.IntroStatus{HasScan: true, Age: "3h ago", Bytes: 5_000_000_000})
	next, _ := m.RunCommand("/scan")
	asked := next.(cli.TuiModel)
	if !asked.Intro || asked.Overlay == cli.OverlayNone || asked.Loading {
		t.Fatalf("with a scan to reuse /scan should ask: intro %v overlay %d loading %v", asked.Intro, asked.Overlay, asked.Loading)
	}
	mustContain(t, asked.View(), "taken 3h ago")
	keep := step(t, asked, keys("enter")...)
	if keep.Intro || !keep.Loading {
		t.Errorf("enter should open the dashboard on the last data: intro %v loading %v", keep.Intro, keep.Loading)
	}

	none, _ := newIntroTUI(t, 110, 45, cli.IntroStatus{})
	next, _ = none.RunCommand("/scan")
	if got := next.(cli.TuiModel); got.Overlay != cli.OverlayNone || !got.Loading {
		t.Errorf("with no earlier scan /scan should scan at once: overlay %d loading %v", got.Overlay, got.Loading)
	}
}

// reclaimModel is the dashboard with a fake plan maker, so Enter on the
// checkbox list can start sealing a plan.
func reclaimModel(t *testing.T) cli.TuiModel {
	t.Helper()
	m := loaded(t, 110, 40)
	m.Deps.MakePlan = func(*cli.TuiDeps, planner.Selector, *engine.OpportunityResult) (engine.PlanResult, error) {
		return samplePlan(), nil
	}
	return m
}

// /reclaim in the dashboard opens a page with the same
// "What do you want to clean?" checkbox list as `macdoc reclaim`.
func TestTUIReclaimOpensTheCheckboxList(t *testing.T) {
	m := reclaimModel(t)
	next, _ := m.RunCommand("/reclaim")
	got := next.(cli.TuiModel)
	if got.Overlay == cli.OverlayNone {
		t.Fatalf("overlay %d; want the checkbox list", got.Overlay)
	}
	mustContain(t, got.View(), "Reclaim", "What do you want to clean?", "[x] SAFE", "[ ] CAUTION", "[-] ADVICE", "enter/space tick", "[ Clean 2 items")
	if strings.Contains(got.View(), "1 Overview") {
		t.Error("the reclaim page has no tabs")
	}
}

// Enter marks exactly the suggestions of the ticked risk types and starts the
// removal (a plan is sealed, then the confirmation box asks again); advice-only
// items are never marked, even with "a".
func TestTUIReclaimEnterMarksOnlyTheTickedTypes(t *testing.T) {
	count := func(m cli.TuiModel, want func(model.Opportunity) bool) int {
		n := 0
		for _, op := range m.Items() {
			if want(op) {
				n++
			}
		}
		return n
	}
	m := reclaimModel(t)
	open, _ := m.RunCommand("/reclaim")
	// Enter on a type only ticks or unticks it; the Clean button starts the removal.
	onType, c0 := open.(cli.TuiModel).Update(keys("enter")[0])
	if c0 != nil || onType.(cli.TuiModel).Planning || onType.(cli.TuiModel).Overlay == cli.OverlayNone {
		t.Fatalf("enter on a type must not start anything")
	}
	onType, _ = onType.(cli.TuiModel).Update(keys("enter")[0]) // SAFE is ticked again
	atButton := step(t, onType.(cli.TuiModel), keys("down", "down", "down", "down")...)
	started, cmd := atButton.Update(keys("enter")[0])
	got := started.(cli.TuiModel)
	if cmd == nil || !got.Planning || got.Overlay != cli.OverlayNone {
		t.Fatalf("enter should close the list and start sealing: planning %v overlay %d", got.Planning, got.Overlay)
	}
	safe := count(got, func(op model.Opportunity) bool { return op.Risk == model.RiskSafe && op.Method != model.MethodAdvise })
	if safe == 0 || len(got.Marked) != safe {
		t.Errorf("marked %d, want the %d SAFE suggestions only", len(got.Marked), safe)
	}
}

// Tick everything with a: CAUTION and RISKY are marked too, advice never.
func TestTUIReclaimTickAllMarksNoAdvice(t *testing.T) {
	m := reclaimModel(t)
	all := step(t, func() cli.TuiModel { n, _ := m.RunCommand("/reclaim"); return n.(cli.TuiModel) }(), keys("a")...)
	started, _ := step(t, all, keys("down", "down", "down", "down")...).Update(keys("enter")[0])
	got := started.(cli.TuiModel)
	for _, op := range got.Items() {
		if got.Marked[op.ID] && (op.Method == model.MethodAdvise || op.Risk == model.RiskAdvise) {
			t.Errorf("advice-only item %s was marked", op.ID)
		}
	}
	want := 0
	for _, op := range got.Items() {
		if op.Method != model.MethodAdvise {
			want++
		}
	}
	if len(got.Marked) != want {
		t.Errorf("a then Clean marked %d, want all %d actionable", len(got.Marked), want)
	}
}

// Esc closes the list without marking or planning anything, and the keys of
// the list never end the dashboard.
func TestTUIReclaimEscChangesNothing(t *testing.T) {
	m := reclaimModel(t)
	open, _ := m.RunCommand("/reclaim")
	before := len(open.(cli.TuiModel).Marked) // the dashboard pre-marks SAFE items after a scan
	closed, cmd := open.(cli.TuiModel).Update(keys("esc")[0])
	got := closed.(cli.TuiModel)
	if got.Overlay != cli.OverlayNone || len(got.Marked) != before || got.Planning {
		t.Errorf("esc: overlay %d marked %d (was %d) planning %v", got.Overlay, len(got.Marked), before, got.Planning)
	}
	if cmd != nil && cmd() == tea.Quit() {
		t.Error("closing the list must not quit the dashboard")
	}
	// q cancels the list too, instead of quitting.
	_, qcmd := open.(cli.TuiModel).Update(keys("q")[0])
	if qcmd != nil && qcmd() == tea.Quit() {
		t.Error("q in the list must cancel it, not quit macdoc")
	}
}

// /reclaim puts the checkbox list and its Clean button first, then the full
// table of every suggestion, whatever is ticked.
func TestTUIReclaimListFirstThenFullTable(t *testing.T) {
	m := loaded(t, 110, 40)
	next, _ := m.RunCommand("/reclaim")
	m = next.(cli.TuiModel)
	out := m.View()
	mustContain(t, out, "What do you want to clean?", "[ Clean 2 items", "SAFE 25.2 GB", "Xcode DerivedData", "Docker build cache",
		"Android system image", "Lost data", "│ Suggestion")
	if strings.Index(out, "What do you want to clean?") > strings.Index(out, "Docker build cache") {
		t.Error("the list should come before the table")
	}
	if strings.Contains(out, "1 Overview") {
		t.Error("the reclaim page shows no tabs")
	}

	// Ticking and unticking changes the button, not the table.
	m = step(t, m, keys("down", "down", " ")...)
	mustContain(t, m.View(), "[ Clean 4 items", "Android system image", "Xcode DerivedData")
	m = step(t, m, keys(" ", "up", "up", " ")...)
	mustContain(t, m.View(), "[ Clean: nothing ticked ]", "Android system image", "Xcode DerivedData", "Lost data")
	for _, gone := range []string{"preview, not ticked", "▶"} {
		if strings.Contains(m.View(), gone) {
			t.Errorf("the table has no previews or cursor, but shows %q", gone)
		}
	}
}

// The vitals page says it is live, and each number shows which way it moved
// since the last reading.
func TestTUIVitalsShowsTrendsAndHistory(t *testing.T) {
	m, _ := statusModel(t)
	m, _ = openVitals(t, m)
	reading := func(cpu float64, freePct int, down float64) cli.StatusMsg {
		return cli.StatusMsg{Res: engine.StatusResult{Score: 90,
			CPU:     &probe.CPU{UsagePercent: cpu, Load1: 1, LogicalCores: 8},
			Network: &probe.NetworkRates{InBytesPerSec: down},
			Report:  probe.Report{Memory: probe.Memory{TotalBytes: 16 << 30, FreePercent: freePct, Pressure: probe.PressureNormal}}}}
	}
	m = step(t, m, reading(20, 60, 1e6))
	mustContain(t, m.View(), "Live", "updated just now", "every 3s", "▬") // one reading: no change to show yet
	m = step(t, m, reading(70, 40, 5e6), reading(30, 40, 2e6))
	out := m.View()
	mustContain(t, out, "CPU", "▼") // CPU fell from 70 to 30
	if strings.ContainsAny(out, "▁▂▃▄▅▆▇") {
		t.Error("there are no history bars any more")
	}
	if strings.Contains(out, "Live: updates every few seconds") {
		t.Error("the old footer line should be gone")
	}
	m = step(t, m, reading(90, 40, 2e6))
	mustContain(t, m.View(), "▲") // CPU rose again
}

// The vitals page explains how memory-slowing is decided: with the red alert
// it shows where pressure sits and what swap and free disk are against their
// limits, and the w key shows the same without an alert.
func TestTUIVitalsExplainsTheAlert(t *testing.T) {
	m, _ := statusModel(t)
	m, _ = openVitals(t, m)
	calm := cli.StatusMsg{Res: engine.StatusResult{Score: 90, Report: probe.Report{
		Swap: probe.Swap{UsedBytes: 1 << 30}, Disk: probe.Disk{ContainerTotalBytes: 500e9, ContainerFreeBytes: 200e9},
		Memory: probe.Memory{TotalBytes: 16 << 30, FreePercent: 50, Pressure: probe.PressureNormal}}}}
	m = step(t, m, calm)
	if strings.Contains(m.View(), "How macdoc decides") {
		t.Error("with no alert the explanation stays hidden until w")
	}
	m = step(t, m, keys("w")...)
	mustContain(t, m.View(), "How macdoc decides", "Normal", "Warn", "Critical", "▲ now", "Swap in use", "Free disk", "Right now: no alert.")
	m = step(t, m, keys("w")...)
	if strings.Contains(m.View(), "How macdoc decides") {
		t.Error("w again hides it")
	}

	tight := cli.StatusMsg{Res: engine.StatusResult{Score: 60, Report: probe.Report{
		Swap: probe.Swap{UsedBytes: 3 << 30}, Disk: probe.Disk{ContainerTotalBytes: 500e9, ContainerFreeBytes: 8e9},
		Memory: probe.Memory{TotalBytes: 16 << 30, FreePercent: 10, Pressure: probe.PressureWarn}}}}
	m = step(t, m, tight)
	mustContain(t, m.View(), "slowing your Mac down", "How macdoc decides", "high", "low", "(from 2.0 GB)", "(under 10.0 GB)", "Right now: the alert is on.")
}
