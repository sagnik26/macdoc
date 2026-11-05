package cli

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"macdoc/internal/engine"
)

func newSlowCmd(g *GlobalFlags) *cobra.Command {
	var days int
	var tips bool
	cmd := &cobra.Command{
		Use:   "slow",
		Short: "Why your Mac feels slow: memory pressure, swap and the biggest memory users (read-only)",
		Long: `macdoc doctor slow explains whether memory is what's slowing your Mac down:
memory pressure, swap now and over the last days, and which apps use the most
memory and why. It only explains: it never quits or changes an app.

Each check stores a small sample (swap, pressure, free disk) in macdoc's own
database, so the swap history fills in as you use macdoc.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if days < 1 || days > 365 {
				return fmt.Errorf("--days must be between 1 and 365")
			}
			home, err := UserHome()
			if err != nil {
				return err
			}
			res := engine.Slow(context.Background(), engine.SlowOptions{Home: home, Runner: Runner, Days: days})
			if tips {
				res.Tips = engine.TipsFor(res.Groups)
			}
			if g.json {
				return writeJSONWarn(cmd.OutOrStdout(), "doctor slow", res, slices.Concat(res.Health.Report.Warnings, res.Warnings))
			}
			return RenderSlow(cmd.OutOrStdout(), res, time.Now())
		},
	}
	cmd.Flags().IntVar(&days, "days", 7, "days of swap history to show")
	cmd.Flags().BoolVar(&tips, "tips", false, "suggest what to do about the biggest memory users (still read-only)")
	return cmd
}

func RenderSlow(w io.Writer, res engine.SlowResult, now time.Time) error {
	res = display(res) // disk-sourced text, cleaned for the terminal (display)
	var b strings.Builder
	t := themeFor(w)
	rep := res.Health.Report
	writeVitals(&b, t, res.Health, "")

	fmt.Fprintf(&b, "\n%s\n", t.heading("Is memory slowing it down?"))
	fmt.Fprintf(&b, "  %s %s\n", t.sev(res.Level), t.Wrap(t.text.Render(res.Verdict), 4))
	if res.Summary != "" {
		fmt.Fprintf(&b, "    %s\n", t.Wrap(t.muted.Render("Biggest users: "+res.Summary), 4))
	}

	fmt.Fprintf(&b, "\n%s\n", t.heading("Swap"))
	fmt.Fprintf(&b, "  %s\n", swapLine(t, res, now))

	m := rep.Memory
	if m.TotalBytes > 0 {
		fmt.Fprintf(&b, "\n%s\n", t.heading("Memory"))
		line := fmt.Sprintf("  %s used by macOS itself (wired) · %s compressed", engine.BinaryBytes(m.WiredBytes), engine.BinaryBytes(m.CompressedBytes))
		if m.FreePercent >= 0 {
			line += fmt.Sprintf(" · %d%% free", m.FreePercent)
		}
		fmt.Fprintln(&b, t.text.Render(line))
		fmt.Fprintln(&b, t.muted.Render("  Compressed memory is how macOS fits more into RAM before it swaps; a lot of it means RAM is full."))
	}

	if len(res.Groups) > 0 {
		fmt.Fprintf(&b, "\n%s\n", t.heading("Biggest memory users"))
		for _, g := range res.Groups {
			fmt.Fprintf(&b, "  %s %s", t.text.Render(fmt.Sprintf("%-24s", Truncate(g.Name, 24))),
				t.size.Render(fmt.Sprintf("%9s", engine.BinaryBytes(g.RSSBytes))))
			if g.Count > 1 {
				b.WriteString(t.muted.Render(fmt.Sprintf("  %d processes", g.Count)))
			}
			b.WriteString("\n")
			if g.Explain != "" {
				fmt.Fprintf(&b, "    %s\n", t.Wrap(t.muted.Render(g.Explain), 4))
			}
		}
		fmt.Fprintf(&b, "  %s\n", t.Wrap(t.faint.Render(engine.RSSNote), 2))
	}

	if len(res.Tips) > 0 {
		fmt.Fprintf(&b, "\n%s\n", t.heading("Tips"))
		for _, tp := range res.Tips {
			fmt.Fprintf(&b, "  %s\n", t.Wrap(t.text.Render(tp.Text), 2))
		}
	}

	if len(res.Notes) > 0 {
		fmt.Fprintf(&b, "\n%s\n", t.heading("Also"))
		for _, n := range res.Notes {
			fmt.Fprintf(&b, "  %s %s\n", t.sev(engine.SeverityWarn), t.Wrap(t.text.Render(n), 4))
		}
	}

	warnings := slices.Concat(rep.Warnings, res.Warnings)
	if len(warnings) > 0 {
		fmt.Fprintln(&b, "\n"+t.heading("Could not measure"))
		for _, wn := range warnings {
			fmt.Fprintln(&b, t.muted.Render("  — "+wn))
		}
	}
	fmt.Fprintln(&b, "\n"+t.done("Nothing has been changed. macdoc only explains; it never quits or changes apps."))
	_, err := io.WriteString(w, b.String())
	return err
}

// swapLine is swap now and over the history window.
func swapLine(t *theme, res engine.SlowResult, now time.Time) string {
	s := res.Health.Report.Swap
	line := t.size.Render(engine.BinaryBytes(s.UsedBytes)) + t.text.Render(" in use now")
	h := res.Swap
	if h.Samples < 2 {
		return line + t.muted.Render(". History builds up each time macdoc checks your Mac.")
	}
	line += t.muted.Render(fmt.Sprintf(" · peak %s %s · lowest %s", engine.BinaryBytes(h.Peak), when(h.PeakAt, now), engine.BinaryBytes(h.Low)))
	line += t.muted.Render(fmt.Sprintf(" (%d checks, last %d days)", h.Samples, h.Days))
	if h.Critical > 0 {
		line += "\n  " + t.warn.Render(fmt.Sprintf("Memory pressure was critical in %d of those checks.", h.Critical))
	}
	return line
}

// when is a short relative time: "today 14:02", "yesterday", "on Mon 27 Oct".
func when(at, now time.Time) string {
	at, now = at.Local(), now.Local()
	y1, m1, d1 := at.Date()
	y2, m2, d2 := now.Date()
	switch {
	case y1 == y2 && m1 == m2 && d1 == d2:
		return "today " + at.Format("15:04")
	case y1 == y2 && m1 == m2 && d1 == d2-1:
		return "yesterday " + at.Format("15:04")
	}
	return "on " + at.Format("Mon 2 Jan")
}
