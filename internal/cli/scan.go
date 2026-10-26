package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"macdoc/internal/engine"
	"macdoc/internal/model"
	"macdoc/internal/scanner"
)

func newScanCmd(g *globalFlags) *cobra.Command {
	var foreground bool
	var workers int
	cmd := &cobra.Command{
		Use:   "scan",
		Short: "Measure where disk space is going (full crawl, background priority, read-only)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			if !foreground {
				if err := scanner.SetBackgroundPriority(); err != nil {
					_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "macdoc: could not lower priority:", err)
				}
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
			defer stop()

			errOut := cmd.ErrOrStderr()
			showProgress := !g.json && !g.quiet && isTerminal(errOut)
			opts := engine.ScanOptions{Home: home, Version: version, Workers: workers}
			if showProgress {
				opts.Progress = func(p scanner.Progress) {
					_, _ = fmt.Fprintf(errOut, "\r\033[KScanning… %s · %d files · %s · %s",
						engine.DecimalBytes(p.Bytes), p.Files, p.Elapsed.Round(time.Second), truncate(p.CurrentDir, 50))
				}
			}
			sum, err := engine.Scan(ctx, opts)
			if showProgress {
				_, _ = fmt.Fprint(errOut, "\r\033[K")
			}
			if err != nil {
				return err
			}
			if g.json {
				return writeJSON(cmd.OutOrStdout(), "scan", sum)
			}
			return renderScan(cmd.OutOrStdout(), sum)
		},
	}
	cmd.Flags().BoolVar(&foreground, "foreground", false, "run at normal priority (faster, but competes with your apps)")
	cmd.Flags().IntVar(&workers, "workers", 4, "concurrent directory readers (1-16)")
	return cmd
}

func renderScan(w io.Writer, s engine.ScanSummary) error {
	var b strings.Builder
	in := s.Info
	dur := in.FinishedAt.Sub(in.StartedAt).Round(time.Second)
	fmt.Fprintf(&b, "Scan %s · %s in %d files · %s", in.ID, engine.DecimalBytes(in.TotalBytes), in.TotalFiles, dur)
	if in.Status != model.ScanComplete {
		fmt.Fprintf(&b, " · %s (interrupted; results are partial)", in.Status)
	}
	b.WriteString("\n")

	if len(s.TopLevel) > 0 {
		fmt.Fprintln(&b, "\nBy top-level folder")
		for _, n := range s.TopLevel {
			fmt.Fprintf(&b, "  %9s  %s\n", engine.DecimalBytes(n.Bytes), n.Path)
		}
	}
	if len(s.Largest) > 0 {
		fmt.Fprintln(&b, "\nWhere the space actually is (folders ≥ 1 GB)")
		for _, n := range s.Largest {
			fmt.Fprintf(&b, "  %9s  %s\n", engine.DecimalBytes(n.Bytes), n.Path)
		}
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
			fmt.Fprintf(&b, "  — %s (%s)\n", u.Path, u.Reason)
		}
	}
	if s.HardlinkLimitHit {
		b.WriteString("\nNote: very many hard links; some may be counted more than once.\n")
	}
	b.WriteString("\nNothing has been changed. Results are saved for `macdoc wins` and `macdoc trend`.\n")
	_, err := io.WriteString(w, b.String())
	return err
}

func hasPermissionGap(us []model.Unmeasured) bool {
	for _, u := range us {
		if strings.Contains(u.Reason, "permission denied") {
			return true
		}
	}
	return false
}

// isTerminal reports whether w is a character device (a TTY).
func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}
