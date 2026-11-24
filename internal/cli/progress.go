package cli

import (
	"fmt"
	"io"
	"os"
	"time"

	"charm.land/bubbles/v2/progress"
	"github.com/charmbracelet/colorprofile"

	"github.com/sagnik26/macdoc/internal/engine"
	"github.com/sagnik26/macdoc/internal/scanner"
)

// ExpectedScanBytes is how much the next scan is expected to measure, from the
// latest stored scan. 0 means unknown (a first scan).
func ExpectedScanBytes(home string) int64 {
	if info, _, err := engine.LastScan(home, time.Now()); err == nil {
		return info.TotalBytes
	}
	return 0
}

// statusBarWidth is the width of the bar in a one-line status, with its percentage.
const statusBarWidth = 30

// barChar fills and draws the track of every progress bar: a lower half block, so
// the bar is half as tall as a full cell. The colors are the Bubbles README's.
const barChar = '▄'

// barTrack is the color of the empty part of a bar: the Bubbles README's slate gray.
const barTrack = "#606060"

// statusBar is the progress bar as the Bubbles README draws it (half blocks in
// the default purple-to-pink blend over a dim track), with its percentage
// unless the total is unknown. It carries truecolor escapes: write it through
// statusOut so the terminal's own depth is respected.
func statusBar(pct float64, showPct bool) string {
	b := progress.New(progress.WithDefaultBlend(), progress.WithFillCharacters(barChar, barChar), progress.WithWidth(statusBarWidth))
	b.ShowPercentage = showPct
	return b.ViewAs(pct)
}

// statusOut wraps a terminal writer so colors are reduced to what it shows, or
// dropped for NO_COLOR and a terminal with no color.
func statusOut(w io.Writer) io.Writer { return colorprofile.NewWriter(w, os.Environ()) }

// ScanStatusLine is the one-line scan progress shown on stderr: the bar with a
// percentage when expected is known, a sweeping bar marked "size unknown" when
// it is not, then the bytes, files and time so far.
func ScanStatusLine(p scanner.Progress, expected int64, dir string) string {
	var bar string
	if expected > 0 {
		bar = statusBar(min(0.99, float64(p.Bytes)/float64(expected)), true)
	} else {
		bar = statusBar(sweep(p.Elapsed), false) + "  size unknown"
	}
	return fmt.Sprintf("Scanning… %s · %s · %d files · %s · %s",
		bar, engine.DecimalBytes(p.Bytes), p.Files, p.Elapsed.Round(time.Second), Truncate(dir, 40))
}

// SourceStatusLine is the one-line progress of `macdoc retire`: the bar over the
// discovery sources read so far.
func SourceStatusLine(done, total int, source string) string {
	pct := 0.0
	if total > 0 {
		pct = float64(done) / float64(total)
	}
	tail := fmt.Sprintf("%d of %d sources", done, total)
	if source != "" {
		tail += " · " + source
	}
	return "Reading installed software… " + statusBar(pct, true) + "  " + tail
}

// ProbeStatusLine is the one-line progress of `macdoc vitals`: the bar over the
// probes that have answered.
func ProbeStatusLine(done, total int, probe string) string {
	pct := 0.0
	if total > 0 {
		pct = float64(done) / float64(total)
	}
	tail := fmt.Sprintf("%d of %d readings", done, total)
	if probe != "" {
		tail += " · " + probe
	}
	return "Reading your Mac… " + statusBar(pct, true) + "  " + tail
}
