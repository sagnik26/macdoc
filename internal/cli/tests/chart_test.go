package tests

import (
	"strings"
	"testing"

	"github.com/sagnik26/macdoc/internal/cli"
)

// A stacked bar fills exactly its width, gives every segment with any bytes at least one
// cell, tells the segments apart by glyph without color, and has a legend line each with
// the size and share.
func TestStackedBarFillsItsWidth(t *testing.T) {
	segs := []cli.Segment{{Label: "Library", Bytes: 50 * gb}, {Label: "Developer", Bytes: 30 * gb}, {Label: "Downloads", Bytes: 19 * gb},
		{Label: "tiny", Bytes: gb / 1000}, {Label: "other", Bytes: 0, Rest: true}}
	bar, legend := cli.StackedBar(cli.PlainLayoutTheme(), segs, 40)
	if n := len([]rune(bar)); n != 40 {
		t.Fatalf("the bar should be 40 cells, got %d: %q", n, bar)
	}
	for _, glyph := range []string{"█", "▓", "▒", "░"} {
		if !strings.Contains(bar, glyph) {
			t.Errorf("segment glyph %q is missing, even the tiny segment needs a cell: %q", glyph, bar)
		}
	}
	if len(legend) != 5 {
		t.Fatalf("one legend line per segment, got %d", len(legend))
	}
	mustContain(t, strings.Join(legend, "\n"), "Library", "50.0 GB", "Developer", "30.0 GB", "tiny", "Downloads")
	if empty, lg := cli.StackedBar(cli.PlainLayoutTheme(), nil, 40); empty != "" || lg != nil {
		t.Errorf("nothing to draw should draw nothing: %q %v", empty, lg)
	}
}

// With color each segment is its own color, drawn as full blocks.
func TestStackedBarUsesDifferentColors(t *testing.T) {
	segs := []cli.Segment{{Label: "A", Bytes: 5 * gb}, {Label: "B", Bytes: 3 * gb}, {Label: "C", Bytes: 2 * gb}, {Label: "rest", Bytes: gb, Rest: true}}
	bar, _ := cli.StackedBar(cli.ForcedColorTheme(true, 100), segs, 30)
	colors := map[string]bool{}
	for _, part := range strings.Split(bar, "\x1b[")[1:] {
		if i := strings.Index(part, "m"); i > 0 && strings.HasPrefix(part, "38;2;") {
			colors[part[:i]] = true
		}
	}
	if len(colors) < 4 {
		t.Errorf("each segment should have its own color, got %d: %q", len(colors), bar)
	}
	if strings.ContainsAny(bar, "▓▒░") {
		t.Errorf("with color the cells are all full blocks: %q", bar)
	}
}

// The Overview shows where the space is as one stacked bar of the biggest folders, with a
// legend, above the folders table.
func TestOverviewShowsWhereTheSpaceIs(t *testing.T) {
	out := loaded(t, 120, 60).Screen()
	mustContain(t, out, "Where your space is", "▒", "Library", "40.0 GB", "40%", "Developer", "25%", "other")
	if strings.Index(out, "▓") > strings.Index(out, "│ Folder") {
		t.Errorf("the bar goes above the folders table:\n%s", out)
	}
}

// The Space tab draws a share bar per category, scaled to the biggest.
func TestSpaceCategoriesHaveShareBars(t *testing.T) {
	out := step(t, loaded(t, 120, 60), keys("4")...).Screen()
	mustContain(t, out, "Reclaimable, by category", "Build caches", "Containers", "Share")
	var full, quarter string
	for _, l := range strings.Split(out, "\n") {
		switch {
		case strings.Contains(l, "Build caches"):
			full = l
		case strings.Contains(l, "Containers"):
			quarter = l
		}
	}
	if !strings.Contains(full, strings.Repeat("█", 12)) {
		t.Errorf("the biggest category fills its bar: %q", full)
	}
	if strings.Count(quarter, "█") != 3 || !strings.Contains(quarter, "█░") { // 5 of 20 GB is 3 of 12 cells
		t.Errorf("a quarter of the biggest fills a quarter of the bar: %q", quarter)
	}
}

// The Space tab's category table has one "Risk type" column, not one column per risk
// type: a category lists each of its types under it, with the size of each.
func TestSpaceCategoriesHaveOneRiskTypeColumn(t *testing.T) {
	out := step(t, loaded(t, 120, 60), keys("4")...).Screen()
	mustContain(t, out, "Risk type", "Build caches", "Dev tools")
	var header string
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "Category") && strings.Contains(l, "Share") {
			header = l
		}
	}
	for _, gone := range []string{"SAFE", "CAUTION", "RISKY", "ADVICE"} {
		if strings.Contains(header, gone) {
			t.Errorf("the header should have no %q column of its own: %q", gone, header)
		}
	}
	lines := strings.Split(out, "\n")
	for i, l := range lines {
		if strings.Contains(l, "Dev tools") {
			if !strings.Contains(l, "SAFE") || !strings.Contains(l, "3.0 GB") || i+1 >= len(lines) ||
				!strings.Contains(lines[i+1], "RISKY") || !strings.Contains(lines[i+1], "1.0 GB") || strings.Contains(lines[i+1], "Dev tools") {
				t.Errorf("a category with two risk types lists them one under the other:\n%s\n%s", l, lines[i+1])
			}
			return
		}
	}
	t.Error("the Dev tools category is missing")
}

// "What grew" draws a bar per row, scaled to the biggest change: the biggest fills the bar,
// the others take their share of it, and a shrinking area gets a bar too.
func TestWhatGrewHasChangeBars(t *testing.T) {
	out := step(t, loaded(t, 120, 60), keys("4")...).Screen()
	bar := func(name string) string {
		for _, l := range strings.Split(out, "\n") {
			if strings.Contains(l, name) && strings.Contains(l, "GB") && strings.Contains(l, "█") {
				return l
			}
		}
		return ""
	}
	gradle, docker, xcode := bar("Gradle"), bar("Docker"), bar("Xcode")
	if strings.Count(gradle, "█") != 12 {
		t.Errorf("the biggest change fills the bar: %q", gradle)
	}
	if strings.Count(docker, "█") != 6 || strings.Count(xcode, "█") != 3 {
		t.Errorf("3 GB of 6 is half the bar and 1.5 GB a quarter: docker %q, xcode %q", docker, xcode)
	}
	mustContain(t, docker, "−3.0 GB")
}
