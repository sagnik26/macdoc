package cli

import (
	"fmt"
	"strings"
	"time"

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

// ScanStatusLine is the one-line scan progress shown on stderr: a bar with a
// percentage when expected is known, a sweeping bar marked "size unknown" when
// it is not, then the bytes, files and time so far.
func ScanStatusLine(p scanner.Progress, expected int64, dir string) string {
	const width = 24
	var bar, tail string
	if expected > 0 {
		pct := min(0.99, float64(p.Bytes)/float64(expected))
		bar, tail = barText(pct, width), fmt.Sprintf("~%d%%", int(pct*100))
	} else {
		bar, tail = barText(sweep(p.Elapsed), width), "size unknown"
	}
	return fmt.Sprintf("Scanning… %s %s · %s · %d files · %s · %s",
		bar, tail, engine.DecimalBytes(p.Bytes), p.Files, p.Elapsed.Round(time.Second), Truncate(dir, 40))
}

// barText draws pct (0..1) as a bar of the given width.
func barText(pct float64, width int) string {
	filled := max(0, min(width, int(pct*float64(width))))
	return "[" + strings.Repeat("█", filled) + strings.Repeat("░", width-filled) + "]"
}
