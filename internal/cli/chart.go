package cli

import (
	"fmt"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/sagnik26/macdoc/internal/engine"
	"github.com/sagnik26/macdoc/internal/model"
)

// Text charts (docs/DESIGN_SYSTEM.md 4.10): a terminal draws in blocks, so a chart is a
// row of colored cells with a legend. They use only sizes macdoc has measured.

// Segment is one part of a stacked bar.
type Segment struct {
	Label string
	Bytes int64
	Rest  bool // "everything else": drawn in the muted color
}

// segColors are the colors of the segments in order; a segment that is the rest uses the
// muted one. They are the palette's semantic and brand colors, readable on both backgrounds.
var segColors = []duo{colBrand, colGreen, colYellow, colOrange, colBlue, {"#EE6FF8", "#B83DC4"}}

// segGlyphs tell the segments apart without color.
var segGlyphs = []string{"█", "▓", "▒", "░", "▪", "·"}

// StackedBar draws segs as one bar of width cells, one color per segment (a different
// glyph each without color), and a legend line per segment with its size and share. A
// segment with any bytes gets at least one cell. It returns an empty bar when there is
// nothing to draw.
func StackedBar(t *Theme, segs []Segment, width int) (bar string, legend []string) {
	var total int64
	vals := make([]int64, len(segs))
	for i, s := range segs {
		vals[i] = max(0, s.Bytes)
		total += vals[i]
	}
	if total <= 0 || width <= 0 || len(segs) == 0 {
		return "", nil
	}
	cells := allocateCells(vals, width)
	var b strings.Builder
	for i, s := range segs {
		glyph, style := segGlyphs[i%len(segGlyphs)], lipgloss.NewStyle()
		if s.Rest {
			glyph = segGlyphs[len(segGlyphs)-1]
		}
		if t.color {
			glyph, style = "█", style.Foreground(t.c(segColor(i, s.Rest)))
		}
		b.WriteString(style.Render(strings.Repeat(glyph, cells[i])))
		// the name in plain text, the size in the accent color and the share muted, so the three read apart
		legend = append(legend, fmt.Sprintf("%s %s  %s  %s", style.Render(glyph), t.text.Render(s.Label),
			t.accent.Render(engine.DecimalBytes(s.Bytes)), t.muted.Render(share(s.Bytes, total))))
	}
	return b.String(), legend
}

// segColor is the color of segment i.
func segColor(i int, rest bool) duo {
	if rest {
		return colMuted
	}
	return segColors[i%len(segColors)]
}

// allocateCells splits width cells among vals in proportion, by largest remainder, and
// gives every non-zero value at least one cell.
func allocateCells(vals []int64, width int) []int {
	var total int64
	for _, v := range vals {
		total += v
	}
	cells, rem := make([]int, len(vals)), make([]float64, len(vals))
	used := 0
	for i, v := range vals {
		exact := float64(v) * float64(width) / float64(total)
		cells[i] = int(exact)
		rem[i] = exact - float64(cells[i])
		if v > 0 && cells[i] == 0 {
			cells[i], rem[i] = 1, -1
		}
		used += cells[i]
	}
	for used < width {
		best := -1
		for i, v := range vals {
			if v > 0 && (best < 0 || rem[i] > rem[best]) {
				best = i
			}
		}
		cells[best]++
		rem[best] = -1
		used++
	}
	for used > width {
		best := 0
		for i := range cells {
			if cells[i] > cells[best] {
				best = i
			}
		}
		if cells[best] <= 1 {
			break
		}
		cells[best]--
		used--
	}
	return cells
}

// spaceChart is the stacked bar of the biggest top-level folders (up to five, the rest as
// "other"), with its legend, for the Overview. It is "" when nothing was measured.
func (m TuiModel) spaceChart(nodes []model.Node, w int) string {
	if len(nodes) == 0 {
		return ""
	}
	sorted := append([]model.Node(nil), nodes...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Bytes > sorted[j].Bytes })
	var segs []Segment
	var rest int64
	for i, n := range sorted {
		if i < 5 {
			segs = append(segs, Segment{Label: TildePath(m.Deps.Home, n.Path), Bytes: n.Bytes})
		} else {
			rest += n.Bytes
		}
	}
	if rest > 0 {
		segs = append(segs, Segment{Label: "other", Bytes: rest, Rest: true})
	}
	bar, legend := StackedBar(m.t, segs, max(20, min(w-4, 72)))
	if bar == "" {
		return ""
	}
	return " " + bar + "\n" + indentLines(strings.Join(legend, "\n"), " ") + "\n\n"
}

// miniBar draws frac (0 to 1) as a small bar of width cells: the blend over a dim track in
// color, the plain text bar without. It is the bar of a table row.
func (t *Theme) miniBar(frac float64, width int) string {
	n := max(0, min(width, int(frac*float64(width)+0.5)))
	if !t.color {
		return strings.Repeat("█", n) + strings.Repeat("░", width-n)
	}
	var b strings.Builder
	for i, hex := range gradient(width) {
		cell, col := "█", t.profile.Convert(lipgloss.Color(hex))
		if i >= n {
			cell, col = "░", t.profile.Convert(lipgloss.Color(barTrack))
		}
		b.WriteString(lipgloss.NewStyle().Foreground(col).Render(cell))
	}
	return b.String()
}

// sureChange is the size of a change that is certain: all of it when exact, the near end of
// a range that does not cross zero, and nothing for one that does.
func sureChange(r engine.Range) int64 {
	switch {
	case r.Low > 0:
		return r.Low
	case r.High < 0:
		return -r.High
	case r.Exact():
		return abs64(r.Low)
	}
	return 0
}

func abs64(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

// changeBar draws how big a change is against the biggest one, width cells: orange for
// growth, green for shrinkage, over a dim track. Without color it is the plain text bar.
func (t *Theme) changeBar(amount, biggest int64, shrank bool, width int) string {
	if biggest <= 0 {
		return strings.Repeat("░", width)
	}
	n := int(float64(amount)/float64(biggest)*float64(width) + 0.5)
	if amount > 0 {
		n = max(1, n)
	}
	n = min(n, width)
	if !t.color {
		return strings.Repeat("█", n) + strings.Repeat("░", width-n)
	}
	fill, track := t.warn, lipgloss.NewStyle().Foreground(t.profile.Convert(lipgloss.Color(barTrack)))
	if shrank {
		fill = t.Ok
	}
	return fill.Render(strings.Repeat("█", n)) + track.Render(strings.Repeat("░", width-n))
}

// systemDataNames are the short names of the parts of "System Data", for the chart. A part
// that is not here is shortened by cutting what is in brackets.
var systemDataNames = []struct{ prefix, short string }{
	{"Local Time Machine snapshots", "Snapshots"},
	{"Swap and sleep image", "Swap and sleep"},
	{"Purgeable space", "Purgeable"},
	{"Not visible to the scan", "Not scanned"},
	{"Temporary and per-app cache files", "Temp files"},
	{"Shared simulator data", "Simulators"},
}

// shortPartName is the name a "System Data" part has in the chart.
func shortPartName(name string) string {
	for _, n := range systemDataNames {
		if strings.HasPrefix(name, n.prefix) {
			return n.short
		}
	}
	if i := strings.Index(name, " ("); i > 0 {
		name = name[:i]
	}
	if r := []rune(name); len(r) > 24 {
		name = string(r[:23]) + "…"
	}
	return name
}

// restOfSpaceBlock is the Space tab's "Where the rest of the space is": two bars of the same
// kind, one for what macOS manages (System Data) and one for your own big folders, each with a
// plain label of who decides. They are two bars on purpose: the sizes come from different
// places and are not added together.
func (m TuiModel) restOfSpaceBlock(sp engine.StorageResult, w int) string {
	t := m.t
	var b strings.Builder
	if len(sp.SystemData) > 0 {
		b.WriteString(" " + t.accent.Bold(true).Render("macOS manages this") + "\n")
		b.WriteString(m.systemDataChart(sp.SystemData, w))
		b.WriteString("\n")
	}
	b.WriteString(" " + t.accent.Bold(true).Render("Your big folders, yours to review") + t.muted.Render("  · 1 GB and up") + "\n")
	if chart := m.spaceChart(sp.Large, w); chart != "" {
		b.WriteString(strings.TrimRight(chart, "\n") + "\n")
		b.WriteString(" " + t.muted.Render("macdoc has no cleanup for these. Open them with /explore and decide yourself.") + "\n")
	} else {
		b.WriteString(" " + t.muted.Render("None found.") + "\n")
	}
	return t.block("Where the rest of the space is", "", b.String())
}

// systemDataChart shows what macOS counts as "System Data" as one stacked bar with a short
// legend, not a table of sentences. Only parts with a size are in the bar; a part whose size
// is unknown is named under it as unknown, never drawn as zero, and an estimate says so.
func (m TuiModel) systemDataChart(parts []engine.SystemDataPart, w int) string {
	t := m.t
	var sized []engine.SystemDataPart
	var unknown []string
	for _, p := range parts {
		if p.Bytes.Source == model.SourceUnknown {
			unknown = append(unknown, shortPartName(p.Name))
			continue
		}
		sized = append(sized, p)
	}
	sort.SliceStable(sized, func(i, j int) bool { return sized[i].Bytes.Value > sized[j].Bytes.Value })
	segs := make([]Segment, len(sized))
	for i, p := range sized {
		label := shortPartName(p.Name)
		if p.Bytes.Source == model.SourceEstimated {
			label += " (estimated)"
		}
		segs[i] = Segment{Label: label, Bytes: p.Bytes.Value}
	}
	var b strings.Builder
	if bar, legend := StackedBar(t, segs, max(20, min(w-4, 72))); bar != "" {
		b.WriteString(" " + bar + "\n" + indentLines(strings.Join(legend, "\n"), " ") + "\n")
	}
	if len(unknown) > 0 {
		b.WriteString(" " + t.muted.Render("Size unknown: "+strings.Join(unknown, ", ")) + "\n")
	}
	b.WriteString(" " + t.muted.Render("macdoc only shows this and never deletes it.") + "\n")
	return b.String()
}
