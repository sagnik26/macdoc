package cli

import (
	"context"
	"errors"
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

// newFolderCmd builds the folder view of `macdoc explore [path]`: what the latest scan
// knows about one folder, biggest items first. Read-only; the path is only
// looked at, never a target.
func newFolderCmd(g *GlobalFlags) *cobra.Command {
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
				return writeJSONWarn(cmd.OutOrStdout(), "explore", res, nil)
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

// openExplore opens the explorer page on path ("" is home). It reads the
// stored scan only, so it is quick, never blocks on the disk and needs no
// dashboard data of its own.
func (m TuiModel) openExplore(path string) (tea.Model, tea.Cmd) {
	res, err := m.Deps.explore(context.Background(), path)
	if err != nil {
		m.Flash = "Couldn't open the explorer: " + err.Error()
		return m, nil
	}
	m.explore, m.treeRoot, m.treeCursor = &res, newTree(res), 0
	m.Overlay, m.scroll = overlayExplore, 0
	return m, nil
}

// exploreKey handles the folder tree's keys. ok is false for keys it doesn't
// use, which then fall through to the page's usual keys (esc and q close it).
func (m TuiModel) exploreKey(k string) (tea.Model, tea.Cmd, bool) {
	if m.explore == nil || m.treeRoot == nil {
		return m, nil, false
	}
	rows := m.treeRoot.visible()
	m.treeCursor = max(0, min(m.treeCursor, len(rows)-1))
	var cur *treeNode
	if len(rows) > 0 {
		cur = rows[m.treeCursor]
	}
	switch k {
	case "up", "k":
		m.treeCursor = max(0, m.treeCursor-1)
	case "down", "j":
		m.treeCursor = min(max(0, len(rows)-1), m.treeCursor+1)
	case "enter", "right", "l":
		if cur == nil {
			return m, nil, true
		}
		if !cur.expandable() {
			m.Flash = "Only folders open. Files and the “other” rows have nothing inside."
			return m, nil, true
		}
		if k == "enter" && cur.open {
			cur.open = false
		} else {
			m.openNode(cur)
		}
	case "left", "h":
		switch {
		case cur == nil:
		case cur.open:
			cur.open = false
		case cur.parent != nil && cur.parent != m.treeRoot:
			m.treeCursor = m.treeRoot.indexOf(cur.parent)
		}
	case "backspace":
		if m.explore.Parent == "" {
			return m, nil, true
		}
		next, cmd := m.openExplore(m.explore.Parent)
		return next, cmd, true
	default:
		return m, nil, false
	}
	m.scrollToLine(m.exploreHead() + m.treeCursor)
	return m, nil, true
}

// openNode opens a folder of the tree, reading what is inside it from the
// scan the first time.
func (m TuiModel) openNode(n *treeNode) {
	if !n.loaded {
		res, err := m.Deps.explore(context.Background(), n.e.Path)
		n.fill(res, err)
	}
	n.open = true
}

// exploreHead is how many lines of the page come before the first row of the
// tree: a blank line, the folder's title, a blank line and the note, if any.
func (m TuiModel) exploreHead() int {
	if m.explore != nil && m.explore.Note != "" {
		return 4
	}
	return 3
}

// exploreBody is the explorer page's text: the folder as a tree.
func (m TuiModel) exploreBody() string {
	if m.explore == nil || m.treeRoot == nil {
		return ""
	}
	res := display(*m.explore)
	t := m.t
	title := TildePath(m.Deps.Home, res.Path)
	var b strings.Builder
	b.WriteString("\n")
	if res.Bytes == nil {
		b.WriteString(" " + t.heading(title) + " " + t.muted.Render("· "+res.Status) + "\n\n")
		b.WriteString(" " + t.text.Render(res.Note) + "\n")
		return b.String()
	}
	b.WriteString(" " + t.heading(title) + " " + t.muted.Render("· "+engine.DecimalBytes(*res.Bytes)+" · "+res.Status+" · scan "+res.ScanAge) + "\n\n")
	if res.Note != "" {
		b.WriteString(" " + t.muted.Render(res.Note) + "\n")
	}
	b.WriteString(m.treeView(m.w - 2))
	return b.String()
}

// scrollToMarker scrolls the view so the line with the ▸ marker is on screen.
func (m *TuiModel) scrollToMarker(body string) {
	for i, line := range strings.Split(body, "\n") {
		if strings.Contains(line, "▸ ") {
			m.scrollToLine(i)
			return
		}
	}
}

// scrollToLine scrolls the page so line i is on screen.
func (m *TuiModel) scrollToLine(i int) {
	h := m.bodyHeight()
	switch {
	case i < m.scroll:
		m.scroll = i
	case i >= m.scroll+h:
		m.scroll = i - h + 1
	}
	m.scroll = max(0, m.scroll)
}

// newExploreCmd builds `macdoc explore [folder]`: where the space is. By
// default it is the folder explorer; --growth shows what grew between scans
// and --projects lists projects with their build folders. All read-only.
func newExploreCmd(g *GlobalFlags) *cobra.Command {
	folder, trend, projects := newFolderCmd(g), newTrendCmd(g), newProjectsCmd(g)
	var growth, byProject bool
	cmd := &cobra.Command{
		Use:   "explore [folder]",
		Short: "Where your space is: folders, what grew, and project build folders (read-only)",
		Long: `macdoc explore shows where the space is, from the latest scan:

  macdoc explore [folder]      the biggest items inside one folder
  macdoc explore --growth      what grew since an earlier scan
  macdoc explore --projects    your projects, idle days, build folders and git state

It reads stored scans only and never takes a folder as a cleanup target.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch {
			case growth && byProject:
				return errors.New("use --growth or --projects, not both")
			case (growth || byProject) && len(args) > 0:
				return errors.New("--growth and --projects take no folder")
			case growth:
				return trend.RunE(cmd, nil)
			case byProject:
				return projects.RunE(cmd, nil)
			}
			return folder.RunE(cmd, args)
		},
	}
	cmd.Flags().BoolVar(&growth, "growth", false, "show what grew since an earlier scan")
	cmd.Flags().BoolVar(&byProject, "projects", false, "list projects with idle days, build folders and git state")
	cmd.Flags().AddFlagSet(trend.Flags())    // --days
	cmd.Flags().AddFlagSet(projects.Flags()) // --idle
	return cmd
}
