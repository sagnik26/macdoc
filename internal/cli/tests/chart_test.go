package tests

import (
	"regexp"
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

// "System Data" is one stacked bar with short names, not a table of sentences: parts with a
// size are in the bar, biggest first, an estimate says so, a part with no known size is
// named as unknown and never drawn as zero, and the long explanations are not on screen.
func TestSystemDataIsOneBarWithShortNames(t *testing.T) {
	out := step(t, loaded(t, 120, 80), keys("4")...).Screen()
	mustContain(t, out, "Where the rest of the space is", "macOS manages this", "Not scanned (estimated)", "36.8 GB", "Temp files", "9.3 GB",
		"Swap and sleep", "8.6 GB", "Purgeable", "4.1 GB", "Size unknown: Snapshots", "macdoc only shows this and never deletes it")
	for _, gone := range []string{"What it is", "Source", "/private/var/folders", "Per-user temporary files", "Snapshots keep deleted files",
		"overcommitted", "decoded", "no recipe", "advice only"} {
		if strings.Contains(out, gone) {
			t.Errorf("the long text %q should not be on the Space tab:\n%s", gone, out)
		}
	}
	// biggest first, and the unknown part is not in the legend as a size
	if strings.Index(out, "Not scanned") > strings.Index(out, "Temp files") || strings.Index(out, "Temp files") > strings.Index(out, "Swap and sleep") {
		t.Errorf("the parts are listed biggest first:\n%s", out)
	}
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "Snapshots") && strings.Contains(l, "0 B") {
			t.Errorf("an unknown size must not show as zero: %q", l)
		}
	}
}

// Under the same heading a second bar of the same kind shows the person's own big folders: up
// to five and the rest as "other", with sizes and shares, a line that says macdoc has no cleanup
// for them, and no recipe wording. It is a separate bar from System Data: their sizes come from
// different places and are not added.
func TestSpaceShowsYourBigFoldersAsASecondBar(t *testing.T) {
	out := step(t, loaded(t, 120, 90), keys("4")...).Screen()
	mustContain(t, out, "Where the rest of the space is", "macOS manages this", "Your big folders, yours to review", "1 GB and up",
		"~/Library/Android", "3.6 GB", "~/.local", "2.8 GB", "other", "Open them with /explore")
	if strings.Index(out, "macOS manages this") > strings.Index(out, "Your big folders") {
		t.Errorf("System Data comes first:\n%s", out)
	}
	if strings.Count(out, "Where the rest of the space is") != 1 {
		t.Errorf("the two bars share one heading:\n%s", out)
	}
	for _, gone := range []string{"Large items with no recipe", "no recipe", "advice only"} {
		if strings.Contains(out, gone) {
			t.Errorf("the Space tab should not say %q:\n%s", gone, out)
		}
	}
}

// In the legend of a stacked bar the name, the size and the share are three different looks, so
// the numbers stand out from the words; without color they stay plain text.
func TestStackedBarLegendColorsSizeAndShare(t *testing.T) {
	segs := []cli.Segment{{Label: "Library", Bytes: 40 * gb}, {Label: "Developer", Bytes: 25 * gb}}
	_, legend := cli.StackedBar(cli.ForcedColorTheme(true, 100), segs, 30)
	line := legend[0]
	colorOf := func(text string) string { // the foreground color just before the text, as "r;g;b"
		i := strings.Index(line, text)
		if i < 0 {
			t.Fatalf("%q is missing from %q", text, line)
		}
		m := regexp.MustCompile(`38;2;(\d+;\d+;\d+)m[^\x1b]*$`).FindStringSubmatch(line[:i+len(text)])
		if m == nil {
			t.Fatalf("no color before %q in %q", text, line)
		}
		return m[1]
	}
	size, pct, name := colorOf("40.0 GB"), colorOf("62%"), colorOf("Library")
	if size == pct || size == name || pct == name {
		t.Errorf("name, size and share should each have their own color: name %q size %q share %q", name, size, pct)
	}
	_, plain := cli.StackedBar(cli.PlainLayoutTheme(), segs, 30)
	if strings.Contains(plain[0], "\x1b") {
		t.Errorf("without color the legend is plain text: %q", plain[0])
	}
}

// The command palette has a blank line above its list and a blank line below the input, in the
// scan view and on the welcome screen, and the screen still fits the terminal's height.
func TestCommandPaletteHasTopAndBottomMargin(t *testing.T) {
	home, _ := newIntroTUI(t, 110, 40, cli.IntroStatus{})
	for label, m := range map[string]cli.TuiModel{"scan view": loaded(t, 110, 40), "welcome": home} {
		out := step(t, m, keys("/")...).Screen()
		lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
		if len(lines) > 40 {
			t.Errorf("%s: %d lines on a 40-line terminal:\n%s", label, len(lines), out)
		}
		first, input := -1, -1
		for i, l := range lines {
			switch {
			case first < 0 && strings.Contains(l, "/scan"):
				first = i
			case strings.Contains(l, "› /"):
				input = i
			}
		}
		if first < 1 || input < 0 || input+2 >= len(lines)+1 {
			t.Fatalf("%s: could not find the palette:\n%s", label, out)
		}
		if strings.TrimSpace(lines[first-1]) != "" {
			t.Errorf("%s: a blank line above the list, got %q", label, lines[first-1])
		}
		if input+1 >= len(lines) || strings.TrimSpace(lines[input+1]) != "" {
			t.Errorf("%s: a blank line below the input:\n%s", label, out)
		}
	}
}
