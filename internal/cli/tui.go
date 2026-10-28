package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"macdoc/internal/engine"
	"macdoc/internal/executor"
	"macdoc/internal/model"
	"macdoc/internal/planner"
	"macdoc/internal/recipes"
	"macdoc/internal/scanner"
)

// The interactive dashboard behind bare `macdoc` in a terminal. It is
// read-only like every other view: it can scan and show, never change.

type tab int

const (
	tabOverview tab = iota
	tabSuggestions
	tabToolchains
	tabProjects
	tabSpace
	tabHealth
)

var tabTitles = []string{"Overview", "Suggestions", "Toolchains", "Projects", "Space", "Health"}

// slashCommand is one entry of the / palette.
type slashCommand struct {
	name, args, desc string
}

var slashCommands = []slashCommand{
	{"overview", "", "health, space and reclaimable at a glance"},
	{"scan", "", "rescan the disk, then refresh every view (Esc stops it)"},
	{"plan", "[safe|caution|risky]", "seal a dry-run plan: marked suggestions, or a whole tier"},
	{"apply", "[P-id]", "apply a plan in this terminal, after you confirm (default: the last one)"},
	{"wins", "", "every suggestion, ranked by safety"},
	{"dev", "", "suggestions per developer toolchain"},
	{"projects", "", "projects: idle days, build folders, git state"},
	{"space", "", `space by category, "System Data" decoded`},
	{"health", "", "disk, memory, swap and top memory users"},
	{"explain", "<id>", "one suggestion in detail"},
	{"json", "", "this view as JSON"},
	{"mcp", "", "Claude integration status"},
	{"help", "", "keys and commands"},
	{"quit", "", "leave macdoc"},
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

type (
	stepMsg     engine.OverviewStep
	progressMsg scanner.Progress
	loadedMsg   struct {
		data *tuiData
		err  error
	}
	planMsg struct {
		res engine.PlanResult
		err error
	}
)

// tuiData is everything the dashboard shows.
type tuiData struct {
	res      engine.OverviewResult
	space    engine.StorageResult
	dev      DevResult
	projects engine.ProjectsResult
}

// tuiDeps are shared by every copy of the model.
type tuiDeps struct {
	home string
	cat  *recipes.Catalog
	send func(tea.Msg) // set once the program exists; nil in tests
	// load and makePlan replace the engine in tests.
	load     func(ctx context.Context, d *tuiDeps, rescan bool) (*tuiData, error)
	makePlan func(d *tuiDeps, sel planner.Selector, pre *engine.OpportunityResult) (engine.PlanResult, error)
	// apply replaces runApply in tests.
	apply func(cmd *cobra.Command, d applyDeps, id string) error
	// remove replaces executorRemove in tests.
	remove func(d *tuiDeps, p model.Plan, onItem func(executor.ItemEvent)) (executor.Report, error)
}

// enginePlan seals a plan with the real engine, reusing the dashboard's
// opportunities when they are from the latest scan.
func enginePlan(d *tuiDeps, sel planner.Selector, pre *engine.OpportunityResult) (engine.PlanResult, error) {
	return engine.CreatePlan(context.Background(), engine.PlanOptions{
		OpportunityOptions: engine.OpportunityOptions{Home: d.home, Runner: runner, Catalog: d.cat},
		Selector:           sel,
		Precomputed:        pre,
	})
}

func (d *tuiDeps) post(msg tea.Msg) {
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
	Run, Complete, Cancel, Mark, Plan, Apply, Remove         key.Binding
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
		Plan:     key.NewBinding(key.WithKeys("p"), key.WithHelp("p", "plan")),
		Apply:    key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "apply (you confirm in Terminal)")),
	}
}

type overlay int

const (
	overlayNone overlay = iota
	overlayHelp
	overlayJSON
	overlayPlan
	overlayRemove
)

type tuiModel struct {
	deps *tuiDeps
	t    *theme
	keys keyMap
	help help.Model

	w, h int
	tab  tab

	// loading
	loading  bool
	stopping bool // Esc pressed; waiting for the engine to return
	quitting bool // quit once loading returns
	steps    []loadStep
	prog     scanner.Progress
	estimate int64 // bytes the scan is expected to reach (last scan), 0 = unknown
	spin     spinner.Model
	bar      progress.Model
	cancel   context.CancelFunc

	data *tuiData
	note string // empty-state or error text when there is no data

	// suggestions
	cursor int
	detail bool // narrow screens: the detail replaces the list

	// plans: marked suggestion IDs (shared by copies of the model, cleared
	// on every new load), the plan being sealed, and the last one made
	marked   map[string]bool
	planning bool
	plan     *engine.PlanResult
	removing *removeState // the remove confirmation box, while open

	scroll  int
	overlay overlay
	jsonDoc string

	input     textinput.Model
	inputMode rune // 0 or '/'
	pick      int  // highlighted completion
	flash     string

	initCmd tea.Cmd

	// welcome screen
	intro       bool
	introPick   int
	introStatus introStatus
}

func newTUIModel(deps *tuiDeps, t *theme) tuiModel {
	sp := spinner.New(spinner.WithSpinner(spinner.Spinner{
		Frames: []string{"·", "✢", "✳", "✶", "✻", "✽", "✻", "✶", "✳", "✢"},
		FPS:    time.Second / 10,
	}), spinner.WithStyle(t.brand))
	bar := progress.New(progress.WithScaledGradient("#E8875F", "#F2CC60"), progress.WithoutPercentage(),
		progress.WithFillCharacters('━', '━'), progress.WithColorProfile(t.r.ColorProfile()))
	bar.EmptyColor = "#3A3A3A"
	in := textinput.New()
	in.Prompt = ""
	in.CharLimit = 64
	in.Cursor.Style = t.brand
	h := help.New()
	h.Styles.ShortKey = t.accent
	h.Styles.ShortDesc = t.muted
	h.Styles.ShortSeparator = t.faint
	h.ShortSeparator = " • "
	return tuiModel{deps: deps, t: t, keys: newKeyMap(), help: h, spin: sp, bar: bar, input: in, w: 100, h: 30, marked: map[string]bool{}}
}

// runTUI starts the dashboard on the terminal.
func runTUI(home string, cat *recipes.Catalog) error {
	if err := scanner.SetBackgroundPriority(); err != nil {
		fmt.Fprintln(os.Stderr, "macdoc: could not lower priority:", err)
	}
	t := newTheme(os.Stdout)
	if !t.on {
		// NO_COLOR or a monochrome terminal: keep the layout, drop colors.
		t = plainLayoutTheme()
	}
	deps := &tuiDeps{home: home, cat: cat, load: engineLoad, makePlan: enginePlan}
	m := newTUIModel(deps, t)
	// Open on the welcome screen; nothing runs until the user picks.
	m.intro, m.introStatus = true, loadIntroStatus(home)
	p := tea.NewProgram(m, tea.WithAltScreen())
	deps.send = p.Send
	final, err := p.Run()
	if err != nil {
		return err
	}
	if fm, ok := final.(tuiModel); ok && fm.data != nil {
		fmt.Println(t.done("Nothing has been changed."))
	}
	return nil
}

// plainLayoutTheme draws boxes and badges without color.
func plainLayoutTheme() *theme {
	t := plainTheme()
	t.on = true
	return t
}

// engineLoad is the real loader: overview, then space and toolchains.
func engineLoad(ctx context.Context, d *tuiDeps, rescan bool) (*tuiData, error) {
	res, err := engine.Overview(ctx, engine.OverviewOptions{
		Home: d.home, Runner: runner, Version: version, Rescan: rescan, Catalog: d.cat, ScanRoot: scanRoot,
		Step:     func(s engine.OverviewStep) { d.post(stepMsg(s)) },
		Progress: func(p scanner.Progress) { d.post(progressMsg(p)) },
	})
	if err != nil {
		return nil, err
	}
	d.post(stepMsg(stepSpace))
	space, err := engine.Storage(ctx, engine.StorageOptions{
		OpportunityOptions: engine.OpportunityOptions{Home: d.home, Runner: runner, Catalog: d.cat},
		Large:              true,
		Precomputed:        &res.Opportunities,
	})
	if err != nil {
		return nil, err
	}
	d.post(stepMsg(stepProjects))
	projs, err := engine.Projects(ctx, engine.OpportunityOptions{Home: d.home, Runner: runner, Catalog: d.cat})
	if err != nil {
		return nil, err
	}
	return &tuiData{res: res, space: space, dev: devResult(res.Opportunities, ""), projects: filterProjects(projs, 0)}, nil
}

func (m tuiModel) Init() tea.Cmd { return m.initCmd }

// startLoad puts m into the loading state and returns the command that runs
// the engine. Call it on the model that Update returns (or before the program
// starts, for the first load).
func (m *tuiModel) startLoad(rescan bool) tea.Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.loading, m.stopping = true, false
	m.steps, m.prog = nil, scanner.Progress{}
	m.estimate = 0
	if m.data != nil {
		m.estimate = m.data.res.Scan.TotalBytes
	}
	deps := m.deps
	run := func() tea.Msg {
		data, err := deps.load(ctx, deps, rescan)
		return loadedMsg{data: data, err: err}
	}
	return tea.Batch(run, m.spin.Tick)
}

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.t.width = msg.Width
		m.bar.Width = min(48, max(10, msg.Width-30))
		return m, nil

	case stepMsg:
		now := time.Now()
		for i := range m.steps {
			if !m.steps[i].done {
				m.steps[i].done, m.steps[i].took = true, now.Sub(m.steps[i].start)
			}
		}
		m.steps = append(m.steps, loadStep{step: engine.OverviewStep(msg), start: now})
		return m, nil

	case progressMsg:
		m.prog = scanner.Progress(msg)
		return m, nil

	case loadedMsg:
		return m.loaded(msg)

	case planMsg:
		return m.planned(msg)

	case appliedMsg:
		return m.applied(msg)

	case removeReadyMsg:
		return m.removeReady(msg)

	case removeProgressMsg:
		return m.removeProgress(msg)

	case removedMsg:
		return m.removed(msg)

	case spinner.TickMsg:
		if !m.loading {
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case tea.KeyMsg:
		m.flash = ""
		if m.inputMode != 0 {
			return m.updateInput(msg)
		}
		return m.updateKey(msg)
	}
	return m, nil
}

func (m tuiModel) loaded(msg loadedMsg) (tea.Model, tea.Cmd) {
	m.loading, m.cancel = false, nil
	if m.quitting {
		return m, tea.Quit
	}
	// A stopped scan can still return (partial) results; they are never shown.
	switch {
	case msg.err == nil && !m.stopping:
		m.data, m.note = msg.data, ""
		clear(m.marked) // IDs belong to a scan
		m.cursor = min(m.cursor, max(0, len(m.items())-1))
	case m.stopping || errors.Is(msg.err, context.Canceled):
		if m.data != nil {
			m.flash = "Scan stopped. Showing the previous results."
		} else {
			m.note = "Scan stopped before it finished. Press r (or /scan) to scan again."
		}
	default:
		if m.data != nil {
			m.flash = "Error: " + msg.err.Error()
		} else {
			m.note = msg.err.Error()
		}
	}
	m.stopping = false
	return m, nil
}

func (m tuiModel) updateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	k := msg.String()
	if m.overlay == overlayRemove && m.removing != nil {
		if k == "ctrl+c" && !m.removing.running {
			return m, tea.Quit
		}
		return m.removeKey(msg)
	}
	if k == "ctrl+c" || (k == "q" && m.overlay == overlayNone) {
		if m.loading && m.cancel != nil {
			if m.quitting { // second press: leave now
				return m, tea.Quit
			}
			m.quitting = true
			m.cancel()
			return m, nil
		}
		return m, tea.Quit
	}
	if m.loading {
		if k == "esc" && m.cancel != nil {
			m.stopping = true
			m.cancel()
		}
		return m, nil
	}

	if m.overlay != overlayNone {
		if k == "a" && m.overlay == overlayPlan && m.plan != nil && m.plan.Status == planner.StatusPending {
			return m.startApply(m.plan.Plan.ID)
		}
		switch k {
		case "esc", "q", "?", "enter":
			m.overlay, m.scroll = overlayNone, 0
		default:
			m.scrollBy(k)
		}
		return m, nil
	}
	if m.intro {
		return m.updateIntroKey(k)
	}

	switch k {
	case "esc":
		m.detail = false
	case "?":
		m.overlay, m.scroll = overlayHelp, 0
	case "/":
		m.inputMode, m.pick = rune(k[0]), 0
		m.input.SetValue("")
		m.input.Focus()
		return m, textinput.Blink
	case "tab", "right", "l":
		m.setTab((m.tab + 1) % tab(len(tabTitles)))
	case "shift+tab", "left", "h":
		m.setTab((m.tab + tab(len(tabTitles)) - 1) % tab(len(tabTitles)))
	case "1", "2", "3", "4", "5", "6":
		m.setTab(tab(k[0] - '1'))
	case "r":
		return m, m.startLoad(true)
	case "enter":
		if m.tab == tabSuggestions && len(m.items()) > 0 {
			if m.detail {
				m.detail = false
				break
			}
			return m.startRemove()
		}
	case "e":
		if m.tab == tabSuggestions && len(m.items()) > 0 {
			m.detail = !m.detail
		}
	case " ":
		if m.tab == tabSuggestions {
			m.toggleMark()
		}
	case "p":
		if m.tab == tabSuggestions {
			return m.startPlan(m.markedSelector())
		}
		m.flash = "Plans are made on the Suggestions tab (2): mark items with space, then press p."
	default:
		if m.tab == tabSuggestions && !m.detail {
			m.moveCursor(k)
		} else {
			m.scrollBy(k)
		}
	}
	return m, nil
}

func (m *tuiModel) setTab(t tab) {
	m.tab, m.scroll, m.detail = t, 0, false
}

func (m *tuiModel) moveCursor(k string) {
	n := len(m.items())
	switch k {
	case "up", "k":
		m.cursor--
	case "down", "j":
		m.cursor++
	case "pgup":
		m.cursor -= 10
	case "pgdown", " ":
		m.cursor += 10
	case "home", "g":
		m.cursor = 0
	case "end", "G":
		m.cursor = n - 1
	}
	m.cursor = max(0, min(m.cursor, n-1))
}

func (m *tuiModel) scrollBy(k string) {
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

func (m tuiModel) completions() []completion {
	q := strings.ToLower(strings.TrimSpace(m.input.Value()))
	var out []completion
	name, _, _ := strings.Cut(q, " ")
	for _, c := range slashCommands {
		if strings.HasPrefix(c.name, name) {
			v := c.name
			if c.args != "" {
				v += " " + c.args
			}
			out = append(out, completion{v, c.desc})
		}
	}
	return out
}

func (m tuiModel) updateInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	comps := m.completions()
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.inputMode = 0
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
		m.inputMode = 0
		m.input.Blur()
		return m.runCommand(v)
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.pick = 0
	return m, cmd
}

func (m tuiModel) runCommand(line string) (tea.Model, tea.Cmd) {
	name, arg, _ := strings.Cut(strings.TrimPrefix(line, "/"), " ")
	arg = strings.TrimSpace(arg)
	views := map[string]tab{
		"overview": tabOverview, "wins": tabSuggestions, "suggestions": tabSuggestions,
		"dev": tabToolchains, "toolchains": tabToolchains, "projects": tabProjects, "space": tabSpace, "storage": tabSpace, "health": tabHealth,
	}
	if v, ok := views[name]; ok {
		m.setTab(v)
		if m.intro { // a view from the welcome screen starts the dashboard there
			return m, m.leaveIntro(false)
		}
		return m, nil
	}
	switch name {
	case "":
	case "scan":
		m.intro = false
		return m, m.startLoad(true)
	case "explain", "json":
		if m.intro {
			m.flash = "Choose “Check my Mac” first: /" + name + " needs results."
			return m, nil
		}
		if name == "explain" {
			return m.explain(arg)
		}
		m.jsonDoc = m.currentJSON()
		m.overlay, m.scroll = overlayJSON, 0
	case "plan":
		if m.intro {
			m.flash = "Choose “Check my Mac” first: /plan needs results."
			return m, nil
		}
		switch r := model.Risk(strings.ToUpper(arg)); r {
		case "":
			return m.startPlan(m.markedSelector())
		case model.RiskSafe, model.RiskCaution, model.RiskRisky:
			return m.startPlan(&planner.Selector{Tier: r})
		}
		m.flash = "Usage: /plan (marked suggestions) or /plan safe|caution|risky"
	case "apply":
		return m.startApply(arg)
	case "mcp":
		m.flash = "MCP arrives in Phase 8: Claude will read diagnoses and propose plans. Only you apply them."
	case "help":
		m.overlay, m.scroll = overlayHelp, 0
	case "quit", "exit", "q":
		return m, tea.Quit
	default:
		m.flash = fmt.Sprintf("Unknown command /%s. Type / to see the list.", name)
	}
	return m, nil
}

func (m tuiModel) explain(id string) (tea.Model, tea.Cmd) {
	if id == "" {
		m.flash = "Usage: /explain <id>, e.g. /explain O-ab12cd"
		return m, nil
	}
	for i, op := range m.items() {
		if strings.EqualFold(op.ID, id) {
			m.setTab(tabSuggestions)
			m.cursor, m.detail = i, true
			return m, nil
		}
	}
	m.flash = id + " is not in the latest scan; IDs change after a new scan."
	return m, nil
}

// items are the suggestions, in ranked order.
func (m tuiModel) items() []model.Opportunity {
	if m.data == nil {
		return nil
	}
	return m.data.res.Opportunities.Opportunities
}

// currentJSON is the current view's data, as the matching --json command
// would print it (without the envelope).
func (m tuiModel) currentJSON() string {
	if m.data == nil {
		return "null"
	}
	var v any
	switch m.tab {
	case tabOverview:
		v = m.data.res
	case tabSuggestions:
		if ops := m.items(); m.detail && m.cursor < len(ops) {
			v = ops[m.cursor]
		} else {
			v = m.data.res.Opportunities
		}
	case tabToolchains:
		v = m.data.dev
	case tabProjects:
		v = m.data.projects
	case tabSpace:
		v = m.data.space
	case tabHealth:
		v = m.data.res.Health
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err.Error()
	}
	return string(b)
}

// --- view ---

func (m tuiModel) bodyHeight() int {
	h := m.h - 4 // header, tabs, rule, footer
	if m.inputMode != 0 {
		start, end := m.paletteWindow()
		h -= 2 + end - start
	} else if m.flash != "" {
		h--
	}
	return max(3, h)
}

func (m tuiModel) View() string {
	t := m.t
	var b strings.Builder
	if m.intro && m.overlay == overlayNone && !m.loading {
		b.WriteString(fitHeight(m.introView(), m.introHeight(), m.w))
		if m.inputMode != 0 {
			b.WriteString(m.inputView())
		} else if m.flash != "" {
			b.WriteString(" " + t.warn.Render("›") + " " + truncateANSI(t.text.Render(m.flash), m.w-4) + "\n")
		}
		if m.inputMode != 0 {
			b.WriteString(m.footerView())
		} else {
			b.WriteString(m.introFooter())
		}
		return b.String()
	}
	b.WriteString(m.headerView() + "\n")
	b.WriteString(m.tabsView() + "\n")

	var body string
	switch {
	case m.loading:
		body = m.loadingView()
	case m.overlay == overlayHelp:
		body = m.scrolled(m.helpView())
	case m.overlay == overlayJSON:
		body = m.scrolled(highlightJSON(t, m.jsonDoc))
	case m.overlay == overlayPlan:
		body = m.scrolled(m.planView())
	case m.overlay == overlayRemove:
		body = m.scrolled(m.removeView())
	case m.data == nil:
		body = "\n  " + t.muted.Render(m.note)
	case m.tab == tabSuggestions:
		body = m.suggestionsView()
	default:
		body = m.scrolled(m.textView())
	}
	b.WriteString(fitHeight(body, m.bodyHeight(), m.w))

	if m.inputMode != 0 {
		b.WriteString(m.inputView())
	} else if m.flash != "" {
		b.WriteString(" " + t.warn.Render("›") + " " + truncateANSI(t.text.Render(m.flash), m.w-4) + "\n")
	}
	b.WriteString(m.footerView())
	return b.String()
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

// scrolled applies the scroll offset to a text body.
func (m *tuiModel) scrolled(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	maxOff := max(0, len(lines)-m.bodyHeight())
	m.scroll = min(m.scroll, maxOff)
	return strings.Join(lines[m.scroll:], "\n")
}

func (m tuiModel) headerView() string {
	t := m.t
	left := " " + t.brand.Render("◆ macdoc")
	var right []string
	if m.data != nil {
		hw := m.data.res.Health.Report.Hardware
		if hw.Model != "" {
			left += t.muted.Render("  " + hw.Model + " · " + strings.TrimPrefix(hw.Chip, "Apple "))
		}
		if d := m.data.res.Health.Report.Disk; d.ContainerTotalBytes > 0 {
			used := d.ContainerTotalBytes - d.ContainerFreeBytes
			right = append(right, t.usageStyle(used, d.ContainerTotalBytes).Render(engine.DecimalBytes(d.ContainerFreeBytes)+" free"))
		}
		age := "scan " + m.data.res.ScanAge
		if m.data.res.Scanned {
			age = "scanned just now"
		}
		st := t.muted
		if m.data.res.ScanStale {
			st = t.warn
		}
		right = append(right, st.Render(age))
	}
	right = append(right, t.ok.Render("asks before removing"))
	r := strings.Join(right, t.faint.Render(" · ")) + " "
	gap := max(1, m.w-lipgloss.Width(left)-lipgloss.Width(r))
	return left + strings.Repeat(" ", gap) + r
}

func (m tuiModel) tabsView() string {
	t := m.t
	var parts []string
	for i, title := range tabTitles {
		label := fmt.Sprintf("%d %s", i+1, title)
		if tab(i) == m.tab {
			parts = append(parts, t.r.NewStyle().Background(colBrand).Foreground(colOnBadge).Bold(true).Padding(0, 1).Render(label))
		} else {
			parts = append(parts, t.muted.Padding(0, 1).Render(label))
		}
	}
	row := " " + strings.Join(parts, " ")
	return row + "\n" + t.rule(m.w)
}

func (m tuiModel) footerView() string {
	var keys []key.Binding
	switch {
	case m.inputMode != 0:
		keys = []key.Binding{m.keys.Run, m.keys.Complete, m.keys.Move, m.keys.Cancel}
	case m.loading:
		keys = []key.Binding{m.keys.Stop, m.keys.Quit}
	case m.overlay == overlayRemove:
		keys = nil
	case m.overlay == overlayPlan && m.plan != nil && m.plan.Status == planner.StatusPending:
		keys = []key.Binding{m.keys.Apply, m.keys.Move, m.keys.Back}
	case m.overlay != overlayNone:
		keys = []key.Binding{m.keys.Move, m.keys.Back, m.keys.Quit}
	case m.tab == tabSuggestions && m.detail:
		keys = []key.Binding{m.keys.Back, m.keys.Tabs, m.keys.Slash, m.keys.Help, m.keys.Quit}
	case m.tab == tabSuggestions:
		keys = []key.Binding{m.keys.Remove, m.keys.Mark, m.keys.Explain, m.keys.Move, m.keys.Tabs, m.keys.Slash, m.keys.Scan, m.keys.Help, m.keys.Quit}
	default:
		keys = []key.Binding{m.keys.Tabs, m.keys.Move, m.keys.Slash, m.keys.Scan, m.keys.Help, m.keys.Quit}
	}
	m.help.Width = m.w - 2
	return " " + m.help.ShortHelpView(keys)
}

// paletteWindow is the range of completions the / menu shows: all of them
// when the window has room (up to half its height, and always leaving the
// header, input line, footer and 3 body lines), otherwise a window that
// keeps the picked one in view.
func (m tuiModel) paletteWindow() (start, end int) {
	n := len(m.completions())
	rows := min(n, max(4, m.h/2), max(1, m.h-9))
	start = max(0, min(m.pick-rows+1, n-rows))
	return start, start + rows
}

func (m tuiModel) inputView() string {
	t := m.t
	var b strings.Builder
	comps := m.completions()
	start, end := m.paletteWindow()
	for i := start; i < end; i++ {
		c := comps[i]
		prefix := string(m.inputMode)
		name := t.accent.Render(fmt.Sprintf("%-18s", prefix+c.value))
		line := "   " + name + " " + t.muted.Render(c.desc)
		if i == m.pick {
			line = " " + t.brand.Render("▸") + " " + t.accent.Bold(true).Render(fmt.Sprintf("%-18s", prefix+c.value)) + " " + t.text.Render(c.desc)
		}
		b.WriteString(truncateANSI(line, m.w) + "\n")
	}
	b.WriteString(t.rule(m.w) + "\n")
	b.WriteString(" " + t.brand.Render("› "+string(m.inputMode)) + m.input.View() + "\n")
	return b.String()
}

func (m tuiModel) loadingView() string {
	t := m.t
	var b strings.Builder
	b.WriteString("\n")
	if len(m.steps) == 0 {
		fmt.Fprintf(&b, "  %s %s\n", m.spin.View(), t.text.Render("Starting…"))
	}
	for _, s := range m.steps {
		label := stepLabels[s.step]
		if s.done {
			fmt.Fprintf(&b, "  %s %s %s\n", t.ok.Render("✓"), t.muted.Render(label), t.faint.Render(fmtDur(s.took)))
			continue
		}
		fmt.Fprintf(&b, "  %s %s %s\n", m.spin.View(), t.title.Render(label+"…"), t.faint.Render(fmtDur(time.Since(s.start))))
		if s.step == engine.StepScan {
			b.WriteString(m.scanProgressView())
		}
	}
	if m.stopping || m.quitting {
		b.WriteString("\n  " + t.warn.Render("Stopping… partial results are not used."))
	}
	return b.String()
}

func (m tuiModel) scanProgressView() string {
	t := m.t
	p := m.prog
	var b strings.Builder
	b.WriteString("\n")
	if m.estimate > 0 {
		pct := min(0.99, float64(p.Bytes)/float64(m.estimate))
		fmt.Fprintf(&b, "    %s %s\n", m.bar.ViewAs(pct), t.muted.Render(fmt.Sprintf("~%d%%", int(pct*100))))
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
func (m tuiModel) textView() string {
	switch m.tab {
	case tabToolchains:
		return m.toolchainTables()
	case tabProjects:
		return m.projectTables()
	case tabSpace:
		return m.spaceTables()
	case tabHealth:
		return m.healthTables()
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
func (m tuiModel) suggestionsView() string {
	ops := m.items()
	if len(ops) == 0 {
		return "\n  " + m.t.muted.Render("Nothing to reclaim was found in the latest scan.")
	}
	h := m.bodyHeight()
	if m.detail && m.w < 120 {
		return m.scrolled(m.detailView(ops[m.cursor], m.w))
	}
	listW := m.w
	if m.w >= 120 {
		listW = m.w - max(46, m.w*2/5)
	}
	list := fitHeight(m.listView(ops, listW, h), h, listW)
	if m.w < 120 {
		return list
	}
	detail := fitHeight(m.detailView(ops[m.cursor], m.w-listW-3), h, m.w-listW-3)
	l, d := strings.Split(list, "\n"), strings.Split(detail, "\n")
	var b strings.Builder
	for i := 0; i < h; i++ {
		b.WriteString(l[i] + strings.Repeat(" ", max(0, listW-lipgloss.Width(l[i]))) + " " + m.t.faint.Render("│") + " " + d[i] + "\n")
	}
	return b.String()
}

// listView is the tier totals and a window of the suggestions table that
// keeps the cursor in view.
func (m tuiModel) listView(ops []model.Opportunity, w, h int) string {
	t := m.t
	var totals []string
	for _, tier := range engine.TierOrder {
		if tierOps := opsInTier(ops, tier); len(tierOps) > 0 {
			totals = append(totals, t.riskStyle(tier).Bold(true).Render(riskLabel(tier))+" "+
				t.size.Render(tierTotal(m.data.res.Opportunities.Totals[tier], tierOps)))
		}
	}
	rowsFit := max(1, h-6) // blank, totals, table top border, header, header rule, bottom border
	start := max(0, min(m.cursor-rowsFit/2, len(ops)-rowsFit))
	end := min(len(ops), start+rowsFit)
	pos := t.muted.Render(fmt.Sprintf("item %d of %d", m.cursor+1, len(ops)))
	head := " " + strings.Join(totals, t.faint.Render("  ·  ")) + "   " + pos
	if note := m.markedNote(); note != "" {
		head += "   " + note
	}
	return "\n" + head + "\n" + indentLines(m.opsTable(ops[start:end], m.cursor-start, w-2, w < m.w), " ")
}

func (m tuiModel) detailView(op model.Opportunity, w int) string {
	ex := ExplainResult{Scan: m.data.res.Scan, Opportunity: op, BaseRisk: op.Risk}
	if base, raised := engine.RaisedFrom(op, m.deps.cat); raised {
		ex.BaseRisk = base
	}
	var buf strings.Builder
	saved := m.t.width
	m.t.width = w
	_ = renderExplain(&themedWriter{Writer: &buf, t: m.t}, ex)
	m.t.width = saved
	return "\n" + buf.String()
}

func (m tuiModel) helpView() string {
	t := m.t
	var b strings.Builder
	b.WriteString("\n " + t.heading("Keys") + "\n")
	keys := [][2]string{
		{"tab / shift+tab, 1–6", "switch view"},
		{"↑ ↓  j k  pgup pgdn", "move or scroll"},
		{"enter", "remove the selected suggestion (or the marked ones), after you confirm"},
		{"space", "mark several suggestions to remove together"},
		{"e", "explain the selected suggestion"},
		{"p", "seal a dry-run plan instead (marked, or the selected one)"},
		{"a  (on a plan)", "apply it here in the terminal, after you confirm"},
		{"/", "command palette"},
		{"r", "rescan the disk"},
		{"esc", "stop a scan · close"},
		{"q, ctrl+c", "quit"},
	}
	for _, k := range keys {
		fmt.Fprintf(&b, "   %s %s\n", t.accent.Render(fmt.Sprintf("%-22s", k[0])), t.muted.Render(k[1]))
	}
	b.WriteString("\n " + t.heading("Commands") + "\n")
	for _, c := range slashCommands {
		fmt.Fprintf(&b, "   %s %s\n", t.accent.Render(fmt.Sprintf("%-22s", strings.TrimSpace("/"+c.name+" "+c.args))), t.muted.Render(c.desc))
	}
	b.WriteString("\n " + t.heading("Safety") + "\n")
	b.WriteString(t.muted.Render("   Nothing is removed until you confirm. Enter shows what will go and how\n"))
	b.WriteString(t.muted.Render("   much space comes back; Enter again removes it (SAFE and CAUTION items).\n"))
	b.WriteString(t.muted.Render("   A RISKY item also needs its code typed. Each item is checked again just\n"))
	b.WriteString(t.muted.Render("   before it's removed, and skipped if it changed. Removed items are deleted\n"))
	b.WriteString(t.muted.Render("   for good, so the space is freed at once. Everything is in macdoc log.\n"))
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

// highlightJSON colors keys, strings, numbers and literals.
func highlightJSON(t *theme, src string) string {
	var b strings.Builder
	b.WriteString("\n")
	for _, line := range strings.Split(src, "\n") {
		b.WriteString(" ")
		i := 0
		for i < len(line) {
			c := line[i]
			switch {
			case c == '"':
				j := i + 1
				for j < len(line) && line[j] != '"' {
					if line[j] == '\\' {
						j++
					}
					j++
				}
				j = min(j+1, len(line))
				tok := line[i:j]
				rest := strings.TrimLeft(line[j:], " ")
				if strings.HasPrefix(rest, ":") {
					b.WriteString(t.jsonKey.Render(tok))
				} else {
					b.WriteString(t.jsonString.Render(tok))
				}
				i = j
			case c == '-' || (c >= '0' && c <= '9'):
				j := i + 1
				for j < len(line) && strings.IndexByte("0123456789.eE+-", line[j]) >= 0 {
					j++
				}
				b.WriteString(t.jsonNumber.Render(line[i:j]))
				i = j
			case strings.HasPrefix(line[i:], "true"), strings.HasPrefix(line[i:], "null"):
				b.WriteString(t.jsonLiteral.Render(line[i : i+4]))
				i += 4
			case strings.HasPrefix(line[i:], "false"):
				b.WriteString(t.jsonLiteral.Render(line[i : i+5]))
				i += 5
			case strings.IndexByte("{}[],:", c) >= 0:
				b.WriteString(t.faint.Render(string(c)))
				i++
			default:
				b.WriteByte(c)
				i++
			}
		}
		b.WriteString("\n")
	}
	return b.String()
}

var _ io.Writer = (*themedWriter)(nil)
