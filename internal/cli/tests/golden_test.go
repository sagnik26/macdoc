package tests

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/sagnik26/macdoc/internal/cli"
	"github.com/sagnik26/macdoc/internal/engine"
	"github.com/sagnik26/macdoc/internal/probe"
)

// Golden snapshots of what the dashboard draws. They are the safety net for the
// Charm v2 migration (docs/UI_MIGRATION_PLAN.md): the plain-layout files must
// stay byte-identical through Phase 1, and the color files change only when a
// phase says the look changes. Update them on purpose with
//
//	go test ./internal/cli/tests -run TestGolden -update-golden
var updateGolden = flag.Bool("update-golden", false, "rewrite internal/cli/tests/testdata/golden")

// goldenTheme is one look to snapshot.
type goldenTheme struct {
	name string
	make func(width int) *cli.Theme
}

// goldenThemes are the looks: the plain layout (boxes, no color) and the
// colored theme on a dark and a light background. The one place that builds a
// colored theme for the tests, so a library change edits this alone.
func goldenThemes() []goldenTheme {
	return []goldenTheme{
		{"plain", func(int) *cli.Theme { return cli.PlainLayoutTheme() }},
		{"dark", func(w int) *cli.Theme { return cli.ForcedColorTheme(true, w) }},
		{"light", func(w int) *cli.Theme { return cli.ForcedColorTheme(false, w) }},
	}
}

// goldenModel is the dashboard with fixed data on theme th, loaded.
func goldenModel(t *testing.T, th *cli.Theme, w, h int) cli.TuiModel {
	t.Helper()
	deps := &cli.TuiDeps{Home: "/Users/me",
		Load:     func(context.Context, *cli.TuiDeps, bool) (*cli.TuiData, error) { return tuiFixture(), nil },
		Software: func(context.Context, *cli.TuiDeps) (engine.SoftwareResult, error) { return softwareFixture(), nil },
		Status: func(context.Context, *cli.TuiDeps) engine.StatusResult {
			return engine.StatusResult{Score: 83, Reasons: []string{"-10 memory pressure is warn", "-7 something else"},
				CPU: &probe.CPU{UsagePercent: 42, Load1: 1.5, LogicalCores: 8}}
		}}
	m := cli.NewTUIModel(deps, th)
	return step(t, m, tea.WindowSizeMsg{Width: w, Height: h}, cli.LoadedMsg{Data: tuiFixture()})
}

// checkGolden compares got with testdata/golden/<name>.txt, or rewrites it.
func checkGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name+".txt")
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v (create it with -update-golden)", name, err)
	}
	if got != string(want) {
		t.Errorf("%s changed. If that is the intended look, run with -update-golden and review the diff.\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

// Every scan-view tab, at a narrow and a wide terminal, in each look.
func TestGoldenDashboardTabs(t *testing.T) {
	cli.NowFunc = func() time.Time { return planNow }
	t.Cleanup(func() { cli.NowFunc = time.Now })
	tabs := []string{"overview", "suggestions", "projects", "space"}
	for _, th := range goldenThemes() {
		for _, size := range [][2]int{{100, 40}, {140, 45}} {
			for i, tab := range tabs {
				m := goldenModel(t, th.make(size[0]), size[0], size[1])
				m = step(t, m, keys(fmt.Sprint(i+1))...)
				checkGolden(t, fmt.Sprintf("%s-%s-%d", th.name, tab, size[0]), m.Screen())
			}
		}
	}
}

// The welcome screen, help, the vitals page and the retire page, in each look.
func TestGoldenDashboardPages(t *testing.T) {
	cli.NowFunc = func() time.Time { return planNow }
	t.Cleanup(func() { cli.NowFunc = time.Now })
	for _, th := range goldenThemes() {
		w, h := 100, 40

		welcome := goldenModel(t, th.make(w), w, h)
		welcome.Intro, welcome.IntroStatus = true, cli.IntroStatus{HasScan: true, Age: "2h ago", Bytes: 128_000_000_000, Files: 1_031_292}
		checkGolden(t, th.name+"-welcome", welcome.Screen())

		help := step(t, goldenModel(t, th.make(w), w, h), keys("?")...)
		checkGolden(t, th.name+"-help", help.Screen())

		vit, cmd := openVitals(t, goldenModel(t, th.make(w), w, h))
		vit = deliverStatus(t, vit, cmd)
		checkGolden(t, th.name+"-vitals", vit.Screen())

		tight := cli.StatusMsg{Res: engine.StatusResult{Score: 60, Report: probe.Report{
			Swap: probe.Swap{UsedBytes: 3 << 30}, Disk: probe.Disk{ContainerTotalBytes: 500e9, ContainerFreeBytes: 8e9},
			Memory: probe.Memory{TotalBytes: 16 << 30, FreePercent: 10, Pressure: probe.PressureWarn}}}}
		checkGolden(t, th.name+"-vitals-alert", step(t, vit, tight).Screen())

		checkGolden(t, th.name+"-retire", openInstalled(t, goldenModel(t, th.make(w), w, h)).Screen())

		// The Overview on a tall screen, so its charts show whole.
		checkGolden(t, th.name+"-overview-tall", goldenModel(t, th.make(w), w, 80).Screen())
	}
}

// The printed (non-dashboard) reports in plain mode: what `--quiet`, a pipe or
// NO_COLOR gets. Phase 1 must leave these byte-identical.
func TestGoldenPrintedReports(t *testing.T) {
	wins, cat := sampleWins()
	data := tuiFixture()
	status := engine.StatusResult{Score: 83, Reasons: []string{"-10 memory pressure is warn", "-7 something else"},
		CPU: &probe.CPU{UsagePercent: 42, Load1: 1.5, LogicalCores: 8}}
	for name, render := range map[string]func(w *bytes.Buffer) error{
		"printed-reclaim":     func(w *bytes.Buffer) error { return cli.RenderWins(w, wins, cat, false, "") },
		"printed-reclaim-all": func(w *bytes.Buffer) error { return cli.RenderWins(w, wins, cat, true, "") },
		"printed-plan": func(w *bytes.Buffer) error {
			return cli.RenderPlan(w, samplePlan(), "/Users/me", planNow.Add(5*time.Minute))
		},
		"printed-retire":   func(w *bytes.Buffer) error { return cli.RenderSoftware(w, softwareFixture()) },
		"printed-vitals":   func(w *bytes.Buffer) error { return cli.RenderStatus(w, status) },
		"printed-overview": func(w *bytes.Buffer) error { return cli.RenderOverview(w, data.Res, cat) },
	} {
		var buf bytes.Buffer
		if err := render(&buf); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if bytes.ContainsRune(buf.Bytes(), 0x1b) {
			t.Errorf("%s: plain output contains an escape sequence", name)
		}
		checkGolden(t, name, buf.String())
	}
}

// A colored dashboard asks the terminal for its background and restyles when
// the answer differs from its guess; a plain one asks nothing.
func TestBackgroundColorRestylesTheDashboard(t *testing.T) {
	light := goldenModel(t, cli.ForcedColorTheme(false, 100), 100, 40)
	dark := goldenModel(t, cli.ForcedColorTheme(true, 100), 100, 40)
	if light.Screen() == dark.Screen() {
		t.Fatal("the light and dark looks should differ")
	}
	if light.Init() == nil {
		t.Error("a colored dashboard should ask the terminal for its background")
	}
	if plain := goldenModel(t, cli.PlainLayoutTheme(), 100, 40); plain.Init() != nil {
		t.Error("a plain dashboard must not ask the terminal anything")
	}
	got := step(t, light, tea.BackgroundColorMsg{Color: color.Black}) // a dark terminal
	if got.Screen() != dark.Screen() {
		t.Error("after a dark answer the screen should look like the dark theme")
	}
	same := step(t, dark, tea.BackgroundColorMsg{Color: color.Black})
	if same.Screen() != dark.Screen() {
		t.Error("an answer that agrees with the guess changed the screen")
	}
}

// The welcome screen's lettering is the six-row block "macdoc" (no cat, no
// larger wordmark): on the brand gradient in color, plain without color, and a
// compact title on a small terminal.
func TestWelcomeScreenKeepsTheBlockLogo(t *testing.T) {
	status := cli.IntroStatus{HasScan: true, Age: "2h ago", Bytes: 128_000_000_000, Files: 1_031_292}
	welcome := func(th *cli.Theme, w, h int) string {
		m := goldenModel(t, th, w, h)
		m.Intro, m.IntroStatus = true, status
		return m.Screen()
	}
	const blockM = "███╗   ███╗" // the "m" of the block logo
	for name, c := range map[string]struct {
		out       string
		wantBlock bool
	}{
		"color, wide and tall": {welcome(cli.ForcedColorTheme(true, 120), 120, 60), true},
		"plain layout":         {welcome(cli.PlainLayoutTheme(), 120, 60), true},
		"short":                {welcome(cli.ForcedColorTheme(true, 120), 120, 30), false},
	} {
		if strings.Contains(c.out, blockM) != c.wantBlock {
			t.Errorf("%s: block logo present = %v, want %v", name, !c.wantBlock, c.wantBlock)
		}
		if strings.ContainsAny(c.out, "▀▄") {
			t.Errorf("%s: there must be no pixel art (the cat is gone)", name)
		}
		mustContain(t, c.out, "Why is my Mac full and slow?")
	}
}

// The Vitals metric bars (CPU, memory, disk, power) keep their full height and
// take the design system's progress colors: "█" cells from #5A56E0 over a dim
// #606060 "░" track. Without color they stay the plain text bar.
func TestVitalsMetricBarsUseTheDesignSystem(t *testing.T) {
	vitals := func(th *cli.Theme) string {
		m, cmd := openVitals(t, goldenModel(t, th, 100, 40))
		return deliverStatus(t, m, cmd).Screen()
	}
	color := vitals(cli.ForcedColorTheme(true, 100))
	if !strings.Contains(color, "█") || !strings.Contains(color, "░") {
		t.Errorf("a colored vitals page should draw full-height bars:\n%s", color)
	}
	if strings.Contains(color, "▄") {
		t.Errorf("the vitals bars must not be shortened to half height")
	}
	if !strings.Contains(color, "38;2;90;86;224") { // #5A56E0, the start of the blend
		t.Errorf("the bar should start on the purple of the blend")
	}
	if !strings.Contains(color, "38;2;96;96;96") { // #606060, the empty track
		t.Errorf("the empty part of the bar should be the slate track")
	}
	plain := vitals(cli.PlainLayoutTheme())
	if !strings.Contains(plain, "████") || strings.Contains(plain, "▄") || strings.Contains(plain, "\x1b") {
		t.Errorf("without color the bar stays the plain text bar:\n%s", plain)
	}
}
