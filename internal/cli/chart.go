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
		legend = append(legend, fmt.Sprintf("%s %s  %s  %s", style.Render(glyph), s.Label, engine.DecimalBytes(s.Bytes), share(s.Bytes, total)))
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
