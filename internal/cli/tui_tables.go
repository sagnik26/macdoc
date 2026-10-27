package cli

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/charmbracelet/x/ansi"

	"macdoc/internal/engine"
	"macdoc/internal/model"
	"macdoc/internal/projects"
)

// Every dashboard list is a bordered table with a header row. Meaning never
// rests on color alone: risk and severity are spelled out, sizes are
// right-aligned, and the selected row carries a ▶ marker as well as a
// highlight.

// column describes one table column.
type column struct {
	title string
	right bool // right-align (sizes, counts)
	wrap  bool // long text that wraps when space is short
	flex  bool // long text that is cut (…) when space is short
}

// cellStyle picks the style of a data cell; nil means the default text.
type cellStyle func(row, col int) *lipgloss.Style

// grid renders rows as a table at most width cells wide.
func (t *theme) grid(cols []column, rows [][]string, width int, style cellStyle) string {
	headers := make([]string, len(cols))
	for i, c := range cols {
		headers[i] = c.title
	}
	rows = fitColumns(cols, rows, width)
	return table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(t.faint).
		BorderRow(false).
		Headers(headers...).
		Rows(rows...).
		Wrap(true).
		StyleFunc(func(row, col int) lipgloss.Style {
			var s lipgloss.Style
			switch {
			case row == table.HeaderRow:
				s = t.title.Underline(t.on)
			case style != nil && style(row, col) != nil:
				s = *style(row, col)
			default:
				s = t.text
			}
			s = s.Padding(0, 1)
			if cols[col].right {
				s = s.Align(lipgloss.Right)
			}
			return s
		}).Render() + "\n"
}

// fitColumns shrinks only the wrap and flex columns so the table fits width:
// sizes, IDs, risk labels and counts always show in full. Wrap columns are
// word-wrapped, flex columns cut with "…".
func fitColumns(cols []column, rows [][]string, width int) [][]string {
	widths := make([]int, len(cols))
	for i, c := range cols {
		widths[i] = lipgloss.Width(c.title)
	}
	for _, r := range rows {
		for i, cell := range r {
			widths[i] = max(widths[i], lipgloss.Width(cell))
		}
	}
	total := len(cols) + 1 // borders
	shrinkable := 0
	for i, c := range cols {
		total += widths[i] + 2 // padding
		if c.wrap || c.flex {
			shrinkable += widths[i]
		}
	}
	excess := total - width
	if excess <= 0 || shrinkable == 0 {
		return rows
	}
	// Each shrinkable column gives up width in proportion to its slack
	// above a floor: 8 cells or its title, and for a wrapping column its longest word
	// (so wrapping never splits a word). Width a column can't give passes
	// to the others.
	limit, floor := make([]int, len(cols)), make([]int, len(cols))
	for i, c := range cols {
		limit[i], floor[i] = widths[i], widths[i]
		if c.wrap || c.flex {
			floor[i] = max(min(widths[i], 8), lipgloss.Width(c.title))
		}
	}
	for _, r := range rows {
		for i, cell := range r {
			if cols[i].wrap {
				// Split on plain spaces only: a no-break space joins words.
				for _, word := range strings.Split(ansi.Strip(cell), " ") {
					floor[i] = min(widths[i], max(floor[i], lipgloss.Width(word)))
				}
			}
		}
	}
	shrink := func() {
		for excess > 0 {
			slack := 0
			for i := range cols {
				slack += limit[i] - floor[i]
			}
			if slack == 0 {
				return
			}
			for i := range cols {
				cut := min(limit[i]-floor[i], (excess*(limit[i]-floor[i])+slack-1)/slack)
				limit[i] -= cut
				excess -= cut
			}
		}
	}
	shrink()
	if excess > 0 { // still too wide: let wrapping columns split long words
		for i, c := range cols {
			if c.wrap {
				floor[i] = max(min(widths[i], 8), lipgloss.Width(c.title))
			}
		}
		shrink()
	}
	out := make([][]string, len(rows))
	for r, row := range rows {
		out[r] = make([]string, len(row))
		for i, cell := range row {
			switch {
			case lipgloss.Width(cell) <= limit[i]:
				out[r][i] = cell
			case cols[i].wrap:
				out[r][i] = wrapANSI(cell, limit[i])
			default:
				out[r][i] = truncateANSI(cell, limit[i])
			}
		}
	}
	return out
}

// block is a heading with an optional muted note, then content.
func (t *theme) block(title, note, content string) string {
	h := " " + t.heading(title)
	if note != "" {
		h += " " + t.muted.Render(note)
	}
	return h + "\n" + indentLines(content, " ") + "\n"
}

func riskLabel(r model.Risk) string {
	if r == model.RiskAdvise {
		return "ADVICE"
	}
	return string(r)
}

func severityLabel(s engine.Severity) string {
	switch s {
	case engine.SeverityCritical:
		return "Critical"
	case engine.SeverityWarn:
		return "Warning"
	}
	return "Info"
}

func (t *theme) severityStyle(s engine.Severity) lipgloss.Style {
	switch s {
	case engine.SeverityCritical:
		return t.bad.Bold(true)
	case engine.SeverityWarn:
		return t.warn
	}
	return t.muted
}

func ptr(s lipgloss.Style) *lipgloss.Style { return &s }

// share is a size relative to a total, as a percentage.
func share(part, total int64) string {
	if total <= 0 {
		return "—"
	}
	return fmt.Sprintf("%.0f%%", 100*float64(part)/float64(total))
}

// --- tabs ---

func (m tuiModel) overviewTables() string {
	t, d, w := m.t, m.data, m.w-2
	res := d.res
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(t.block("Your Mac", "", m.vitalsTable(w)))

	// What matters most: at most 3 here, all of them on the Health tab.
	b.WriteString("\n")
	fs := res.Health.Findings
	if len(fs) == 0 {
		b.WriteString(t.block("What matters most", "", " "+t.ok.Render("Nothing urgent.")+" Disk, memory and swap look healthy.\n"))
	} else {
		note := ""
		if len(fs) > 3 {
			note = fmt.Sprintf("(top 3 of %d · all on the Health tab)", len(fs))
			fs = fs[:3]
		}
		b.WriteString(t.block("What matters most", note, m.findingsTable(fs, w, false)))
	}

	// Reclaimable, per tier.
	ops := res.Opportunities.Opportunities
	var rows [][]string
	var risks []model.Risk
	for _, tier := range engine.TierOrder {
		tierOps := opsInTier(ops, tier)
		if len(tierOps) == 0 {
			continue
		}
		rows = append(rows, []string{riskLabel(tier), shortTierName(tier), tierTotal(res.Opportunities.Totals[tier], tierOps),
			fmt.Sprint(len(tierOps)), tierSummary(tierOps, m.deps.cat)})
		risks = append(risks, tier)
	}
	b.WriteString("\n")
	if len(rows) == 0 {
		b.WriteString(t.block("Reclaimable", "", " Nothing to reclaim was found in the latest scan.\n"))
	} else {
		cols := []column{{title: "Risk"}, {title: "Tier"}, {title: "Size", right: true}, {title: "Items", right: true}, {title: "Biggest items", wrap: true}}
		b.WriteString(t.block("Reclaimable", "(details on the Suggestions tab)", t.grid(cols, rows, w, func(r, c int) *lipgloss.Style {
			switch c {
			case 0:
				return ptr(t.riskStyle(risks[r]).Bold(true))
			case 2:
				return ptr(t.size)
			case 4:
				return ptr(t.muted)
			}
			return nil
		})))
	}

	// Where the space is.
	b.WriteString("\n")
	b.WriteString(t.block("Where your space is", fmt.Sprintf("(Data volume, %s measured)", engine.DecimalBytes(res.Scan.TotalBytes)),
		m.foldersTable(res.TopLevel, overviewTopLevel, w)))
	if du := res.DataVolume; du.Source != model.SourceUnknown && du.Value > res.Scan.TotalBytes+du.Value/50 {
		b.WriteString("  " + t.muted.Render(engine.DecimalBytes(du.Value-res.Scan.TotalBytes)+" more is not visible to the scan (needs Full Disk Access).") + "\n")
	}
	if res.ScanStale {
		b.WriteString("\n  " + t.warn.Render("Note:") + fmt.Sprintf(" the scan is %s old. Press r to rescan.\n", strings.TrimSuffix(res.ScanAge, " ago")))
	}
	return b.String()
}

// vitalsTable is disk, memory, swap and snapshots as Metric | Value.
func (m tuiModel) vitalsTable(w int) string {
	t, rep := m.t, m.data.res.Health.Report
	var rows [][]string
	var styles []*lipgloss.Style
	d := rep.Disk
	if d.ContainerTotalBytes > 0 {
		used := d.ContainerTotalBytes - d.ContainerFreeBytes
		v := fmt.Sprintf("%s  %s used of %s · %s free (%s)", t.meter(used, d.ContainerTotalBytes, 20), engine.DecimalBytes(used),
			engine.DecimalBytes(d.ContainerTotalBytes), engine.DecimalBytes(d.ContainerFreeBytes), share(d.ContainerFreeBytes, d.ContainerTotalBytes))
		if d.PurgeableBytes.Value > 0 {
			v += fmt.Sprintf(" · +%s purgeable", engine.DecimalBytes(d.PurgeableBytes.Value))
		}
		rows = append(rows, []string{"Disk", v})
		styles = append(styles, nil)
	} else {
		rows = append(rows, []string{"Disk", "unmeasured (see Health)"})
		styles = append(styles, ptr(t.muted))
	}
	mem := rep.Memory
	if mem.TotalBytes > 0 {
		v := engine.BinaryBytes(mem.TotalBytes)
		if mem.FreePercent >= 0 {
			v += fmt.Sprintf(" · %d%% free", mem.FreePercent)
		}
		rows = append(rows, []string{"Memory", v})
		styles = append(styles, nil)
	}
	if mem.Pressure != "" {
		st, label := t.ok, "Normal"
		switch strings.ToLower(string(mem.Pressure)) {
		case "warn", "warning":
			st, label = t.warn, "Elevated"
		case "critical":
			st, label = t.bad.Bold(true), "Critical"
		}
		rows = append(rows, []string{"Memory pressure", label})
		styles = append(styles, ptr(st))
	}
	if rep.Swap.TotalBytes > 0 || rep.Swap.UsedBytes > 0 {
		rows = append(rows, []string{"Swap used", engine.BinaryBytes(rep.Swap.UsedBytes)})
		styles = append(styles, nil)
	}
	rows = append(rows, []string{"Snapshots", fmt.Sprintf("%d local Time Machine snapshot(s)", rep.Snapshots.Count)})
	styles = append(styles, nil)
	return t.grid([]column{{title: "Metric"}, {title: "Value", wrap: true}}, rows, w, func(r, c int) *lipgloss.Style {
		if c == 0 {
			return ptr(t.muted)
		}
		return styles[r]
	})
}

func (m tuiModel) findingsTable(fs []engine.Finding, w int, details bool) string {
	t := m.t
	cols := []column{{title: "#", right: true}, {title: "Level"}, {title: "Finding", wrap: true}}
	if details {
		cols = append(cols, column{title: "Why it matters", wrap: true})
	}
	rows := make([][]string, len(fs))
	for i, f := range fs {
		rows[i] = []string{fmt.Sprint(i + 1), severityLabel(f.Severity), f.Title}
		if details {
			rows[i] = append(rows[i], f.Detail)
		}
	}
	return t.grid(cols, rows, w, func(r, c int) *lipgloss.Style {
		switch c {
		case 0:
			return ptr(t.muted)
		case 1:
			return ptr(t.severityStyle(fs[r].Severity))
		case 3:
			return ptr(t.muted)
		}
		return nil
	})
}

// foldersTable lists folders with size and share of the largest total.
func (m tuiModel) foldersTable(nodes []model.Node, limit, w int) string {
	t := m.t
	if len(nodes) == 0 {
		return " —\n"
	}
	var total int64
	for _, n := range nodes {
		total += n.Bytes
	}
	var rows [][]string
	for i, n := range nodes {
		if limit > 0 && i == limit {
			break
		}
		rows = append(rows, []string{n.Path, engine.DecimalBytes(n.Bytes), share(n.Bytes, total)})
	}
	return t.grid([]column{{title: "Folder", flex: true}, {title: "Size", right: true}, {title: "Share", right: true}}, rows, w,
		func(_, c int) *lipgloss.Style {
			switch c {
			case 1:
				return ptr(t.size)
			case 2:
				return ptr(t.muted)
			}
			return nil
		})
}

func (m tuiModel) healthTables() string {
	t, w := m.t, m.w-2
	h := m.data.res.Health
	rep := h.Report
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(t.block("Your Mac", "", m.vitalsTable(w)))
	b.WriteString("\n")
	if len(h.Findings) == 0 {
		b.WriteString(t.block("Findings", "", " "+t.ok.Render("Nothing urgent.")+" Disk, memory and swap look healthy.\n"))
	} else {
		b.WriteString(t.block("Findings", "", m.findingsTable(h.Findings, w, true)))
	}
	if len(rep.TopProcesses) > 0 {
		var rows [][]string
		for _, p := range rep.TopProcesses {
			rows = append(rows, []string{p.Name, engine.BinaryBytes(p.RSSBytes), fmt.Sprint(max(1, p.Count))})
		}
		b.WriteString("\n")
		b.WriteString(t.block("Top memory users", "", t.grid(
			[]column{{title: "App or tool", flex: true}, {title: "Memory", right: true}, {title: "Processes", right: true}}, rows, w,
			func(_, c int) *lipgloss.Style {
				if c == 1 {
					return ptr(t.size)
				}
				return nil
			})))
	}
	if len(rep.Warnings) > 0 {
		var rows [][]string
		for _, wn := range rep.Warnings {
			rows = append(rows, []string{wn})
		}
		b.WriteString("\n")
		b.WriteString(t.block("Could not measure", "", t.grid([]column{{title: "What and why", wrap: true}}, rows, w,
			func(_, _ int) *lipgloss.Style { return ptr(t.muted) })))
	}
	return b.String()
}

func (m tuiModel) spaceTables() string {
	t, w := m.t, m.w-2
	sp := m.data.space
	var b strings.Builder
	b.WriteString("\n")

	if len(sp.Categories) == 0 {
		b.WriteString(t.block("Reclaimable, by category", "", " Nothing to reclaim was found in the latest scan.\n"))
	} else {
		cols := []column{{title: "Category", flex: true}, {title: "Size", right: true}, {title: "Items", right: true}}
		for _, r := range engine.TierOrder {
			cols = append(cols, column{title: riskLabel(r), right: true})
		}
		var rows [][]string
		for _, c := range sp.Categories {
			size := engine.DecimalBytes(c.Bytes)
			if c.UnknownCount > 0 {
				size += " + unknown"
			}
			row := []string{c.Category, size, fmt.Sprint(c.Count)}
			for _, r := range engine.TierOrder {
				if v, ok := c.ByRisk[r]; ok {
					row = append(row, engine.DecimalBytes(v))
				} else {
					row = append(row, "—")
				}
			}
			rows = append(rows, row)
		}
		b.WriteString(t.block("Reclaimable, by category", "", t.grid(cols, rows, w, func(r, c int) *lipgloss.Style {
			switch {
			case c == 1:
				return ptr(t.size)
			case c >= 3 && rows[r][c] != "—":
				return ptr(t.riskStyle(engine.TierOrder[c-3]))
			case c >= 2:
				return ptr(t.muted)
			}
			return nil
		})))
	}

	if len(sp.SystemData) > 0 {
		var rows [][]string
		for _, p := range sp.SystemData {
			size, src, explain := "—", "unknown", p.Explain
			switch p.Bytes.Source {
			case model.SourceUnknown:
				if p.Bytes.Reason != "" {
					explain = "Size unknown: " + p.Bytes.Reason + ". " + explain
				}
			case model.SourceEstimated:
				size, src = "~"+engine.DecimalBytes(p.Bytes.Value), "estimated"
			default:
				size, src = engine.DecimalBytes(p.Bytes.Value), string(p.Bytes.Source)
			}
			rows = append(rows, []string{p.Name, size, src, explain})
		}
		b.WriteString("\n")
		b.WriteString(t.block(`"System Data", decoded`, "", t.grid(
			[]column{{title: "Part", wrap: true}, {title: "Size", right: true}, {title: "Source"}, {title: "What it is", wrap: true}}, rows, w,
			func(r, c int) *lipgloss.Style {
				switch c {
				case 1:
					if rows[r][1] == "—" {
						return ptr(t.muted)
					}
					return ptr(t.size)
				case 2, 3:
					return ptr(t.muted)
				}
				return nil
			})))
	}

	b.WriteString("\n")
	if len(sp.Large) == 0 {
		b.WriteString(t.block("Large items with no recipe", "(≥ 1 GB, advice only)", " None.\n"))
	} else {
		b.WriteString(t.block("Large items with no recipe", "(≥ 1 GB, advice only: review them yourself)", m.foldersTable(sp.Large, 0, w)))
	}
	return b.String()
}

func (m tuiModel) toolchainTables() string {
	t, w := m.t, m.w-2
	groups := m.data.dev.Toolchains
	if len(groups) == 0 {
		return "\n  " + t.muted.Render("Nothing to reclaim was found in the latest scan.")
	}
	var b strings.Builder
	b.WriteString("\n")
	var rows [][]string
	for _, g := range groups {
		biggest := ""
		if len(g.Opportunities) > 0 {
			top := g.Opportunities[0]
			for _, op := range g.Opportunities {
				if op.Bytes.Value > top.Bytes.Value {
					top = op
				}
			}
			biggest = top.Title + " (" + sizeCell(top.Bytes) + ")"
		}
		rows = append(rows, []string{g.Toolchain, tierTotal(g.Bytes, g.Opportunities), fmt.Sprint(len(g.Opportunities)), biggest})
	}
	b.WriteString(t.block("By toolchain", "", t.grid(
		[]column{{title: "Toolchain"}, {title: "Size", right: true}, {title: "Items", right: true}, {title: "Biggest item", flex: true}}, rows, w,
		func(_, c int) *lipgloss.Style {
			switch c {
			case 0:
				return ptr(t.accent.Bold(true))
			case 1:
				return ptr(t.size)
			case 2, 3:
				return ptr(t.muted)
			}
			return nil
		})))

	for _, g := range groups {
		b.WriteString("\n")
		b.WriteString(t.block(g.Toolchain, "· "+tierTotal(g.Bytes, g.Opportunities), m.opsTable(g.Opportunities, -1, w, false)))
	}
	return b.String()
}

// opsTable lists suggestions; sel is the selected row (-1 for none).
// compact drops the restore column (the details beside the list show it).
func (m tuiModel) opsTable(ops []model.Opportunity, sel, w int, compact bool) string {
	t := m.t
	cols := []column{{title: " "}, {title: "ID"}, {title: "Size", right: true}, {title: "Risk"}, {title: "Suggestion", flex: true}, {title: "Restore", flex: true}}
	rows := make([][]string, len(ops))
	for i, op := range ops {
		mark := " "
		if i == sel {
			mark = "▶"
		}
		rows[i] = []string{mark, op.ID, sizeCell(op.Bytes), riskLabel(op.Risk), op.Title, truncate(restoreNote(op), 30)}
	}
	if compact {
		cols = cols[:5]
		for i := range rows {
			rows[i] = rows[i][:5]
		}
	}
	if sel < 0 {
		cols, rows = cols[1:], dropFirst(rows)
	}
	off := 0
	if sel < 0 {
		off = 1
	}
	return t.grid(cols, rows, w, func(r, c int) *lipgloss.Style {
		c += off
		var s lipgloss.Style
		switch c {
		case 0:
			s = t.brand.Bold(true)
		case 1:
			s = t.id
		case 2:
			s = t.size
		case 3:
			s = t.riskStyle(ops[r].Risk).Bold(true)
		case 5:
			s = t.muted
		default:
			s = t.text
		}
		if r == sel && t.on {
			s = s.Background(colSelect).Bold(true)
		}
		return &s
	})
}

func dropFirst(rows [][]string) [][]string {
	out := make([][]string, len(rows))
	for i, r := range rows {
		out[i] = r[1:]
	}
	return out
}

// projectTables is the Projects view: every project with build artifacts,
// how long it has been idle, its git state and its biggest folders.
func (m tuiModel) projectTables() string {
	t, w := m.t, m.w-2
	pr := m.data.projects
	if len(pr.Projects) == 0 {
		return "\n  " + t.muted.Render("No projects with build artifacts were found in the latest scan.")
	}
	var b strings.Builder
	b.WriteString("\n")
	var rows [][]string
	var total int64
	for _, p := range pr.Projects {
		total += p.ArtifactBytes
		name := tildePath(m.deps.home, p.Root)
		if p.Protected {
			name += " (protected)"
		}
		idle := "unknown"
		if p.IdleDays >= 0 {
			idle = fmt.Sprintf("%d d · %s", p.IdleDays, p.ActivitySource)
		}
		parts := make([]string, 0, 3)
		for _, a := range p.Artifacts[:min(3, len(p.Artifacts))] {
			parts = append(parts, strings.ReplaceAll(a.Rel+" "+engine.DecimalBytes(a.Bytes), " ", "\u00a0")) // wrap between folders, not inside one
		}
		if extra := len(p.Artifacts) - 3; extra > 0 {
			parts = append(parts, fmt.Sprintf("+%d more", extra))
		}
		rows = append(rows, []string{name, idle, engine.DecimalBytes(p.ArtifactBytes), gitLabel(p.Git), strings.Join(parts, " · ")})
	}
	git := func(r int) *lipgloss.Style {
		switch pr.Projects[r].Git {
		case projects.GitClean:
			return ptr(t.ok)
		case projects.GitDirty:
			return ptr(t.warn)
		}
		return ptr(t.muted)
	}
	b.WriteString(t.block("Projects", fmt.Sprintf("· %s of build folders in %d", engine.DecimalBytes(total), len(pr.Projects)), t.grid(
		[]column{{title: "Project", wrap: true}, {title: "Idle"}, {title: "Size", right: true}, {title: "Git"}, {title: "Biggest folders", wrap: true}}, rows, w,
		func(r, c int) *lipgloss.Style {
			switch c {
			case 0:
				if pr.Projects[r].Protected {
					return ptr(t.muted)
				}
				return ptr(t.title)
			case 1:
				if pr.Projects[r].IdleDays >= 90 {
					return ptr(t.ok)
				}
				return ptr(t.text)
			case 2:
				return ptr(t.size)
			case 3:
				return git(r)
			}
			return ptr(t.muted)
		})))
	b.WriteString("  " + t.wrap(t.muted.Render("Idle is the newest of the last commit, a file change or a build. Projects in protected folders (Desktop, Documents, …) are listed only."), 2) + "\n")

	var gone [][]string
	for _, d := range pr.DerivedData {
		if d.Missing {
			gone = append(gone, []string{tildePath(m.deps.home, d.Workspace), engine.DecimalBytes(d.Bytes)})
		}
	}
	if len(gone) > 0 {
		b.WriteString("\n")
		b.WriteString(t.block("DerivedData of deleted projects", "", t.grid(
			[]column{{title: "Workspace", flex: true}, {title: "Size", right: true}}, gone, w,
			func(_, c int) *lipgloss.Style {
				if c == 1 {
					return ptr(t.size)
				}
				return ptr(t.muted)
			})))
	}
	return b.String()
}

// gitLabel is a project's git state as a word.
func gitLabel(g projects.GitState) string {
	switch g {
	case projects.GitClean:
		return "clean"
	case projects.GitDirty:
		return "uncommitted"
	case projects.GitNone:
		return "not in git"
	}
	return "not checked"
}
