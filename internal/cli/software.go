package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"github.com/sagnik26/macdoc/internal/engine"
)

// softwareListLimit caps the rows shown for each list; --json has them all.
const softwareListLimit = 25

// newSoftwareCmd builds `macdoc retire`: what is installed, which
// folders belong to it, and what is new since the last scan (read-only).
func newRetireCmd(g *GlobalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "retire",
		Short: "Installed apps and the data that belongs to them: what you can retire (read-only)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := UserHome()
			if err != nil {
				return err
			}
			errOut := cmd.ErrOrStderr()
			tty := !g.json && !g.quiet && IsTerminal(errOut)
			opts := engine.SoftwareOptions{Home: home, Runner: Runner}
			if tty {
				opts.Progress = func(done, total int, source string) {
					_, _ = fmt.Fprintf(statusOut(errOut), "\r\033[K%s", SourceStatusLine(done, total, source))
				}
			}
			res, err := engine.Software(context.Background(), opts)
			if tty {
				_, _ = fmt.Fprint(errOut, "\r\033[K")
			}
			if err != nil {
				return err
			}
			if g.json {
				return writeJSONWarn(cmd.OutOrStdout(), "retire", res, res.Warnings)
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
	b.WriteString(t.commandHeader("retire", res.Scan.ID, res.ScanAge))

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
			fmt.Fprintf(&b, "  … and %d more (macdoc retire --json lists them all)\n", len(res.Software)-i)
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

// SoftwareProgressMsg says how many discovery sources have finished while the
// dashboard reads the software inventory.
type SoftwareProgressMsg struct {
	Done, Total int
	Source      string
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
	return engine.Software(ctx, engine.SoftwareOptions{Home: d.Home, Runner: Runner,
		Progress: func(done, total int, source string) {
			d.post(SoftwareProgressMsg{Done: done, Total: total, Source: source})
		}})
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
	m.softwareDone, m.softwareTotal, m.softwareSource = 0, 0, ""
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
	if m.softwareTotal > 0 {
		pct := float64(m.softwareDone) / float64(m.softwareTotal)
		tail := fmt.Sprintf("%d of %d sources", m.softwareDone, m.softwareTotal)
		if m.softwareSource != "" {
			tail += " · " + m.softwareSource
		}
		fmt.Fprintf(&b, "\n  %s\n\n  %s\n\n", m.barView(pct, true), t.hint(tail))
	} else {
		fmt.Fprintf(&b, "\n  %s\n\n  %s\n\n", m.barView(sweep(time.Since(m.softwareStarted)), false), t.hint("starting"))
	}
	b.WriteString("  " + t.muted.Render("Checking /Applications, Homebrew, npm, the programs on PATH, and the folders in your home.") + "\n")
	b.WriteString("  " + t.muted.Render("This can take a little while. esc closes this view; the list will be kept for next time.") + "\n")
	return b.String()
}

// softwareFailedView says why the inventory couldn't be read.
func (m TuiModel) softwareFailedView() string {
	return "\n  " + m.t.warn.Render("Couldn't read installed software: "+m.softwareErr.Error()) + "\n  " +
		m.t.muted.Render("esc to close, then run /retire again.") + "\n"
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
	m.softwareCursor = 0
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
	filterDeletable softwareFilter = iota
	filterNew
	filterAll
)

func (f softwareFilter) label() string {
	return [...]string{"deletable", "new", "all"}[f]
}

func (f softwareFilter) next() softwareFilter { return (f + 1) % 3 }

// keep reports whether the filter shows sw. Deletable programs are the apps and the
// ones with a data folder: the ones Enter can delete something for.
func (f softwareFilter) keep(sw engine.SoftwareEntry, isNew bool) bool {
	switch f {
	case filterDeletable:
		return len(sw.Folders) > 0 || sw.Kind == "app"
	case filterNew:
		return isNew
	}
	return true
}

// softwareSummary is the top of the view: the headline, what is new, and the
// largest folder no software explains.
func (m TuiModel) softwareSummary(res engine.SoftwareResult) string {
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
	lines := []string{softwareHeadline(res.Software), newLine}
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
	n := 0
	if m.software != nil {
		n = len(m.shownSoftware(display(*m.software)))
	}
	switch k {
	case "f":
		m.softwareFilter = m.softwareFilter.next()
		m.softwareCursor, m.scroll = 0, 0
		return m, nil, true
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
			next, cmd := m.startRetire()
			return next, cmd, true
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
		newMark := ""
		if isNew[sw.ID] {
			newMark = "new"
		}
		rows = append(rows, []string{name, kindLabel(sw), data, newMark})
	}
	style := func(r, c int) *lipgloss.Style {
		if r == m.softwareCursor {
			return ptr(t.selectedRow(t.text))
		}
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
			return ptr(t.warn)
		}
		return ptr(t.text)
	}
	return t.block("Installed software", title, t.Grid(
		[]Column{{Title: "Name", Flex: true}, {Title: "Kind"}, {Title: "Data", Right: true}, {Title: "New"}}, rows, w, style))
}
