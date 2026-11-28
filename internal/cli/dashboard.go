package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/spf13/cobra"

	"github.com/sagnik26/macdoc/internal/engine"
	"github.com/sagnik26/macdoc/internal/executor"
	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/planner"
	"github.com/sagnik26/macdoc/internal/recipes"
	"github.com/sagnik26/macdoc/internal/scanner"
	"github.com/sagnik26/macdoc/internal/store"
)

// The interactive dashboard behind bare `macdoc` in a terminal. It is
// read-only like every other view: it can scan and show, never change.

type tab int

// The dashboard's tabs, in display order.
const (
	tabOverview tab = iota
	TabSuggestions
	tabProjects
	TabSpace
)

// The tabs belong to the scan view only. Every other command (reclaim, apply,
// vitals, explore, retire) opens its own page without them.
var tabTitles = []string{"Overview", "Suggestions", "Projects", "Space"}

// slashCommand is one entry of the / palette.
type slashCommand struct {
	Name, args, desc string
}

// SlashCommands is the / palette, in display order. It lists the same
// commands, in the same order, as the welcome screen (introActions).
var SlashCommands = []slashCommand{
	{"scan", "", "scan the disk again, then show the full report (esc stops a scan)"},
	{"reclaim", "", "scan, then clean every SAFE and CAUTION item at once (esc stops it)"},
	{"retire", "", "installed software and its data folders; delete what you don't need"},
	{"explore", "", "where the space is, as a folder tree you can open"},
	{"vitals", "", "live CPU, memory, disk, network and battery, with a health score"},
}

// stepLabels describe the loading stages.
var stepLabels = map[engine.OverviewStep]string{
	engine.StepHealth: "Checking disk, memory and swap",
	engine.StepScan:   "Scanning your Mac",
	engine.StepLoad:   "Reading the latest scan",
	engine.StepRank:   "Ranking cleanup suggestions",
	stepSpace:         `Decoding "System Data"`,
	stepProjects:      "Looking at your projects",
}

const (
	stepSpace    engine.OverviewStep = "space"
	stepProjects engine.OverviewStep = "projects"
)

// Messages the dashboard's background commands send to Update.
type (
	// StepMsg says which stage of loading has started.
	StepMsg engine.OverviewStep
	// ProgressMsg is a periodic scan update.
	ProgressMsg scanner.Progress
	// LoadedMsg carries the loaded data, or the error that stopped loading.
	LoadedMsg struct {
		Data *TuiData
		Err  error
	}
)

// TuiData is everything the dashboard shows.
type TuiData struct {
	Res      engine.OverviewResult
	Space    engine.StorageResult
	Projects engine.ProjectsResult
	Slow     engine.SlowResult
	Trend    *engine.TrendResult // nil when there's nothing to compare yet
	trendMsg string              // why trend is nil
	// planOps are the opportunities as the engine returned them, passed to
	// CreatePlan as Precomputed. Everything above is a display copy, cleaned
	// for the terminal (display), and must never be planned from.
	PlanOps *engine.OpportunityResult
}

// TuiDeps are shared by every copy of the model.
type TuiDeps struct {
	Home string
	cat  *recipes.Catalog
	// AppDirs is where Retire looks for an app's bundle; nil means /Applications and
	// ~/Applications. Tests set it.
	AppDirs []string
	// retire is cat plus the one entry a Retire deletion runs under, while that
	// deletion is open (see retire.go); nil otherwise.
	retire *recipes.Catalog
	send   func(tea.Msg) // set once the program exists; nil in tests
	// load and makePlan replace the engine in tests.
	Load     func(ctx context.Context, d *TuiDeps, rescan bool) (*TuiData, error)
	MakePlan func(d *TuiDeps, sel planner.Selector, pre *engine.OpportunityResult) (engine.PlanResult, error)
	// apply replaces RunApply in tests.
	apply func(cmd *cobra.Command, d ApplyDeps, id string) error
	// remove replaces executorRemove in tests.
	Remove func(d *TuiDeps, p model.Plan, onItem func(executor.Event)) (executor.Report, error)
	// Measure walks one suggestion's folder for the range it frees; the
	// dashboard starts no walk while it is nil. Tests replace it.
	Measure func(ctx context.Context, d *TuiDeps, op model.Opportunity) (*engine.Span, error)
	// Software reads the software inventory; nil uses the engine. Tests replace it.
	Software func(ctx context.Context, d *TuiDeps) (engine.SoftwareResult, error)
	// Explore reads a folder of the latest scan for the explorer; nil uses the engine.
	Explore func(ctx context.Context, d *TuiDeps, path string) (engine.ExploreResult, error)
	// Status takes a live reading for /vitals; nil uses the engine.
	Status func(ctx context.Context, d *TuiDeps) engine.StatusResult

	// removeCtx lets esc stop a /reclaim run between items; nil means no way to stop.
	removeCtx context.Context
}

// enginePlan seals a plan with the real engine, reusing the dashboard's
// opportunities when they are from the latest scan.
func enginePlan(d *TuiDeps, sel planner.Selector, pre *engine.OpportunityResult) (engine.PlanResult, error) {
	return engine.CreatePlan(context.Background(), engine.PlanOptions{
		OpportunityOptions: engine.OpportunityOptions{Home: d.Home, Runner: Runner, Catalog: d.catalog()},
		Selector:           sel,
		Precomputed:        pre,
	})
}

// SetSend sets where background work posts its progress messages. runTUI sets
// it to the program's Send; tests set it to feed the model directly.
func (d *TuiDeps) SetSend(send func(tea.Msg)) { d.send = send }

func (d *TuiDeps) post(msg tea.Msg) {
	if d.send != nil {
		d.send(msg)
	}
}

type loadStep struct {
	step  engine.OverviewStep
	start time.Time
	took  time.Duration
	done  bool
}

type keyMap struct {
	Tabs, Move, Explain, Back, Scan, Slash, Help, Quit, Stop key.Binding
	Run, Complete, Cancel, Mark, Remove, Hide                key.Binding
}

func newKeyMap() keyMap {
	return keyMap{
		Tabs:     key.NewBinding(key.WithKeys("tab", "shift+tab"), key.WithHelp("tab", "switch view")),
		Move:     key.NewBinding(key.WithKeys("up", "down", "j", "k"), key.WithHelp("↑↓", "move")),
		Explain:  key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "explain")),
		Remove:   key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "remove")),
		Back:     key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
		Scan:     key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "rescan")),
		Slash:    key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "commands")),
		Help:     key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		Quit:     key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
		Stop:     key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "stop")),
		Run:      key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "run")),
		Complete: key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "complete")),
		Cancel:   key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel")),
		Mark:     key.NewBinding(key.WithKeys(" "), key.WithHelp("space", "mark")),
		Hide:     key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "hide this kind")),
	}
}

type overlay int

// The overlays that can cover the dashboard.
const (
	OverlayNone overlay = iota
	overlayHelp
	overlaySoftware
	overlayExplore
	overlayVitals
	OverlayRemove
)

// TuiModel is the dashboard's Bubble Tea model.
type TuiModel struct {
	Deps *TuiDeps
	t    *Theme
	keys keyMap
	help help.Model

	w, h int
	Tab  tab

	// loading
	Loading  bool
	Stopping bool // Esc pressed; waiting for the engine to return
	Quitting bool // quit once loading returns
	steps    []loadStep
	prog     scanner.Progress
	estimate int64 // bytes the scan is expected to reach (last scan), 0 = unknown
	spin     spinner.Model
	Bar      progress.Model
	cancel   context.CancelFunc

	Data *TuiData
	note string // empty-state or error text when there is no data

	// suggestions
	Cursor int
	Detail bool // narrow screens: the detail replaces the list

	// plans: marked suggestion IDs (shared by copies of the model, cleared
	// on every new load), the plan being sealed, and the last one made
	Marked   map[string]bool
	span     *spanState // the range walk and its results, shared by copies
	Planning bool
	// software: the last inventory shown in the /retire overlay
	software        *engine.SoftwareResult
	softwareBusy    bool
	softwareDone    int    // discovery sources finished so far
	softwareTotal   int    // how many there are (0 until the first report)
	softwareSource  string // the one that finished last
	softwareErr     error
	softwareStarted time.Time
	softwareFilter  softwareFilter
	softwareCursor  int                   // the highlighted row of the filtered list
	explore         *engine.ExploreResult // the folder shown in the explorer
	reclaimAuto     bool                  // the removal being sealed is /reclaim's: it starts as soon as it is ready
	treeRoot        *treeNode             // the folder tree shown in /explore
	treeCursor      int                   // the highlighted row of that tree
	status          *engine.StatusResult  // the last live reading, shown by /vitals
	statusBusy      bool                  // a live reading is running
	statusDone      int                   // probes of the first reading that have answered
	statusTotal     int                   // how many there are (0 until the first report)
	statusProbe     string                // the one that answered last
	statusOpened    time.Time             // when the vitals page was opened
	statusShown     float64               // how full the first-reading bar is drawn (0..1); it eases toward the real progress
	statusGen       int                   // which opening of the page the bar animation belongs to
	statusWait      bool                  // a reading was just taken; the next waits for the tick
	statusAt        time.Time             // when the last reading finished
	hist            vitalsHist            // the recent readings behind the arrows
	clockOn         bool                  // the once-a-second clock for "updated Ns ago" is running
	reclaimFresh    bool                  // /reclaim's own rescan just finished
	pending         string                // a command waiting for the latest scan to load
	removing        *removeState          // the remove confirmation box, while open

	scroll  int
	Overlay overlay

	input     textinput.Model
	InputMode rune // 0 or '/'
	pick      int  // highlighted completion
	Flash     string

	InitCmd tea.Cmd

	// welcome screen
	Intro       bool
	introPick   int
	IntroStatus IntroStatus
}

// NewTUIModel returns a dashboard model that has not started loading.
func NewTUIModel(deps *TuiDeps, t *Theme) TuiModel {
	in := textinput.New()
	in.Prompt = ""
	in.CharLimit = 64
	in.SetVirtualCursor(true) // the input is drawn inside the dashboard's own view
	m := TuiModel{Deps: deps, keys: newKeyMap(), spin: spinner.New(spinner.WithSpinner(spinner.Dot)),
		input: in, w: 100, h: 30, Marked: map[string]bool{}, span: newSpanState()}
	m.applyTheme(t)
	return m
}

// applyTheme gives the model and its Bubbles components the colors of t. It
// runs when the model is built and again if the terminal reports a different
// background color. The components keep the look they have in the Bubbles
// README: the dot spinner in ANSI 205, the half-block progress bar with the
// default purple-to-pink blend, the default help and input styles.
func (m *TuiModel) applyTheme(t *Theme) {
	m.t = t
	m.spin.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))
	m.Bar = progress.New(progress.WithDefaultBlend(), progress.WithFillCharacters(barChar, barEmpty))
	m.Bar.SetWidth(barWidth(m.w))
	m.input.SetStyles(textinput.DefaultStyles(t.dark))
	h := help.New()
	h.Styles = help.DefaultStyles(t.dark)
	m.help = h
}

// barWidth is the progress bar's width, including its percentage: the terminal
// less a margin, at most 80 cells, as in the Bubbles progress example.
func barWidth(termWidth int) int { return min(80, max(20, termWidth-12)) }

// barView draws the progress bar at pct, with its percentage unless the total
// is unknown.
func (m TuiModel) barView(pct float64, showPct bool) string {
	b := m.Bar
	b.ShowPercentage = showPct
	return b.ViewAs(pct)
}

// runTUI starts the dashboard on the terminal.
func runTUI(home string, cat *recipes.Catalog) error {
	t := newTheme(os.Stdout)
	if !t.on {
		// NO_COLOR or a monochrome terminal: keep the layout, drop colors.
		t = PlainLayoutTheme()
	}
	deps := &TuiDeps{Home: home, cat: cat, Load: engineLoad, MakePlan: enginePlan, Measure: engineMeasure}
	m := NewTUIModel(deps, t)
	// Open on the welcome screen; nothing runs until the user picks.
	m.Intro, m.IntroStatus = true, loadIntroStatus(home)
	p := tea.NewProgram(m)
	deps.send = p.Send
	final, err := p.Run()
	if err != nil {
		return err
	}
	if fm, ok := final.(TuiModel); ok {
		fm.span.stop() // leave no walk running
	}
	if fm, ok := final.(TuiModel); ok && fm.Data != nil {
		fmt.Println(t.done("Nothing has been changed."))
	}
	return nil
}

// PlainLayoutTheme draws boxes and badges without color.
func PlainLayoutTheme() *Theme {
	t := PlainTheme()
	t.on = true
	return t
}

// engineLoad is the real loader: overview, then space and toolchains.
// background is the step callback that lowers priority when the dashboard's
// load starts a scan. Its warnings would draw over the screen, so it stays quiet.
var background = Background(scanner.SetBackgroundPriority, io.Discard)

func engineLoad(ctx context.Context, d *TuiDeps, rescan bool) (*TuiData, error) {
	res, err := engine.Overview(ctx, engine.OverviewOptions{
		Home: d.Home, Runner: Runner, Version: version, Rescan: rescan, Catalog: d.cat, ScanRoot: ScanRoot,
		Step: func(s engine.OverviewStep) {
			background(s) // only a scan is slowed down on purpose
			d.post(StepMsg(s))
		},
		Progress: func(p scanner.Progress) { d.post(ProgressMsg(p)) },
	})
	if err != nil {
		return nil, err
	}
	d.post(StepMsg(stepSpace))
	space, err := engine.Storage(ctx, engine.StorageOptions{
		OpportunityOptions: engine.OpportunityOptions{Home: d.Home, Runner: Runner, Catalog: d.cat},
		Large:              true,
		Precomputed:        &res.Opportunities,
	})
	if err != nil {
		return nil, err
	}
	d.post(StepMsg(stepProjects))
	projs, err := engine.Projects(ctx, engine.OpportunityOptions{Home: d.Home, Runner: Runner, Catalog: d.cat})
	if err != nil {
		return nil, err
	}
	data := &TuiData{Res: res, Space: space, Projects: FilterProjects(projs, 0),
		Slow: engine.SlowFrom(res.Health, engine.SlowOptions{Home: d.Home})}
	data.Trend, data.trendMsg = loadTrend(d.Home)
	return data, nil
}

// loadTrend compares the latest scan with one about 30 days old. Not having
// two scans yet isn't an error: the view says so.
func loadTrend(home string) (*engine.TrendResult, string) {
	tr, err := engine.Trend(engine.TrendOptions{Home: home, Days: 30})
	switch {
	case errors.Is(err, engine.ErrNoHistory), errors.Is(err, store.ErrNoScan):
		return nil, "Trends need two scans. Rescan (r) in a few days to see what grew."
	case err != nil:
		return nil, "Couldn't compare scans: " + err.Error()
	}
	return &tr, ""
}

// Init implements tea.Model. A colored theme also asks the terminal for its
// background color, so the palette follows a light or dark terminal even if the
// first guess was wrong (MACDOC_THEME overrides that).
func (m TuiModel) Init() tea.Cmd {
	if m.t.color && os.Getenv("MACDOC_THEME") == "" {
		return tea.Batch(m.InitCmd, tea.RequestBackgroundColor)
	}
	return m.InitCmd
}

// StartLoad puts m into the loading state and returns the command that runs
// the engine. Call it on the model that Update returns (or before the program
// starts, for the first load).
func (m *TuiModel) StartLoad(rescan bool) tea.Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.Loading, m.Stopping = true, false
	m.steps, m.prog = nil, scanner.Progress{}
	m.estimate = m.expectedScanBytes()
	deps := m.Deps
	run := func() tea.Msg {
		data, err := deps.Load(ctx, deps, rescan)
		return LoadedMsg{Data: data, Err: err}
	}
	return tea.Batch(run, m.spin.Tick)
}

// Update implements tea.Model. After every message it makes the range walk
// match what the detail pane shows (syncSpan).
func (m TuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if sm, ok := msg.(SpanMsg); ok {
		m.spanned(sm)
		return m, nil
	}
	next, cmd := m.update(msg)
	if nm, ok := next.(TuiModel); ok {
		afterSpan, cmd := nm.syncSpan(cmd)
		if nm, ok := afterSpan.(TuiModel); ok {
			return nm.syncStatus(cmd)
		}
		return afterSpan, cmd
	}
	return next, cmd
}

func (m TuiModel) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.t.width = msg.Width
		m.Bar.SetWidth(barWidth(msg.Width))
		return m, nil

	case tea.BackgroundColorMsg:
		// The terminal answered: restyle if its background is not the one the
		// theme was built for.
		if m.t.color && msg.IsDark() != m.t.dark {
			m.applyTheme(ColorTheme(msg.IsDark(), m.t.profile, m.t.width))
		}
		return m, nil

	case StepMsg:
		now := time.Now()
		for i := range m.steps {
			if !m.steps[i].done {
				m.steps[i].done, m.steps[i].took = true, now.Sub(m.steps[i].start)
			}
		}
		m.steps = append(m.steps, loadStep{step: engine.OverviewStep(msg), start: now})
		return m, nil

	case ProgressMsg:
		m.prog = scanner.Progress(msg)
		return m, nil

	case LoadedMsg:
		return m.loaded(msg)

	case StatusProgressMsg:
		m.statusDone, m.statusTotal, m.statusProbe = msg.Done, msg.Total, msg.Probe
		return m, nil

	case SoftwareProgressMsg:
		m.softwareDone, m.softwareTotal, m.softwareSource = msg.Done, msg.Total, msg.Source
		return m, nil

	case SoftwareMsg:
		return m.softwareLoaded(msg)

	case StatusMsg:
		return m.statusRead(msg)

	case statusTickMsg:
		m.statusWait = false
		return m, nil

	case statusClockMsg:
		return m.clockTick()

	case statusBarMsg:
		return m.barTick(msg)

	case removeReadyMsg:
		return m.removeReady(msg)

	case removeProgressMsg:
		return m.removeProgress(msg)

	case removedMsg:
		return m.removed(msg)

	case spinner.TickMsg:
		return m.tick(msg)

	case tea.KeyPressMsg:
		m.Flash = ""
		if m.InputMode != 0 {
			return m.updateInput(msg)
		}
		return m.updateKey(msg)
	}
	return m, nil
}

func (m TuiModel) loaded(msg LoadedMsg) (tea.Model, tea.Cmd) {
	m.Loading, m.cancel = false, nil
	if m.Quitting {
		return m, tea.Quit
	}
	// A stopped scan can still return (partial) results; they are never shown.
	switch {
	case msg.Err == nil && !m.Stopping:
		m.Data, m.note = cleanData(msg.Data), ""
		clear(m.Marked) // IDs belong to a scan
		m.span.reset()
		m.Cursor = min(m.Cursor, max(0, len(m.Items())-1))
	case m.Stopping || errors.Is(msg.Err, context.Canceled):
		if m.Data != nil {
			m.Flash = "Scan stopped. Showing the previous results."
		} else {
			m.note = "Scan stopped before it finished. Press r (or /scan) to scan again."
		}
	default:
		if m.Data != nil {
			m.Flash = "Error: " + msg.Err.Error()
		} else {
			m.note = msg.Err.Error()
		}
	}
	stopped := m.Stopping
	m.Stopping = false
	if p := m.pending; p != "" {
		m.pending = ""
		if m.Data != nil {
			m.reclaimFresh = msg.Err == nil && !stopped && strings.HasPrefix(strings.TrimLeft(p, "/"), "reclaim")
			return m.RunCommand(p)
		}
		if msg.Err != nil {
			m.Flash = "Couldn't read your Mac: " + msg.Err.Error()
		}
	}
	return m, nil
}

// loadThen reads the latest scan (scanning only when there is none) and then
// runs line, so a command that needs it starts by itself instead of asking
// the person to check the Mac first. The welcome screen stays underneath, so
// closing the command's page comes back to it.
func (m TuiModel) loadThen(line string) (tea.Model, tea.Cmd) {
	if m.Loading {
		return m, nil
	}
	m.pending = line
	cmd := m.StartLoad(false)
	return m, cmd
}

// rescanThen scans afresh and then runs line. Only the scan view reuses a
// stored scan; /reclaim cleans from what is on disk now.
func (m TuiModel) rescanThen(line string) (tea.Model, tea.Cmd) {
	if m.Loading {
		return m, nil
	}
	m.pending = line
	cmd := m.StartLoad(true)
	return m, cmd
}

func (m TuiModel) updateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	k := msg.String()
	if m.Overlay == OverlayRemove && m.removing != nil {
		if k == "ctrl+c" && !m.removing.running {
			return m, tea.Quit
		}
		return m.removeKey(msg)
	}
	if k == "ctrl+c" || (k == "q" && m.Overlay == OverlayNone) {
		if m.Loading && m.cancel != nil {
			if m.Quitting { // second press: leave now
				return m, tea.Quit
			}
			m.Quitting = true
			m.cancel()
			return m, nil
		}
		return m, tea.Quit
	}
	if m.Loading {
		if k == "esc" && m.cancel != nil {
			m.Stopping = true
			m.cancel()
		}
		return m, nil
	}

	if m.Overlay == overlaySoftware {
		if next, cmd, ok := m.softwareKey(k); ok {
			return next, cmd
		}
	}
	if m.Overlay == overlayExplore {
		if next, cmd, ok := m.exploreKey(k); ok {
			return next, cmd
		}
	}
	if m.Overlay != OverlayNone {
		switch k {
		case "esc", "q", "?", "enter":
			m.Overlay, m.scroll = OverlayNone, 0
		default:
			m.scrollBy(k)
		}
		return m, nil
	}
	if m.Intro {
		return m.updateIntroKey(k)
	}

	switch k {
	case "esc":
		if m.Detail {
			m.Detail = false
			break
		}
		return m.goHome()
	case "?":
		m.Overlay, m.scroll = overlayHelp, 0
	case "/":
		m.InputMode, m.pick = rune(k[0]), 0
		m.input.SetValue("")
		m.input.Focus()
		return m, textinput.Blink
	case "tab", "right", "l":
		m.setTab((m.Tab + 1) % tab(len(tabTitles)))
	case "shift+tab", "left", "h":
		m.setTab((m.Tab + tab(len(tabTitles)) - 1) % tab(len(tabTitles)))
	case "1", "2", "3", "4":
		m.setTab(tab(k[0] - '1'))
	case "r":
		return m, m.StartLoad(true)
	case "enter":
		if m.Tab == TabSpace {
			return m.openExplore("")
		}
		if m.Tab == TabSuggestions && len(m.Items()) > 0 {
			if m.Detail {
				m.Detail = false
				break
			}
			return m.startRemove()
		}
	case "e":
		if m.Tab == TabSuggestions && len(m.Items()) > 0 {
			m.Detail = !m.Detail
		}
	case "space":
		if m.Tab == TabSuggestions {
			m.toggleMark()
		}
	case "x":
		if m.Tab == TabSuggestions && !m.Detail {
			return m.hideCurrent()
		}
	default:
		if m.Tab == TabSuggestions && !m.Detail {
			m.moveCursor(k)
		} else {
			m.scrollBy(k)
		}
	}
	return m, nil
}

func (m *TuiModel) setTab(t tab) {
	m.Tab, m.scroll, m.Detail = t, 0, false
}

func (m *TuiModel) moveCursor(k string) {
	n := len(m.Items())
	switch k {
	case "up", "k":
		m.Cursor--
	case "down", "j":
		m.Cursor++
	case "pgup":
		m.Cursor -= 10
	case "pgdown", " ":
		m.Cursor += 10
	case "home", "g":
		m.Cursor = 0
	case "end", "G":
		m.Cursor = n - 1
	}
	m.Cursor = max(0, min(m.Cursor, n-1))
}

func (m *TuiModel) scrollBy(k string) {
	switch k {
	case "up", "k":
		m.scroll--
	case "down", "j":
		m.scroll++
	case "pgup":
		m.scroll -= m.bodyHeight() - 1
	case "pgdown", " ":
		m.scroll += m.bodyHeight() - 1
	case "home", "g":
		m.scroll = 0
	case "end", "G":
		m.scroll = 1 << 30
	}
	m.scroll = max(0, m.scroll)
}

// --- command input (/) ---

// completion is one row of the palette.
type completion struct{ value, desc string }

func (m TuiModel) completions() []completion {
	q := strings.ToLower(strings.TrimSpace(m.input.Value()))
	var out []completion
	name, _, _ := strings.Cut(q, " ")
	for _, c := range SlashCommands {
		if strings.HasPrefix(c.Name, name) {
			v := c.Name
			if c.args != "" {
				v += " " + c.args
			}
			out = append(out, completion{v, c.desc})
		}
	}
	return out
}

func (m TuiModel) updateInput(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	comps := m.completions()
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.InputMode = 0
		m.input.Blur()
		return m, nil
	case "up":
		m.pick = max(0, m.pick-1)
		return m, nil
	case "down":
		m.pick = min(len(comps)-1, m.pick+1)
		return m, nil
	case "tab":
		if len(comps) > 0 {
			v, _, _ := strings.Cut(comps[min(m.pick, len(comps)-1)].value, " <")
			if strings.Contains(comps[m.pick].value, "<") {
				v += " "
			}
			m.input.SetValue(v)
			m.input.CursorEnd()
		}
		return m, nil
	case "enter":
		v := strings.TrimSpace(m.input.Value())
		// Enter on a partial word runs the highlighted completion.
		if len(comps) > 0 && !strings.Contains(v, " ") {
			if c, _, _ := strings.Cut(comps[min(m.pick, len(comps)-1)].value, " "); c != v {
				v = c
			}
		}
		m.InputMode = 0
		m.input.Blur()
		return m.RunCommand(v)
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.pick = 0
	return m, cmd
}

// RunCommand runs one / palette command line.
func (m TuiModel) RunCommand(line string) (tea.Model, tea.Cmd) {
	name, arg, _ := strings.Cut(strings.TrimPrefix(line, "/"), " ")
	arg = strings.TrimSpace(arg)
	if name == "overview" { // typed only: the 1 key does the same
		m.setTab(tabOverview)
		if m.Intro { // the scan view starts the dashboard from the welcome screen
			return m, m.leaveIntro(false)
		}
		return m, nil
	}
	switch name {
	case "":
	case "vitals":
		return m.openVitals()
	case "reclaim":
		if m.reclaimFresh { // the scan it asked for has just finished
			m.reclaimFresh = false
			return m.autoReclaim()
		}
		return m.rescanThen(line) // reclaim never works from a stored scan
	case "scan":
		if m.Loading {
			return m, nil
		}
		m.Intro = false
		return m, m.StartLoad(true)
	case "explore":
		return m.openExplore(arg)
	case "retire":
		if m.Data == nil {
			return m.loadThen(line)
		}
		return m.startSoftware()
	case "help":
		m.Overlay, m.scroll = overlayHelp, 0
	case "quit", "exit", "q":
		return m, tea.Quit
	default:
		m.Flash = fmt.Sprintf("Unknown command /%s. Type / to see the list.", name)
	}
	return m, nil
}

// Items are the suggestions, in ranked order.
func (m TuiModel) Items() []model.Opportunity {
	if m.Data == nil {
		return nil
	}
	return m.Data.Res.Opportunities.Opportunities
}

// --- view ---

func (m TuiModel) bodyHeight() int {
	h := m.h - 4 // header, tabs, rule, footer
	if m.pageTitle() != "" {
		h = m.h - 3 // header, rule, footer: a page has no tabs
	}
	if m.InputMode != 0 {
		start, end := m.paletteWindow()
		h -= paletteChrome + end - start
	} else if m.Flash != "" {
		h--
	}
	return max(3, h)
}

// View implements tea.Model.
// View implements tea.Model: the screen, on the alternate screen.
func (m TuiModel) View() tea.View {
	v := tea.NewView(m.Screen())
	v.AltScreen = true
	return v
}

// Screen is what the dashboard shows, as a string. A theme without color
// carries no escape sequence at all, so the plain layout stays plain.
func (m TuiModel) Screen() string {
	s := m.screen()
	if !m.t.color {
		s = ansi.Strip(s)
	}
	return s
}

func (m TuiModel) screen() string {
	var b strings.Builder
	if m.Intro && m.Overlay == OverlayNone && !m.Loading {
		b.WriteString(fitHeight(m.introView(), m.introHeight(), m.w))
		if m.InputMode != 0 {
			b.WriteString(m.inputView())
		} else if m.Flash != "" {
			b.WriteString(m.flashLine())
		}
		if m.InputMode != 0 {
			b.WriteString(m.footerView())
		} else {
			b.WriteString(m.introFooter())
		}
		return b.String()
	}
	if page := m.pageTitle(); page != "" {
		// Every command except the scan view is a page of its own: its title,
		// no tabs and nothing about the scan.
		b.WriteString(m.pageHeader(page) + "\n")
	} else {
		b.WriteString(m.headerView() + "\n")
		b.WriteString(m.tabsView() + "\n")
	}

	b.WriteString(fitHeight(m.bodyView(), m.bodyHeight(), m.w))

	if m.InputMode != 0 {
		b.WriteString(m.inputView())
	} else if m.Flash != "" {
		b.WriteString(m.flashLine())
	}
	b.WriteString(m.footerView())
	return b.String()
}

// bodyView is the page's body: the loading view, an overlay, or the tab.
func (m TuiModel) bodyView() string {
	t := m.t
	switch {
	case m.Loading:
		return m.loadingView()
	case m.Overlay == overlayHelp:
		return m.scrolled(m.HelpView())
	case m.Overlay == overlayExplore:
		return m.scrolled(m.exploreBody())
	case m.Overlay == overlayVitals:
		return m.scrolled(m.vitalsBody())
	case m.Overlay == overlaySoftware:
		return m.scrolled(m.softwareTables())
	case m.Overlay == OverlayRemove:
		return m.scrolled(m.removeView())
	case m.Data == nil:
		return "\n  " + t.muted.Render(display(m.note))
	case m.Tab == TabSuggestions:
		return m.suggestionsView()
	default:
		return m.scrolled(m.textView())
	}
}

// flashLine is the one-line message under the body.
func (m TuiModel) flashLine() string {
	return " " + m.t.warn.Render("›") + " " + truncateANSI(m.t.text.Render(display(m.Flash)), m.w-4) + "\n"
}

// fitHeight pads or cuts s to exactly h lines, each at most w cells wide.
func fitHeight(s string, h, w int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > h {
		lines = lines[:h]
	}
	for len(lines) < h {
		lines = append(lines, "")
	}
	for i, l := range lines {
		lines[i] = truncateANSI(l, w)
	}
	return strings.Join(lines, "\n") + "\n"
}

// scrolled applies the scroll offset to a text body, through a Bubbles
// viewport: the offset is kept within the body, and the part that fits is shown.
func (m *TuiModel) scrolled(s string) string {
	vp := viewport.New(viewport.WithWidth(m.w), viewport.WithHeight(m.bodyHeight()))
	vp.SetContent(strings.TrimRight(s, "\n"))
	vp.SetYOffset(m.scroll)
	m.scroll = vp.YOffset()
	return vp.View()
}

func (m TuiModel) headerView() string {
	t := m.t
	left := " " + t.brand.Render("◆ macdoc")
	var right []string
	if m.Data != nil {
		hw := m.Data.Res.Health.Report.Hardware
		if hw.Model != "" {
			left += t.muted.Render("  " + hw.Model + " · " + strings.TrimPrefix(hw.Chip, "Apple "))
		}
		if d := m.Data.Res.Health.Report.Disk; d.ContainerTotalBytes > 0 {
			used := d.ContainerTotalBytes - d.ContainerFreeBytes
			right = append(right, t.usageStyle(used, d.ContainerTotalBytes).Render(engine.DecimalBytes(d.ContainerFreeBytes)+" free"))
		}
		age := "scan " + m.Data.Res.ScanAge
		if m.Data.Res.Scanned {
			age = "scanned just now"
		}
		st := t.muted
		if m.Data.Res.ScanStale {
			st = t.warn
		}
		right = append(right, st.Render(age))
	}
	r := strings.Join(right, t.faint.Render(" · ")) + " "
	gap := max(1, m.w-lipgloss.Width(left)-lipgloss.Width(r))
	return left + strings.Repeat(" ", gap) + r
}

// pageTitle names the page the person is on, or "" in the scan view, which is
// the only one with tabs.
func (m TuiModel) pageTitle() string {
	titles := map[overlay]string{
		overlayHelp: "Help", overlaySoftware: "Retire", overlayExplore: "Explore",
		overlayVitals: "Vitals",
		OverlayRemove: "Reclaim",
	}
	if m.Overlay == OverlayRemove && m.removing != nil && m.removing.retire != "" {
		return "Retire" // a deletion started from the Retire list
	}
	if t, ok := titles[m.Overlay]; ok {
		return t
	}
	if m.Loading && m.pending != "" {
		name, _, _ := strings.Cut(strings.TrimPrefix(m.pending, "/"), " ")
		return strings.ToUpper(name[:1]) + name[1:]
	}
	return ""
}

// pageHeader is the top of a page: the brand, the page's title and a rule.
func (m TuiModel) pageHeader(title string) string {
	t := m.t
	left := " " + t.brand.Render("◆ macdoc") + t.faint.Render("  ›  ") + t.chip(title)
	return left + "\n" + t.gradientRule(m.w)
}

func (m TuiModel) tabsView() string {
	t := m.t
	var parts []string
	for i, title := range tabTitles {
		label := fmt.Sprintf("%d %s", i+1, title)
		if tab(i) == m.Tab {
			parts = append(parts, t.style().Background(t.c(colBrand)).Foreground(t.c(colOnBadge)).Bold(true).Padding(0, 1).Render(label))
		} else {
			parts = append(parts, t.muted.Padding(0, 1).Render(label))
		}
	}
	row := " " + strings.Join(parts, " ")
	return row + "\n" + t.rule(m.w)
}

func (m TuiModel) footerView() string {
	var keys []key.Binding
	switch {
	case m.InputMode != 0:
		// /help and /quit are not listed in the menu; they are shown here and
		// still work when typed.
		keys = []key.Binding{m.keys.Run, m.keys.Complete, m.keys.Move, m.keys.Cancel,
			key.NewBinding(key.WithKeys("/help"), key.WithHelp("/help", "help")),
			key.NewBinding(key.WithKeys("/quit"), key.WithHelp("/quit", "quit"))}
	case m.Loading:
		keys = []key.Binding{m.keys.Stop, m.keys.Quit}
	case m.Overlay == OverlayRemove:
		keys = nil
	case m.Overlay == overlayExplore:
		keys = []key.Binding{
			key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter/→", "open")),
			key.NewBinding(key.WithKeys("left"), key.WithHelp("←", "close")),
			key.NewBinding(key.WithKeys("backspace"), key.WithHelp("backspace", "parent folder")),
			m.keys.Move, m.keys.Back, m.keys.Quit,
		}
	case m.Overlay == overlayVitals:
		keys = []key.Binding{m.keys.Move, m.keys.Back, m.keys.Quit}
	case m.Overlay == overlaySoftware && m.softwareBusy:
		keys = []key.Binding{m.keys.Back, m.keys.Quit}
	case m.Overlay == overlaySoftware:
		keys = []key.Binding{
			key.NewBinding(key.WithKeys("f"), key.WithHelp("f", "filter")),
			key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "delete")),
			m.keys.Move, m.keys.Back, m.keys.Quit,
		}
	case m.Overlay != OverlayNone:
		keys = []key.Binding{m.keys.Move, m.keys.Back, m.keys.Quit}
	case m.Tab == TabSpace:
		keys = []key.Binding{key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "explore folders")), m.keys.Tabs, m.keys.Move, m.keys.Slash, m.keys.Scan, m.keys.Help, key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "home")), m.keys.Quit}
	case m.Tab == TabSuggestions && m.Detail:
		keys = []key.Binding{m.keys.Back, m.keys.Tabs, m.keys.Slash, m.keys.Help, m.keys.Quit}
	case m.Tab == TabSuggestions:
		keys = []key.Binding{m.keys.Remove, m.keys.Mark, m.keys.Hide, m.keys.Explain, m.keys.Move, m.keys.Tabs, m.keys.Slash, m.keys.Scan, m.keys.Help, key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "home")), m.keys.Quit}
	default:
		keys = []key.Binding{m.keys.Tabs, m.keys.Move, m.keys.Slash, m.keys.Scan, m.keys.Help, key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "home")), m.keys.Quit}
	}
	// The footer starts with one space. A line that is too long loses hints from
	// the middle, so the last three (help, home, quit) stay.
	return " " + m.help.ShortHelpView(m.fitKeys(keys, m.w-1))
}

// fitKeys drops hints, from just before the last three backwards, until the
// footer fits width. The last three always stay.
func (m TuiModel) fitKeys(keys []key.Binding, width int) []key.Binding {
	m.help.SetWidth(0) // measure without any cut
	for len(keys) > 3 && lipgloss.Width(m.help.ShortHelpView(keys)) > width {
		keys = slices.Concat(keys[:len(keys)-4], keys[len(keys)-3:])
	}
	return keys
}

// paletteWindow is the range of completions the / menu shows: all of them
// when the window has room (up to half its height, and always leaving the
// header, input line, footer and 3 body lines), otherwise a window that
// keeps the picked one in view.
// paletteChrome is the lines the command palette uses besides its list: a blank line above,
// the rule, the input and a blank line below.
const paletteChrome = 4

func (m TuiModel) paletteWindow() (start, end int) {
	n := len(m.completions())
	rows := min(n, max(4, m.h/2), max(1, m.h-9-(paletteChrome-2)))
	start = max(0, min(m.pick-rows+1, n-rows))
	return start, start + rows
}

func (m TuiModel) inputView() string {
	t := m.t
	var b strings.Builder
	comps := m.completions()
	start, end := m.paletteWindow()
	b.WriteString("\n") // a blank line above the list
	for i := start; i < end; i++ {
		c := comps[i]
		prefix := string(m.InputMode)
		name := t.accent.Render(fmt.Sprintf("%-18s", prefix+c.value))
		line := "   " + name + " " + t.muted.Render(c.desc)
		if i == m.pick {
			line = " " + t.brand.Render("▸") + " " + t.accent.Bold(true).Render(fmt.Sprintf("%-18s", prefix+c.value)) + " " + t.text.Render(c.desc)
		}
		b.WriteString(truncateANSI(line, m.w) + "\n")
	}
	b.WriteString(t.rule(m.w) + "\n")
	b.WriteString(" " + t.brand.Render("› "+string(m.InputMode)) + m.input.View() + "\n")
	b.WriteString("\n") // and one below the input, before the footer
	return b.String()
}

func (m TuiModel) loadingView() string {
	t := m.t
	var b strings.Builder
	b.WriteString("\n")
	if len(m.steps) == 0 {
		fmt.Fprintf(&b, "  %s %s\n", m.spin.View(), t.text.Render("Starting…"))
	}
	for _, s := range m.steps {
		label := stepLabels[s.step]
		if s.done {
			fmt.Fprintf(&b, "  %s %s %s\n", t.Ok.Render("✓"), t.muted.Render(label), t.faint.Render(fmtDur(s.took)))
			continue
		}
		fmt.Fprintf(&b, "  %s %s %s\n", m.spin.View(), t.Title.Render(label+"…"), t.faint.Render(fmtDur(time.Since(s.start))))
		if s.step == engine.StepScan {
			b.WriteString(m.scanProgressView())
		}
	}
	if m.Stopping || m.Quitting {
		b.WriteString("\n  " + t.warn.Render("Stopping… partial results are not used."))
	}
	return b.String()
}

// expectedScanBytes is how much the scan is expected to measure, so the bar can
// show a percentage: the scan on screen, else the one the welcome screen
// reported, else the latest stored one. 0 means unknown (a first scan).
func (m TuiModel) expectedScanBytes() int64 {
	switch {
	case m.Data != nil && m.Data.Res.Scan.TotalBytes > 0:
		return m.Data.Res.Scan.TotalBytes
	case m.IntroStatus.Bytes > 0:
		return m.IntroStatus.Bytes
	case m.Deps != nil && m.Deps.Home != "":
		if info, _, err := engine.LastScan(m.Deps.Home, time.Now()); err == nil {
			return info.TotalBytes
		}
	}
	return 0
}

func (m TuiModel) scanProgressView() string {
	t := m.t
	p := m.prog
	var b strings.Builder
	b.WriteString("\n")
	if m.estimate > 0 {
		pct := min(0.99, float64(p.Bytes)/float64(m.estimate))
		fmt.Fprintf(&b, "    %s\n\n", m.barView(pct, true)) // a blank line under the bar
	} else {
		// No earlier scan to estimate from: the bar still shows, sweeping
		// back and forth, with no percentage (the total is unknown).
		fmt.Fprintf(&b, "    %s\n\n    %s\n", m.barView(sweep(p.Elapsed), false), t.hint("first scan, size unknown"))
	}
	fmt.Fprintf(&b, "    %s  %s  %s\n", t.size.Render(engine.DecimalBytes(p.Bytes)),
		t.muted.Render(fmt.Sprintf("%s files", groupDigits(p.Files))), t.faint.Render(p.Elapsed.Round(time.Second).String()))
	if p.CurrentDir != "" {
		fmt.Fprintf(&b, "    %s\n", t.faint.Render(truncateMiddle(p.CurrentDir, max(20, m.w-8))))
	}
	b.WriteString("\n    " + t.muted.Render("Runs at background priority so your apps stay responsive.") + "\n")
	return b.String()
}

// textView renders a non-list tab as tables.
func (m TuiModel) textView() string {
	switch m.Tab {
	case tabProjects:
		return m.projectTables()
	case TabSpace:
		return m.spaceTables()
	}
	return m.overviewTables()
}

func indentLines(s, pad string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if l != "" {
			lines[i] = pad + l
		}
	}
	return strings.Join(lines, "\n")
}

// suggestionsView is the selectable table, with the selected item's details
// beside it (wide screens) or instead of it (Enter on narrow ones).
func (m TuiModel) suggestionsView() string {
	ops := m.Items()
	if len(ops) == 0 {
		return "\n  " + m.t.muted.Render("Nothing to reclaim was found in the latest scan.")
	}
	h := m.bodyHeight()
	if m.Detail && m.w < 120 {
		return m.scrolled(m.detailView(ops[m.Cursor], m.w))
	}
	listW := m.w
	if m.w >= 120 {
		listW = m.w - max(46, m.w*2/5)
	}
	list := fitHeight(m.listView(ops, listW, h), h, listW)
	if m.w < 120 {
		return list
	}
	detail := fitHeight(m.detailView(ops[m.Cursor], m.w-listW-3), h, m.w-listW-3)
	l, d := strings.Split(list, "\n"), strings.Split(detail, "\n")
	var b strings.Builder
	for i := 0; i < h; i++ {
		b.WriteString(l[i] + strings.Repeat(" ", max(0, listW-lipgloss.Width(l[i]))) + " " + m.t.faint.Render("│") + " " + d[i] + "\n")
	}
	return b.String()
}

// listView is the tier totals and a window of the suggestions table that
// keeps the cursor in view.
func (m TuiModel) listView(ops []model.Opportunity, w, h int) string {
	t := m.t
	var totals []string
	for _, tier := range engine.TierOrder {
		if tierOps := opsInTier(ops, tier); len(tierOps) > 0 {
			totals = append(totals, t.riskStyle(tier).Bold(true).Render(riskLabel(tier))+" "+
				t.size.Render(tierTotal(m.Data.Res.Opportunities.Totals[tier], tierOps)))
		}
	}
	rowsFit := max(1, h-6) // blank, totals, table top border, header, header rule, bottom border
	start := max(0, min(m.Cursor-rowsFit/2, len(ops)-rowsFit))
	end := min(len(ops), start+rowsFit)
	pos := t.muted.Render(fmt.Sprintf("item %d of %d", m.Cursor+1, len(ops)))
	head := " " + strings.Join(totals, t.faint.Render("  ·  ")) + "   " + pos
	if note := m.markedNote(); note != "" {
		head += "   " + note
	}
	return "\n" + head + "\n" + indentLines(m.opsTable(ops[start:end], m.Cursor-start, w-2, w < m.w), " ")
}

func (m TuiModel) detailView(op model.Opportunity, w int) string {
	ex := ExplainResult{Scan: m.Data.Res.Scan, Opportunity: op, BaseRisk: op.Risk}
	if base, raised := engine.RaisedFrom(op, m.Deps.cat); raised {
		ex.BaseRisk = base
	}
	var buf strings.Builder
	saved := m.t.width
	m.t.width = w
	ex.setSpan(m.span.done[op.ID])
	_ = RenderExplain(&themedWriter{Writer: &buf, t: m.t}, ex, m.spanLine(op.ID))
	m.t.width = saved
	return "\n" + buf.String()
}

// HelpView renders the key and command help overlay.
func (m TuiModel) HelpView() string {
	t := m.t
	var b strings.Builder
	b.WriteString("\n " + t.heading("Keys") + "\n")
	keys := [][2]string{
		{"tab / shift+tab, 1–4", "switch tab (the scan view)"},
		{"↑ ↓  j k  pgup pgdn", "move or scroll"},
		{"enter", "remove the selected suggestion (or the marked ones), after you confirm"},
		{"space", "mark several suggestions to remove together"},
		{"e", "explain the selected suggestion"},
		{"x", "hide this kind of suggestion from now on"},
		{"d  (after /reclaim)", "review the RISKY items it left alone; each needs its ID typed"},
		{"/", "command palette"},
		{"?", "this help"},
		{"r", "rescan the disk"},
		{"esc", "stop a scan · close a page · back to the home screen"},
		{"q, ctrl+c", "quit"},
	}
	for _, k := range keys {
		fmt.Fprintf(&b, "   %s %s\n", t.accent.Render(fmt.Sprintf("%-22s", k[0])), t.muted.Render(k[1]))
	}
	b.WriteString("\n " + t.heading("Commands") + "\n")
	for _, c := range SlashCommands {
		fmt.Fprintf(&b, "   %s %s\n", t.accent.Render(fmt.Sprintf("%-22s", strings.TrimSpace("/"+c.Name+" "+c.args))), t.muted.Render(c.desc))
	}
	b.WriteString("\n " + t.heading("Safety") + "\n")
	b.WriteString(t.muted.Render("   /reclaim removes every SAFE and CAUTION item right after its scan, without") + "\n")
	b.WriteString(t.muted.Render("   asking (esc stops it; nothing RISKY is ever included). Everything else asks:") + "\n")
	b.WriteString(t.muted.Render("   Enter shows what will go and how") + "\n")
	b.WriteString(t.muted.Render("   much space comes back; Enter again removes it (SAFE and CAUTION items).") + "\n")
	b.WriteString(t.muted.Render("   A RISKY item also needs its code typed. Each item is checked again just") + "\n")
	b.WriteString(t.muted.Render("   before it's removed, and skipped if it changed. Removed items are deleted") + "\n")
	b.WriteString(t.muted.Render("   for good, so the space is freed at once. Everything is in macdoc log.") + "\n")
	return b.String()
}

// --- small helpers ---

func fmtDur(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return d.Round(100 * time.Millisecond).String()
}

func groupDigits(n int64) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// truncateMiddle keeps the start and end of a long path.
func truncateMiddle(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	half := (n - 1) / 2
	return string(r[:half]) + "…" + string(r[len(r)-(n-1-half):])
}

var _ io.Writer = (*themedWriter)(nil)

// cleanData returns the display copy of freshly loaded data, keeping the
// engine's opportunities aside (planOps) for planning.
func cleanData(d *TuiData) *TuiData {
	if d == nil {
		return nil
	}
	raw := d.Res.Opportunities
	// TuiData's fields are unexported, which display (textsafe.Copy) leaves
	// as they are, so each is cleaned on its own.
	c := TuiData{Res: display(d.Res), Space: display(d.Space), Projects: display(d.Projects),
		Slow: display(d.Slow), Trend: display(d.Trend), trendMsg: display(d.trendMsg), PlanOps: &raw}
	return &c
}

// sweep is a position in [0,1] that moves back and forth with time, for a bar
// whose total is unknown.
func sweep(elapsed time.Duration) float64 {
	const period = 3 * time.Second
	x := float64(elapsed%period) / float64(period) // 0..1
	if x > 0.5 {
		x = 1 - x
	}
	return 2 * x
}
