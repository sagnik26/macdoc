package cli

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"github.com/sagnik26/macdoc/internal/engine"
	"github.com/sagnik26/macdoc/internal/model"
)

// softwareListLimit caps the rows shown for each list; --json has them all.
const softwareListLimit = 25

// newSoftwareCmd builds `macdoc doctor software`: what is installed, which
// folders belong to it, and what is new since the last scan (read-only).
func newSoftwareCmd(g *GlobalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "software",
		Short: "Installed software, the data folders that belong to it, and what is new since the last scan (read-only)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := UserHome()
			if err != nil {
				return err
			}
			res, err := engine.Software(context.Background(), engine.SoftwareOptions{Home: home, Runner: Runner})
			if err != nil {
				return err
			}
			if g.json {
				return writeJSONWarn(cmd.OutOrStdout(), "doctor software", res, res.Warnings)
			}
			return RenderSoftware(cmd.OutOrStdout(), res)
		},
	}
}

// RenderSoftware writes the software inventory in the human format.
func RenderSoftware(w io.Writer, res engine.SoftwareResult) error {
	res = display(res) // names and paths come from the disk; clean them for the terminal
	var b strings.Builder
	t := themeFor(w)
	b.WriteString(t.commandHeader("software", res.Scan.ID, res.ScanAge))

	isNew := map[string]bool{}
	for _, id := range res.New {
		isNew[id] = true
	}

	b.WriteString(t.section(t.accent, "Installed software", softwareHeadline(res.Software)))
	if len(res.Software) == 0 {
		b.WriteString("  No software was found. See the discovery lines below.\n")
	}
	for i, sw := range res.Software {
		if i == softwareListLimit {
			fmt.Fprintf(&b, "  … and %d more (macdoc doctor software --json lists them all)\n", len(res.Software)-i)
			break
		}
		size := "no data folder found"
		if len(sw.Folders) > 0 {
			size = engine.DecimalBytes(sw.Bytes)
		}
		cleanup := "advisory only"
		if n := len(sw.CoveredBy); n > 0 {
			cleanup = fmt.Sprintf("%d cleanup recipe(s)", n)
		}
		mark := ""
		if isNew[sw.ID] {
			mark = "  new since last scan"
		}
		fmt.Fprintf(&b, "  %-32s %-22s %20s  %-20s%s\n", truncateMiddle(sw.Name, 32), kindLabel(sw), size, cleanup, mark)
	}

	b.WriteString("\n")
	var unexplained int64
	for _, f := range res.Unattributed {
		unexplained += f.Bytes
	}
	b.WriteString(t.section(t.accent, "Folders no software explains",
		fmt.Sprintf("%s in %d", engine.DecimalBytes(unexplained), len(res.Unattributed))))
	for i, f := range res.Unattributed {
		if i == softwareListLimit {
			fmt.Fprintf(&b, "  … and %d more\n", len(res.Unattributed)-i)
			break
		}
		fmt.Fprintf(&b, "  %12s  %s\n", engine.DecimalBytes(f.Bytes), f.Path)
	}

	b.WriteString("\n")
	b.WriteString(t.section(t.accent, "Discovery", ""))
	for _, s := range res.Sources {
		state := s.State
		if s.Reason != "" {
			state += " (" + s.Reason + ")"
		}
		fmt.Fprintf(&b, "  %-8s %s\n", s.Source, state)
	}
	writeNotes(&b, t, res.Warnings)
	b.WriteString(t.done("Nothing has been changed.") + "\n")
	_, err := io.WriteString(w, b.String())
	return err
}

// kindLabel names what a row is, with its version when there is one.
func kindLabel(sw engine.SoftwareEntry) string {
	if sw.Version != "" {
		return sw.Kind + " " + sw.Version
	}
	return sw.Kind
}

// softwareHeadline is the one-line count of the inventory, by category.
func softwareHeadline(sw []engine.SoftwareEntry) string {
	return engine.CategoryHeadline(sw)
}

// SoftwareMsg carries the software inventory read for the dashboard, or the
// error that stopped it.
type SoftwareMsg struct {
	Res engine.SoftwareResult
	Err error
}

// software reads the inventory for the dashboard. Tests replace the reader
// through TuiDeps.Software.
func (d *TuiDeps) software(ctx context.Context) (engine.SoftwareResult, error) {
	if d.Software != nil {
		return d.Software(ctx, d)
	}
	return engine.Software(ctx, engine.SoftwareOptions{Home: d.Home, Runner: Runner})
}

// startSoftware reads the software inventory in the background. Reading it
// stores a snapshot with the latest scan, as `doctor software` does.
func (m TuiModel) startSoftware() (tea.Model, tea.Cmd) {
	if m.softwareBusy {
		return m, nil
	}
	if m.Data == nil {
		m.Flash = "Check your Mac first: the software list is read after a scan."
		return m, nil
	}
	m.softwareBusy = true
	m.softwareErr = nil
	m.softwareStarted = time.Now()
	m.Overlay, m.scroll = overlaySoftware, 0
	m.Flash = ""
	deps := m.Deps
	read := func() tea.Msg {
		res, err := deps.software(context.Background())
		return SoftwareMsg{Res: res, Err: err}
	}
	return m, tea.Batch(read, m.spin.Tick)
}

// softwareLoadingView is shown while the inventory is read: a spinner, the
// time so far, and what is being looked at.
func (m TuiModel) softwareLoadingView() string {
	t := m.t
	var b strings.Builder
	b.WriteString("\n")
	fmt.Fprintf(&b, "  %s %s %s\n", m.spin.View(), t.Title.Render("Reading installed software…"),
		t.faint.Render(fmtDur(time.Since(m.softwareStarted))))
	b.WriteString("  " + t.muted.Render("Checking /Applications, Homebrew, npm, the programs on PATH, and the folders in your home.") + "\n")
	b.WriteString("  " + t.muted.Render("This can take a little while. esc closes this view; the list will be kept for next time.") + "\n")
	return b.String()
}

// softwareFailedView says why the inventory couldn't be read.
func (m TuiModel) softwareFailedView() string {
	return "\n  " + m.t.warn.Render("Couldn't read installed software: "+m.softwareErr.Error()) + "\n  " +
		m.t.muted.Render("esc to close, then run /installed again.") + "\n"
}

// softwareLoaded shows the inventory in an overlay, or why it couldn't be read.
func (m TuiModel) softwareLoaded(msg SoftwareMsg) (tea.Model, tea.Cmd) {
	m.softwareBusy = false
	if msg.Err != nil {
		m.softwareErr = msg.Err
		m.software = nil
		return m, nil
	}
	res := msg.Res
	m.software = &res
	m.softwareErr = nil
	m.softwareCursor, m.softwareDetail = 0, false
	// The result is kept even if the view was closed while reading; it is only
	// shown when the view is open, so it never reopens by itself.
	return m, nil
}

// softwareTables shows the software inventory in the dashboard's table style:
// one block for the installed software, one for the folders no software
// explains, and one for the discovery sources.
func (m TuiModel) softwareTables() string {
	if m.softwareBusy {
		return m.softwareLoadingView()
	}
	if m.software == nil {
		return m.softwareFailedView()
	}
	res := display(*m.software) // names and paths come from the disk
	return "\n" + m.softwareSummary(res) + "\n" + m.installedTable(res)
}

// softwareFilter picks which installed programs the table shows.
type softwareFilter int

const (
	filterWithData softwareFilter = iota
	filterCleanable
	filterNew
	filterAll
)

func (f softwareFilter) label() string {
	return [...]string{"with data", "cleanable", "new", "all"}[f]
}

func (f softwareFilter) next() softwareFilter { return (f + 1) % 4 }

func (f softwareFilter) keep(sw engine.SoftwareEntry, isNew bool) bool {
	switch f {
	case filterWithData:
		return len(sw.Folders) > 0
	case filterCleanable:
		return len(sw.CoveredBy) > 0
	case filterNew:
		return isNew
	}
	return true
}

// cleanupSummary is what the installed programs' cleanups would reclaim.
type cleanupSummary struct {
	Programs int      // programs that have at least one cleanup recipe
	Items    []string // suggestion IDs of those cleanups; advice-only items are left out
	Bytes    int64    // known sizes only
	Unknown  int      // suggestions whose size is unknown: never counted as zero
}

// softwareCleanups finds the suggestions that the installed programs' recipes
// produce. A suggestion belongs to a program when its recipe covers it.
func softwareCleanups(res engine.SoftwareResult, ops []model.Opportunity) cleanupSummary {
	var s cleanupSummary
	recipes := map[string]bool{}
	for _, sw := range res.Software {
		if len(sw.CoveredBy) > 0 {
			s.Programs++
		}
		for _, r := range sw.CoveredBy {
			recipes[r] = true
		}
	}
	for _, op := range ops {
		if op.Method == model.MethodAdvise || op.Risk == model.RiskAdvise || !recipes[op.RecipeID] {
			continue
		}
		s.Items = append(s.Items, op.ID)
		if op.Bytes.Source == model.SourceUnknown {
			s.Unknown++
		} else {
			s.Bytes += op.Bytes.Value
		}
	}
	return s
}

// softwareSummary is the top of the view: the headline, what cleanups would
// reclaim, what is new, and the largest folder no software explains.
func (m TuiModel) softwareSummary(res engine.SoftwareResult) string {
	sum := softwareCleanups(res, m.Items())
	verb := "have"
	if sum.Programs == 1 {
		verb = "has"
	}
	cl := fmt.Sprintf("%d %s %s cleanups · %s can be reclaimed", sum.Programs, plural(sum.Programs, "program", "programs"), verb, engine.DecimalBytes(sum.Bytes))
	if sum.Unknown > 0 {
		cl += fmt.Sprintf(" + %d of unknown size", sum.Unknown)
	}
	if len(sum.Items) > 0 {
		cl += " · press c to mark them"
	}
	names := map[string]string{}
	for _, sw := range res.Software {
		names[sw.ID] = sw.Name
	}
	newLine := "New since last scan: none"
	if len(res.New) > 0 {
		var ns []string
		for _, id := range res.New {
			ns = append(ns, names[id])
		}
		newLine = "New since last scan: " + strings.Join(ns, ", ")
	}
	lines := []string{softwareHeadline(res.Software), cl, newLine}
	if len(res.Unattributed) > 0 {
		big := res.Unattributed[0]
		lines = append(lines, fmt.Sprintf("Largest folder no software explains: %s (%s)",
			TildePath(m.Deps.Home, big.Path), engine.DecimalBytes(big.Bytes)))
	}
	return m.t.block("Summary", "", " "+strings.Join(lines, "\n ")+"\n")
}

// softwareKey handles the keys of the software view. ok is false for keys it
// doesn't use, which then fall through to the overlay's usual keys.
func (m TuiModel) softwareKey(k string) (tea.Model, tea.Cmd, bool) {
	if m.softwareBusy {
		switch k {
		case "esc", "q", "enter", "?":
			return m, nil, false // closing keys go to the overlay's usual handling
		}
		return m, nil, true // the list can't be used until the read finishes
	}
	if m.softwareDetail {
		switch k {
		case "esc":
			m.softwareDetail, m.scroll = false, 0
			return m, nil, true
		case "up", "down", "j", "k", "pgup", "pgdown", " ", "home", "end", "g", "G":
			return m, nil, false // scrolling the details
		}
		return m, nil, true // Enter and the list keys do nothing in the details
	}
	n := 0
	if m.software != nil {
		n = len(m.shownSoftware(display(*m.software)))
	}
	switch k {
	case "f":
		m.softwareFilter = m.softwareFilter.next()
		m.softwareCursor, m.scroll = 0, 0
		return m, nil, true
	case "c":
		next, cmd := m.markCleanable()
		return next, cmd, true
	case "up", "k":
		if m.softwareCursor > 0 {
			m.softwareCursor--
			m.keepCursorVisible()
		} else {
			m.scroll = max(0, m.scroll-m.scrollStep())
		}
		return m, nil, true
	case "down", "j":
		if m.softwareCursor < n-1 {
			m.softwareCursor++
			m.keepCursorVisible()
		} else {
			m.scroll = min(m.maxSoftwareScroll(), m.scroll+m.scrollStep())
		}
		return m, nil, true
	case "enter":
		if n > 0 {
			m.softwareDetail, m.scroll = true, 0
			return m, nil, true
		}
	}
	return m, nil, false
}

// scrollStep is how far the arrow keys scroll the view past the last row:
// half a screen.
func (m TuiModel) scrollStep() int {
	return max(1, m.bodyHeight()/2)
}

// maxSoftwareScroll is the furthest the software view can scroll.
func (m TuiModel) maxSoftwareScroll() int {
	lines := strings.Count(strings.TrimRight(m.softwareTables(), "\n"), "\n") + 1
	return max(0, lines-m.bodyHeight())
}

// keepCursorVisible scrolls the view so the highlighted program's row is on
// screen.
func (m *TuiModel) keepCursorVisible() {
	m.scrollToMarker(m.softwareTables())
}

// softwareDetailView shows one installed program: its data folders, and the
// suggestions its cleanup recipes produce.
func (m TuiModel) softwareDetailView() string {
	t, w := m.t, m.w-2
	res := display(*m.software)
	shown := m.shownSoftware(res)
	if m.softwareCursor >= len(shown) {
		return ""
	}
	sw := shown[m.softwareCursor]
	var b strings.Builder
	b.WriteString("\n")
	info := fmt.Sprintf(" %s · %s", sw.Name, kindLabel(sw))
	if sw.BundleID != "" {
		info += " · " + sw.BundleID
	}
	if sw.Installed != nil {
		info += " · installed " + sw.Installed.Format("2006-01-02")
	}
	b.WriteString(t.block("Program", "· esc to go back", info+"\n"+slimLine(sw)))

	b.WriteString("\n")
	var frows [][]string
	for _, f := range sw.Folders {
		frows = append(frows, []string{engine.DecimalBytes(f.Bytes), TildePath(m.Deps.Home, f.Path),
			fmt.Sprintf("%.0f%%", f.Confidence*100), f.Evidence})
	}
	if len(frows) == 0 {
		b.WriteString(t.block("Data folders", "", " No data folder was found for this program.\n"))
	} else {
		b.WriteString(t.block("Data folders", "", t.Grid(
			[]Column{{Title: "Size", Right: true}, {Title: "Folder", Flex: true}, {Title: "Match"}, {Title: "Why", Wrap: true}}, frows, w,
			func(_, c int) *lipgloss.Style {
				if c == 0 {
					return ptr(t.size)
				}
				return ptr(t.text)
			})))
	}

	b.WriteString("\n")
	var crows [][]string
	for _, op := range m.Items() {
		if slices.Contains(sw.CoveredBy, op.RecipeID) && op.Method != model.MethodAdvise {
			size := engine.DecimalBytes(op.Bytes.Value)
			if op.Bytes.Source == model.SourceUnknown {
				size = "unknown"
			}
			crows = append(crows, []string{op.RecipeID, op.ID, size, string(op.Risk)})
		}
	}
	switch {
	case len(sw.CoveredBy) == 0:
		b.WriteString(t.block("Cleanups", "", " No cleanup recipe covers this program. It is advisory only.\n"))
	case len(crows) == 0:
		b.WriteString(t.block("Cleanups", "", fmt.Sprintf(" Covered by %s, but the latest scan found nothing to clean for it.\n",
			strings.Join(sw.CoveredBy, ", "))))
	default:
		b.WriteString(t.block("Cleanups", "", t.Grid(
			[]Column{{Title: "Recipe", Flex: true}, {Title: "Suggestion"}, {Title: "Size", Right: true}, {Title: "Risk"}}, crows, w,
			func(_, c int) *lipgloss.Style {
				if c == 2 {
					return ptr(t.size)
				}
				return ptr(t.text)
			})))
	}
	return b.String()
}

// shownSoftware is the list the table shows for the filter in use.
func (m TuiModel) shownSoftware(res engine.SoftwareResult) []engine.SoftwareEntry {
	isNew := map[string]bool{}
	for _, id := range res.New {
		isNew[id] = true
	}
	var shown []engine.SoftwareEntry
	for _, sw := range res.Software {
		if m.softwareFilter.keep(sw, isNew[sw.ID]) {
			shown = append(shown, sw)
		}
	}
	return shown
}

// installedTable is the Installed software block, for the filter in use.
func (m TuiModel) installedTable(res engine.SoftwareResult) string {
	t, w := m.t, m.w-2
	if len(res.Software) == 0 {
		return t.block("Installed software", "", " No software was found. See the discovery block below.\n")
	}
	isNew := map[string]bool{}
	for _, id := range res.New {
		isNew[id] = true
	}
	shown := m.shownSoftware(res)
	title := fmt.Sprintf("· %s · %d of %d", m.softwareFilter.label(), len(shown), len(res.Software))
	if len(shown) == 0 {
		return t.block("Installed software", title, " Nothing matches this filter. Press f for the next one.\n")
	}
	rows := make([][]string, 0, len(shown))
	for i, sw := range shown {
		name := sw.Name
		if i == m.softwareCursor {
			name = "▸ " + name
		}
		data := "—"
		if len(sw.Folders) > 0 {
			data = engine.DecimalBytes(sw.Bytes)
		}
		cleanup := "advisory only"
		if n := len(sw.CoveredBy); n > 0 {
			cleanup = fmt.Sprintf("%d recipe(s)", n)
		}
		newMark := ""
		if isNew[sw.ID] {
			newMark = "new"
		}
		rows = append(rows, []string{name, kindLabel(sw), data, cleanup, newMark})
	}
	style := func(r, c int) *lipgloss.Style {
		sw := shown[r]
		switch c {
		case 0:
			return ptr(t.Title)
		case 2:
			if len(sw.Folders) > 0 {
				return ptr(t.size)
			}
			return ptr(t.muted)
		case 3:
			if len(sw.CoveredBy) > 0 {
				return ptr(t.Ok)
			}
			return ptr(t.muted)
		case 4:
			return ptr(t.warn)
		}
		return ptr(t.text)
	}
	return t.block("Installed software", title, t.Grid(
		[]Column{{Title: "Name", Flex: true}, {Title: "Kind"}, {Title: "Data", Right: true}, {Title: "Cleanup"}, {Title: "New"}}, rows, w, style))
}

// slimLine is a one-line piece of advice for a universal app, or "" when
// there is nothing to say. An unknown Intel size is said to be unknown.
func slimLine(sw engine.SoftwareEntry) string {
	if sw.Slim == nil || !sw.Slim.Universal {
		return ""
	}
	size := "size unknown"
	if sw.Slim.IntelBytes != nil {
		size = "about " + engine.DecimalBytes(*sw.Slim.IntelBytes)
	}
	return " Universal binary: the Intel (x86_64) part could be slimmed, " + size + ". Advice only; macdoc does not change apps.\n"
}
