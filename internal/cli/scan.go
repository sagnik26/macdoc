package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sagnik26/macdoc/internal/engine"
	"github.com/sagnik26/macdoc/internal/model"
)

// ScanAsk reports whether `macdoc scan` may ask a question: only at an
// interactive terminal. Tests replace it.
var ScanAsk = interactive

func newScanCmd(g *GlobalFlags) *cobra.Command {
	var foreground, fresh, last bool
	var workers int
	cmd := &cobra.Command{
		Use:   "scan",
		Short: "Scan the disk (fresh, or reuse the last one), then show the full report (read-only)",
		Long: `macdoc scan shows the full report: every section of the overview, the scan
details, and every suggestion.

In a terminal, when an earlier scan exists, it asks whether you want a fresh
scan (the whole disk is read again) or the last data. Say which up front with
--fresh or --last. Piped, with --json or --quiet, it scans fresh, as the
scheduled scan needs.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if fresh && last {
				return errors.New("use --fresh or --last, not both")
			}
			if !fresh && !last && !g.json && !g.quiet && ScanAsk(cmd) {
				choice, err := askScanChoice(cmd)
				if err != nil {
					return err
				}
				last = choice == "last"
			}
			return runOverview(cmd, g, overviewMode{full: true, rescan: !last, foreground: foreground, workers: workers})
		},
	}
	cmd.Flags().BoolVar(&fresh, "fresh", false, "scan the disk again without asking")
	cmd.Flags().BoolVar(&last, "last", false, "use the last scan's data without asking")
	cmd.Flags().BoolVar(&foreground, "foreground", false, "scan at normal priority (faster, but competes with your apps)")
	cmd.Flags().IntVar(&workers, "workers", 4, "concurrent directory readers (1-16)")
	return cmd
}

// RenderScan writes the result of a scan in the human format.
func RenderScan(w io.Writer, s engine.ScanSummary) error {
	s = display(s) // disk-sourced text, cleaned for the terminal (display)
	var b strings.Builder
	t := themeFor(w)
	in := s.Info
	dur := in.FinishedAt.Sub(in.StartedAt).Round(time.Second)
	fmt.Fprintf(&b, "%s %s · %s in %d files · %s", t.heading("Scan"), t.ID.Render(in.ID), t.size.Render(engine.DecimalBytes(in.TotalBytes)), in.TotalFiles, dur)
	if in.Status != model.ScanComplete {
		b.WriteString(t.warn.Render(fmt.Sprintf(" · %s (interrupted; results are partial)", in.Status)))
	}
	b.WriteString("\n")
	if du := s.DataVolumeUsed; du.Source != model.SourceUnknown && du.Value > 0 {
		hidden := du.Value - in.TotalBytes
		fmt.Fprintf(&b, "Measured %s of the %s APFS reports as used on the Data volume", engine.DecimalBytes(in.TotalBytes), engine.DecimalBytes(du.Value))
		if hidden > du.Value/50 { // more than 2% not visible
			fmt.Fprintf(&b, "; %s is not visible to the scan (locations needing Full Disk Access, plus system-managed indexes)", engine.DecimalBytes(hidden))
		}
		b.WriteString(".\n")
	}

	if len(s.TopLevel) > 0 {
		fmt.Fprintln(&b, "\n"+t.heading("By top-level folder"))
		writeSizeList(&b, t, s.TopLevel)
	}
	if len(s.Largest) > 0 {
		fmt.Fprintln(&b, "\n"+t.heading("Where the space actually is")+t.muted.Render(" (folders ≥ 1 GB)"))
		writeSizeList(&b, t, s.Largest)
	}
	if len(s.Unmeasured) > 0 {
		fmt.Fprintf(&b, "\nCould not measure %d location(s)", len(s.Unmeasured))
		if hasPermissionGap(s.Unmeasured) {
			b.WriteString(" — grant Full Disk Access to your terminal app in System Settings › Privacy & Security for complete results")
		}
		b.WriteString("\n")
		for i, u := range s.Unmeasured {
			if i == 5 {
				fmt.Fprintf(&b, "  … and %d more (see --json)\n", len(s.Unmeasured)-5)
				break
			}
			b.WriteString(t.muted.Render(fmt.Sprintf("  — %s (%s)", u.Path, u.Reason)) + "\n")
		}
	}
	if s.HardlinkLimitHit {
		b.WriteString("\nNote: very many hard links; some may be counted more than once.\n")
	}
	b.WriteString("\n" + t.done("Nothing has been changed.") + "\n")
	_, err := io.WriteString(w, b.String())
	return err
}

// writeSizeList writes "  <size>  <path>" lines; with color, a small bar
// shows each item relative to the first (largest).
func writeSizeList(b *strings.Builder, t *Theme, nodes []model.Node) {
	var top int64
	for _, n := range nodes {
		top = max(top, n.Bytes)
	}
	for _, n := range nodes {
		size := t.size.Render(fmt.Sprintf("%9s", engine.DecimalBytes(n.Bytes)))
		if t.on {
			filled := 0
			if top > 0 {
				filled = max(1, int(12*float64(n.Bytes)/float64(top)))
			}
			fmt.Fprintf(b, "  %s  %s%s  %s\n", size, t.accent.Render(strings.Repeat("▬", filled)), strings.Repeat(" ", 12-filled), t.text.Render(n.Path))
			continue
		}
		fmt.Fprintf(b, "  %s  %s\n", size, n.Path)
	}
}

func hasPermissionGap(us []model.Unmeasured) bool {
	for _, u := range us {
		if strings.Contains(u.Reason, "permission denied") {
			return true
		}
	}
	return false
}

// IsTerminal reports whether w (a reader or writer) is a character device (a TTY).
func IsTerminal(w any) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// askScanChoice asks whether to scan again or keep the last data, when there is
// a last scan. It returns "fresh" or "last"; with no scan to reuse it returns
// "fresh" without asking.
func askScanChoice(cmd *cobra.Command) (string, error) {
	home, err := UserHome()
	if err != nil {
		return "", err
	}
	info, age, err := engine.LastScan(home, time.Now())
	if err != nil {
		return "fresh", nil //nolint:nilerr // no earlier scan: nothing to choose between
	}
	out := cmd.OutOrStdout()
	_, _ = fmt.Fprintf(out, "Last scan: %s (%s measured).\n", age, engine.DecimalBytes(info.TotalBytes))
	_, _ = fmt.Fprint(out, "Scan the disk again, or use the last data? [F]resh / [u]se last: ")
	line, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	if err != nil && line == "" {
		return "", errors.New("no answer; nothing was scanned (use --fresh or --last to skip the question)")
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "", "f", "fresh":
		return "fresh", nil
	case "u", "l", "last":
		return "last", nil
	}
	return "", errors.New("answer f for a fresh scan or u for the last data")
}
