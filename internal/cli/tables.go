package cli

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/charmbracelet/x/ansi"

	"github.com/sagnik26/macdoc/internal/engine"
	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/projects"
)

// Every dashboard list is a bordered table with a header row. Meaning never
// rests on color alone: risk and severity are spelled out, sizes are
// right-aligned, and the selected row carries a ▶ marker as well as a
// highlight.

// Column describes one table Column.
type Column struct {
	Title string
	Right bool // right-align (sizes, counts)
	Wrap  bool // long text that wraps when space is short
	Flex  bool // long text that is cut (…) when space is short
}

// cellStyle picks the style of a data cell; nil means the default text.
type cellStyle func(row, col int) *lipgloss.Style

// Grid renders rows as a table at most width cells wide.
func (t *Theme) Grid(cols []Column, rows [][]string, width int, style cellStyle) string {
	headers := make([]string, len(cols))
	for i, c := range cols {
		headers[i] = c.Title
	}
	rows = fitColumns(cols, rows, width)
	// The Bubbles README table: a square gray outline, one rule under the header and
	// no lines between columns.
	return table.New().
		Border(lipgloss.NormalBorder()).
		BorderStyle(t.faint).
		BorderRow(false).
		BorderColumn(false).
		BorderHeader(true).
		Headers(headers...).
		Rows(rows...).
		Wrap(true).
		StyleFunc(func(row, col int) lipgloss.Style {
			var s lipgloss.Style
			switch {
			case row == table.HeaderRow:
				s = t.text // not bold: the rule under it sets it apart
			case style != nil && style(row, col) != nil:
				s = *style(row, col)
			default:
				s = t.text
			}
			s = s.Padding(0, 1)
			if cols[col].Right {
				s = s.Align(lipgloss.Right)
			}
			return s
		}).Render() + "\n"
}

// fitColumns shrinks only the wrap and flex columns so the table fits width:
// sizes, IDs, risk labels and counts always show in full. Wrap columns are
// word-wrapped, flex columns cut with "…".
func fitColumns(cols []Column, rows [][]string, width int) [][]string {
	widths := make([]int, len(cols))
	for i, c := range cols {
		widths[i] = lipgloss.Width(c.Title)
	}
	for _, r := range rows {
		for i, cell := range r {
			widths[i] = max(widths[i], lipgloss.Width(cell))
		}
	}
	total := 2 // the outline
	shrinkable := 0
	for i, c := range cols {
		total += widths[i] + 2 // padding
		if c.Wrap || c.Flex {
			shrinkable += widths[i]
		}
	}
	excess := total - width
	if excess <= 0 || shrinkable == 0 {
		return rows
	}
	// Each shrinkable Column gives up width in proportion to its slack
	// above a floor: 8 cells or its title, and for a wrapping Column its longest word
	// (so wrapping never splits a word). Width a Column can't give passes
	// to the others.
	limit, floor := make([]int, len(cols)), make([]int, len(cols))
	for i, c := range cols {
		limit[i], floor[i] = widths[i], widths[i]
		if c.Wrap || c.Flex {
			floor[i] = max(min(widths[i], 8), lipgloss.Width(c.Title))
		}
	}
	for _, r := range rows {
		for i, cell := range r {
			if cols[i].Wrap {
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
			if c.Wrap {
				floor[i] = max(min(widths[i], 8), lipgloss.Width(c.Title))
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
			case cols[i].Wrap:
				out[r][i] = wrapANSI(cell, limit[i])
			default:
				out[r][i] = truncateANSI(cell, limit[i])
			}
		}
	}
	return out
}

// block is a heading with an optional muted note, then content.
func (t *Theme) block(title, note, content string) string {
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

func (t *Theme) severityStyle(s engine.Severity) lipgloss.Style {
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

func (m TuiModel) overviewTables() string {
	t, d, w := m.t, m.Data, m.w-2
	res := d.Res
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(t.block("Your Mac", "", m.vitalsTable(w)))

	// What matters most: at most 3 here, all of them on the Health tab.
	b.WriteString("\n")
	fs := res.Health.Findings
	if len(fs) == 0 {
		b.WriteString(t.block("What matters most", "", " "+t.Ok.Render("Nothing urgent.")+" Disk, memory and swap look healthy.\n"))
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
			fmt.Sprint(len(tierOps)), tierSummary(tierOps, m.Deps.cat)})
		risks = append(risks, tier)
	}
	b.WriteString("\n")
	if len(rows) == 0 {
		b.WriteString(t.block("Reclaimable", "", " Nothing to reclaim was found in the latest scan.\n"))
	} else {
		cols := []Column{{Title: "Risk"}, {Title: "Tier"}, {Title: "Size", Right: true}, {Title: "Items", Right: true}, {Title: "Biggest items", Wrap: true}}
		b.WriteString(t.block("Reclaimable", "(details on the Suggestions tab)", t.Grid(cols, rows, w, func(r, c int) *lipgloss.Style {
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
		m.spaceChart(res.TopLevel, w)+m.foldersTable(res.TopLevel, overviewTopLevel, w)))
	if du := res.DataVolume; du.Source != model.SourceUnknown && du.Value > res.Scan.TotalBytes+du.Value/50 {
		b.WriteString("  " + t.muted.Render(engine.DecimalBytes(du.Value-res.Scan.TotalBytes)+" more is not visible to the scan (needs Full Disk Access).") + "\n")
	}
	if res.ScanStale {
		b.WriteString("\n  " + t.warn.Render("Note:") + fmt.Sprintf(" the scan is %s old. Press r to rescan.\n", strings.TrimSuffix(res.ScanAge, " ago")))
	}
	return b.String()
}

// vitalsTable is disk, memory, swap and snapshots as Metric | Value.
func (m TuiModel) vitalsTable(w int) string {
	t, rep := m.t, m.Data.Res.Health.Report
	var rows [][]string
	var styles []*lipgloss.Style
	d := rep.Disk
	if d.ContainerTotalBytes > 0 {
		used := d.ContainerTotalBytes - d.ContainerFreeBytes
		v := fmt.Sprintf("%s  %s used of %s · %s free (%s)", t.Meter(used, d.ContainerTotalBytes, 20), engine.DecimalBytes(used),
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
		st, label := t.Ok, "Normal"
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
	return t.Grid([]Column{{Title: "Metric"}, {Title: "Value", Wrap: true}}, rows, w, func(r, c int) *lipgloss.Style {
		if c == 0 {
			return ptr(t.muted)
		}
		return styles[r]
	})
}

func (m TuiModel) findingsTable(fs []engine.Finding, w int, details bool) string {
	t := m.t
	cols := []Column{{Title: "#", Right: true}, {Title: "Level"}, {Title: "Finding", Wrap: true}}
	if details {
		cols = append(cols, Column{Title: "Why it matters", Wrap: true})
	}
	rows := make([][]string, len(fs))
	for i, f := range fs {
		rows[i] = []string{fmt.Sprint(i + 1), severityLabel(f.Severity), f.Title}
		if details {
			rows[i] = append(rows[i], f.Detail)
		}
	}
	return t.Grid(cols, rows, w, func(r, c int) *lipgloss.Style {
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
func (m TuiModel) foldersTable(nodes []model.Node, limit, w int) string {
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
	return t.Grid([]Column{{Title: "Folder", Flex: true}, {Title: "Size", Right: true}, {Title: "Share", Right: true}}, rows, w,
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

// trendBlock is what grew between the latest scan and one about 30 days old.
func (m TuiModel) trendBlock(w int) string {
	t := m.t
	tr := m.Data.Trend
	if tr == nil {
		return t.block("What grew", "", " "+t.muted.Render(m.Data.trendMsg)+"\n")
	}
	floor := engine.DecimalBytes(tr.KeptFloor)
	span := engine.SpanText(tr.To.StartedAt.Sub(tr.From.StartedAt))
	var rows [][]string
	var changes []engine.Range // each row's change, for its bar
	for _, a := range tr.Areas {
		if c := changeText(a.Change, floor); c != "no change" && !strings.HasPrefix(c, "under ") {
			rows = append(rows, []string{a.Name, sizeText(a.Before, floor), sizeText(a.After, floor), c, ""})
			changes = append(changes, a.Change)
		}
	}
	for _, f := range tr.Growing {
		rows = append(rows, []string{TildePath(m.Deps.Home, f.Path), sizeText(f.Before, floor), engine.DecimalBytes(f.After), changeText(f.Change, floor), ""})
		changes = append(changes, f.Change)
	}
	var biggest int64
	for _, c := range changes {
		biggest = max(biggest, sureChange(c))
	}
	for i, c := range changes {
		rows[i][4] = t.changeBar(sureChange(c), biggest, c.High < 0, 12)
	}
	note := "in the last " + span + ", from macdoc's stored scans"
	if len(rows) == 0 {
		return t.block("What grew", note, " Nothing changed by more than "+floor+".\n")
	}
	return t.block("What grew", note, t.Grid(
		[]Column{{Title: "Area or folder", Flex: true}, {Title: "Before", Right: true}, {Title: "Now", Right: true}, {Title: "Change", Right: true}, {Title: " "}}, rows, w,
		func(r, c int) *lipgloss.Style {
			switch c {
			case 4:
				return ptr(t.text)
			case 1:
				return ptr(t.muted)
			case 2:
				return ptr(t.size)
			case 3:
				if strings.HasPrefix(rows[r][3], "+") {
					return ptr(t.warn)
				}
				return ptr(t.Ok)
			}
			return nil
		}))
}

// riskTypes is the "Risk type" cell of a category: one line per risk type it has, the
// label in its color and the size after it, in SAFE, CAUTION, RISKY, ADVICE order. A
// category with none shows a dash.
func (t *Theme) riskTypes(byRisk map[model.Risk]int64) string {
	var lines []string
	for _, r := range engine.TierOrder {
		if v, ok := byRisk[r]; ok {
			lines = append(lines, t.riskStyle(r).Render(fmt.Sprintf("%-7s", riskLabel(r)))+" "+t.size.Render(engine.DecimalBytes(v)))
		}
	}
	if len(lines) == 0 {
		return "—"
	}
	return strings.Join(lines, "\n")
}

func (m TuiModel) spaceTables() string {
	t, w := m.t, m.w-2
	sp := m.Data.Space
	var b strings.Builder
	b.WriteString("\n")

	if len(sp.Categories) == 0 {
		b.WriteString(t.block("Reclaimable, by category", "", " Nothing to reclaim was found in the latest scan.\n"))
	} else {
		cols := []Column{{Title: "Category", Flex: true}, {Title: "Size", Right: true}, {Title: "Share"}, {Title: "Items", Right: true}, {Title: "Risk type"}}
		var biggest int64
		for _, c := range sp.Categories {
			biggest = max(biggest, c.Bytes)
		}
		var rows [][]string
		for _, c := range sp.Categories {
			size := engine.DecimalBytes(c.Bytes)
			if c.UnknownCount > 0 {
				size += " + unknown"
			}
			frac := 0.0
			if biggest > 0 {
				frac = float64(c.Bytes) / float64(biggest)
			}
			rows = append(rows, []string{c.Category, size, t.miniBar(frac, 12), fmt.Sprint(c.Count), t.riskTypes(c.ByRisk)})
		}
		b.WriteString(t.block("Reclaimable, by category", "", t.Grid(cols, rows, w, func(r, c int) *lipgloss.Style {
			switch {
			case c == 1:
				return ptr(t.size)
			case c == 2:
				return ptr(t.text)
			case c == 3:
				return ptr(t.muted)
			}
			return nil
		})))
	}

	b.WriteString("\n")
	b.WriteString(m.trendBlock(w))

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
		b.WriteString(t.block(`"System Data", decoded`, "", t.Grid(
			[]Column{{Title: "Part", Wrap: true}, {Title: "Size", Right: true}, {Title: "Source"}, {Title: "What it is", Wrap: true}}, rows, w,
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
	b.WriteString("\n")
	b.WriteString(softwareBlock(t, m.Data.Res.Software))
	return b.String()
}

// softwareBlock is the Installed software section of the Space view, from
// the stored snapshot. It says where to look for the full list.
func softwareBlock(t *Theme, sw *engine.SoftwareSummary) string {
	if sw == nil {
		return t.block("Installed software", "", " Run macdoc scan to see the software on this Mac.\n")
	}
	s := display(*sw) // names come from the disk; clean them for the terminal
	body := " " + s.Headline + "\n"
	if len(s.NewNames) > 0 {
		body += " New since the last scan: " + strings.Join(s.NewNames, ", ") + "\n"
	}
	return t.block("Installed software", "(macdoc retire for the list)", body)
}

// opsTable lists suggestions; sel is the selected row (-1 for none).
// compact drops the restore Column (the details beside the list show it).
func (m TuiModel) opsTable(ops []model.Opportunity, sel, w int, compact bool) string {
	t := m.t
	// The ID is not a column: nobody types it here. A RISKY item's ID shows in
	// the removal box, where it must be typed.
	cols := []Column{{Title: " "}, {Title: "Size", Right: true}, {Title: "Risk"}, {Title: "Suggestion", Flex: true}, {Title: "Restore", Wrap: true}}
	rows := make([][]string, len(ops))
	for i, op := range ops {
		// The first column holds the mark only: a highlighted row needs no pointer. With
		// no color there is no highlight, so the pointer stays (the selection must show).
		mark := " "
		switch {
		case i == sel && !t.color && m.Marked[op.ID]:
			mark = "▶✓"
		case i == sel && !t.color:
			mark = "▶"
		case m.Marked[op.ID]:
			mark = "✓"
		}
		rows[i] = []string{mark, sizeCell(op.Bytes), riskLabel(op.Risk), op.Title, restoreText(op)}
	}
	if compact {
		cols = cols[:4]
		for i := range rows {
			rows[i] = rows[i][:4]
		}
	}
	off := 0
	if sel < 0 {
		cols, rows = cols[1:], dropFirst(rows)
		off = 1
	}
	return t.Grid(cols, rows, w, func(r, c int) *lipgloss.Style {
		c += off
		var s lipgloss.Style
		switch c {
		case 0:
			s = t.brand.Bold(true)
			if r == sel {
				s = t.fg(colSelectFg).Bold(true)
			}
		case 1:
			s = t.size
		case 2:
			s = t.riskStyle(ops[r].Risk).Bold(true)
		case 4:
			s = t.muted
		default:
			s = t.text
		}
		if r == sel {
			s = t.selectedRow(s)
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
func (m TuiModel) projectTables() string {
	t, w := m.t, m.w-2
	pr := m.Data.Projects
	if len(pr.Projects) == 0 {
		return "\n  " + t.muted.Render("No projects with build artifacts were found in the latest scan.")
	}
	var b strings.Builder
	b.WriteString("\n")
	var rows [][]string
	var total int64
	for _, p := range pr.Projects {
		total += p.ReclaimableBytes
		name := TildePath(m.Deps.Home, p.Root)
		if p.Protected {
			name += " (protected)"
		}
		idle := "unknown"
		if p.IdleDays >= 0 {
			idle = fmt.Sprintf("%d d · %s", p.IdleDays, p.ActivitySource)
		}
		parts := make([]string, 0, 3)
		for _, a := range p.Artifacts[:min(3, len(p.Artifacts))] {
			parts = append(parts, strings.ReplaceAll(a.Rel+" "+engine.DecimalBytes(a.Reclaimable()), " ", "\u00a0")) // wrap between folders, not inside one
		}
		if extra := len(p.Artifacts) - 3; extra > 0 {
			parts = append(parts, fmt.Sprintf("+%d more", extra))
		}
		rows = append(rows, []string{name, idle, engine.DecimalBytes(p.ReclaimableBytes), gitLabel(p.Git), strings.Join(parts, " · ")})
	}
	git := func(r int) *lipgloss.Style {
		switch pr.Projects[r].Git {
		case projects.GitClean:
			return ptr(t.Ok)
		case projects.GitDirty:
			return ptr(t.warn)
		}
		return ptr(t.muted)
	}
	b.WriteString(t.block("Projects", fmt.Sprintf("· %s of build folders in %d", engine.DecimalBytes(total), len(pr.Projects)), t.Grid(
		[]Column{{Title: "Project", Wrap: true}, {Title: "Idle"}, {Title: "Size", Right: true}, {Title: "Git"}, {Title: "Biggest folders", Wrap: true}}, rows, w,
		func(r, c int) *lipgloss.Style {
			switch c {
			case 0:
				if pr.Projects[r].Protected {
					return ptr(t.muted)
				}
				return ptr(t.Title)
			case 1:
				if pr.Projects[r].IdleDays >= 90 {
					return ptr(t.Ok)
				}
				return ptr(t.text)
			case 2:
				return ptr(t.size)
			case 3:
				return git(r)
			}
			return ptr(t.muted)
		})))
	b.WriteString("  " + t.Wrap(t.muted.Render("Idle is the newest of the last commit, a file change or a build. Projects in protected folders (Desktop, Documents, …) are listed only."), 2) + "\n")

	var gone [][]string
	for _, d := range pr.DerivedData {
		if d.Missing {
			gone = append(gone, []string{TildePath(m.Deps.Home, d.Workspace), engine.DecimalBytes(d.Reclaimable())})
		}
	}
	if len(gone) > 0 {
		b.WriteString("\n")
		b.WriteString(t.block("DerivedData of deleted projects", "", t.Grid(
			[]Column{{Title: "Workspace", Flex: true}, {Title: "Size", Right: true}}, gone, w,
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
func gitLabel(g projects.Git) string {
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
