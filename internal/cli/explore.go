package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"github.com/sagnik26/macdoc/internal/engine"
)

// exploreBarWidth is the width of the share bar in the explorer tables.
const exploreBarWidth = 16

// newExploreCmd builds `macdoc doctor explore [path]`: what the latest scan
// knows about one folder, biggest items first. Read-only; the path is only
// looked at, never a target.
func newExploreCmd(g *GlobalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "explore [path]",
		Short: "Where the space is inside one folder, from the latest scan (read-only)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			home, err := UserHome()
			if err != nil {
				return err
			}
			path := ""
			if len(args) == 1 {
				path = args[0]
			}
			res, err := engine.Explore(context.Background(), engine.ExploreOptions{Home: home, Path: path})
			if err != nil {
				return err
			}
			if g.json {
				return writeJSONWarn(cmd.OutOrStdout(), "doctor explore", res, nil)
			}
			return RenderExplore(cmd.OutOrStdout(), res, home)
		},
	}
}

// RenderExplore writes one explored folder in the human format.
func RenderExplore(w io.Writer, res engine.ExploreResult, home string) error {
	res = display(res) // names and paths come from the disk
	t := themeFor(w)
	var b strings.Builder
	b.WriteString(t.commandHeader("explore", res.Scan.ID, res.ScanAge))
	b.WriteString(exploreBlocks(t, 100, home, res, -1))
	b.WriteString(t.done("Nothing has been changed.") + "\n")
	_, err := io.WriteString(w, b.String())
	return err
}

// exploreBlocks is the explorer's body: the folder line, its entries and its
// biggest files. cursor is the highlighted entry, or -1 for none.
func exploreBlocks(t *Theme, w int, home string, res engine.ExploreResult, cursor int) string {
	var b strings.Builder
	title := TildePath(home, res.Path)
	if res.Bytes == nil {
		b.WriteString(t.block(title, "· "+res.Status, " "+res.Note+"\n"))
		return b.String()
	}
	note := "· " + engine.DecimalBytes(*res.Bytes) + " · " + res.Status
	body := ""
	if res.Note != "" {
		body = " " + res.Note + "\n"
	}
	if len(res.Entries) == 0 {
		body += " Nothing inside was kept by the scan.\n"
		return t.block(title, note, body)
	}
	rows := make([][]string, 0, len(res.Entries))
	for i, e := range res.Entries {
		name := e.Name
		if e.Kind == "dir" {
			name += "/"
		}
		if i == cursor {
			name = "▸ " + name
		}
		rows = append(rows, []string{name, engine.DecimalBytes(e.Bytes), shareBar(e.Share), fmt.Sprintf("%.1f%%", e.Share*100)})
	}
	style := func(r, c int) *lipgloss.Style {
		switch c {
		case 0:
			if res.Entries[r].Kind == "other" {
				return ptr(t.muted)
			}
			return ptr(t.Title)
		case 1:
			return ptr(t.size)
		case 2:
			return ptr(t.accent)
		}
		return ptr(t.text)
	}
	table := t.Grid([]Column{{Title: "Name", Flex: true}, {Title: "Size", Right: true}, {Title: "Share"}, {Title: "%", Right: true}}, rows, w, style)
	b.WriteString(t.block(title, note, body+table))
	if len(res.LargestFiles) > 0 {
		b.WriteString("\n")
		b.WriteString(t.block("Biggest files below", "", t.Grid(
			[]Column{{Title: "Size", Right: true}, {Title: "File", Flex: true}},
			fileRows(home, res.LargestFiles), w,
			func(_, c int) *lipgloss.Style {
				if c == 0 {
					return ptr(t.size)
				}
				return ptr(t.text)
			})))
	}
	return b.String()
}

func fileRows(home string, files []engine.ExploreEntry) [][]string {
	rows := make([][]string, 0, len(files))
	for _, f := range files {
		rows = append(rows, []string{engine.DecimalBytes(f.Bytes), TildePath(home, f.Path)})
	}
	return rows
}

// shareBar draws a share (0 to 1) as filled and empty cells.
func shareBar(share float64) string {
	n := int(share*exploreBarWidth + 0.5)
	n = max(0, min(exploreBarWidth, n))
	return strings.Repeat("█", n) + strings.Repeat("░", exploreBarWidth-n)
}

// explore is how the dashboard reads a folder. Tests replace it through
// TuiDeps.Explore.
func (d *TuiDeps) explore(ctx context.Context, path string) (engine.ExploreResult, error) {
	if d.Explore != nil {
		return d.Explore(ctx, d, path)
	}
	return engine.Explore(ctx, engine.ExploreOptions{Home: d.Home, Path: path})
}

// openExplore opens the explorer overlay on path ("" is home). It reads the
// stored scan only, so it is quick and never blocks on the disk.
func (m TuiModel) openExplore(path string) (tea.Model, tea.Cmd) {
	if m.Data == nil {
		m.Flash = "Check your Mac first: the explorer reads the latest scan."
		return m, nil
	}
	res, err := m.Deps.explore(context.Background(), path)
	if err != nil {
		m.Flash = "Couldn't open the explorer: " + err.Error()
		return m, nil
	}
	m.explore, m.exploreCursor = &res, 0
	m.Overlay, m.scroll = overlayExplore, 0
	return m, nil
}

// exploreKey handles the explorer's keys. ok is false for keys it doesn't use,
// which then fall through to the overlay's usual keys (esc and q close it).
func (m TuiModel) exploreKey(k string) (tea.Model, tea.Cmd, bool) {
	if m.explore == nil {
		return m, nil, false
	}
	n := len(m.explore.Entries)
	switch k {
	case "up", "k":
		m.exploreCursor = max(0, m.exploreCursor-1)
		m.scrollToMarker(m.exploreBody())
		return m, nil, true
	case "down", "j":
		m.exploreCursor = min(max(0, n-1), m.exploreCursor+1)
		m.scrollToMarker(m.exploreBody())
		return m, nil, true
	case "enter":
		if n == 0 || m.exploreCursor >= n {
			return m, nil, true
		}
		e := m.explore.Entries[m.exploreCursor]
		if e.Kind != "dir" {
			m.Flash = "Only folders open. The biggest files below are listed at the bottom."
			return m, nil, true
		}
		next, cmd := m.openExplore(e.Path)
		return next, cmd, true
	case "backspace", "h", "left":
		if m.explore.Parent == "" {
			return m, nil, true
		}
		next, cmd := m.openExplore(m.explore.Parent)
		return next, cmd, true
	}
	return m, nil, false
}

// exploreBody is the explorer overlay's text.
func (m TuiModel) exploreBody() string {
	if m.explore == nil {
		return ""
	}
	return "\n" + exploreBlocks(m.t, m.w-2, m.Deps.Home, display(*m.explore), m.exploreCursor)
}

// scrollToMarker scrolls the view so the line with the ▸ marker is on screen.
func (m *TuiModel) scrollToMarker(body string) {
	for i, line := range strings.Split(body, "\n") {
		if !strings.Contains(line, "▸ ") {
			continue
		}
		h := m.bodyHeight()
		switch {
		case i < m.scroll:
			m.scroll = i
		case i >= m.scroll+h:
			m.scroll = i - h + 1
		}
		return
	}
}
