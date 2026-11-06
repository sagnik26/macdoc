package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"github.com/sagnik26/macdoc/internal/engine"
	"github.com/sagnik26/macdoc/internal/store"
)

func newTrendCmd(g *GlobalFlags) *cobra.Command {
	var days int
	cmd := &cobra.Command{
		Use:   "trend",
		Short: "What grew between scans: per area and the fastest-growing folders (read-only)",
		Long: `macdoc doctor trend compares the latest scan with one from about --days ago
(or the oldest macdoc kept) and shows what grew: per area (Xcode, Gradle,
Docker, …) and the folders that grew the most. It reads macdoc's stored scans
only. macdoc keeps the 10 newest scans plus one per week for 13 weeks.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if days < 1 || days > 365 {
				return fmt.Errorf("--days must be between 1 and 365")
			}
			home, err := UserHome()
			if err != nil {
				return err
			}
			res, err := engine.Trend(engine.TrendOptions{Home: home, Days: days})
			switch {
			case errors.Is(err, store.ErrNoScan):
				return errors.New("no scan yet: run macdoc doctor scan, then again in a few days")
			case errors.Is(err, engine.ErrNoHistory):
				return errors.New("only one scan so far: trends need two. Run macdoc doctor scan again in a few days")
			case err != nil:
				return err
			}
			if g.json {
				return writeJSON(cmd.OutOrStdout(), "doctor trend", res)
			}
			return RenderTrend(cmd.OutOrStdout(), res, home)
		},
	}
	cmd.Flags().IntVar(&days, "days", 30, "compare with a scan about this many days old")
	return cmd
}

// RenderTrend writes what grew between two scans in the human format.
func RenderTrend(w io.Writer, res engine.TrendResult, home string) error {
	res = display(res) // disk-sourced text, cleaned for the terminal (display)
	var b strings.Builder
	t := themeFor(w)
	span := fmt.Sprintf("%s → %s", res.From.StartedAt.Local().Format("2 Jan 15:04"), res.To.StartedAt.Local().Format("2 Jan 15:04"))
	if !t.on {
		fmt.Fprintf(&b, "macdoc doctor trend · %s\n\n", span)
	} else {
		b.WriteString(t.Card(t.brand.Render("◆ macdoc")+" "+t.Title.Render("doctor trend")+t.muted.Render(" · "+span)) + "\n")
	}
	floor := engine.DecimalBytes(res.KeptFloor)

	fmt.Fprintf(&b, " %s %s %s\n", t.muted.Render("Scanned data"), t.size.Render(changeText(res.Total, floor)),
		t.muted.Render(fmt.Sprintf("(%s → %s)", engine.DecimalBytes(res.From.TotalBytes), engine.DecimalBytes(res.To.TotalBytes))))
	if res.Note != "" {
		fmt.Fprintf(&b, " %s\n", t.Wrap(t.warn.Render(res.Note), 1))
	}

	b.WriteString("\n" + t.section(t.accent, "By area", "Before → Now"))
	if len(res.Areas) == 0 {
		b.WriteString("  None of the areas macdoc tracks is big enough to be stored in a scan.\n")
	}
	for _, a := range res.Areas {
		fmt.Fprintf(&b, "  %s %s %s  %s\n", t.text.Render(fmt.Sprintf("%-26s", a.Name)),
			t.muted.Render(fmt.Sprintf("%10s", sizeText(a.Before, floor))), t.size.Render(fmt.Sprintf("%10s", sizeText(a.After, floor))),
			changeStyle(t, a.Change).Render(changeText(a.Change, floor)))
	}

	b.WriteString("\n" + t.section(t.accent, "Grew the most", engine.SpanText(res.To.StartedAt.Sub(res.From.StartedAt))))
	if len(res.Growing) == 0 {
		fmt.Fprintf(&b, "  No folder grew by %s or more.\n", engine.DecimalBytes(500_000_000))
	}
	for _, f := range res.Growing {
		fmt.Fprintf(&b, "  %s %s %s\n", t.size.Render(fmt.Sprintf("%-18s", changeText(f.Change, floor))),
			t.text.Render(TildePath(home, f.Path)), t.muted.Render("(now "+engine.DecimalBytes(f.After)+")"))
	}

	fmt.Fprintf(&b, "\n %s\n", t.Wrap(t.faint.Render(fmt.Sprintf(
		"Scans store folders of %s or more. A folder missing from a scan was smaller than that, so its size is shown as \"<%s\" and changes involving it as a lower bound (\"or more\").", floor, floor)), 1))
	b.WriteString(t.footer("macdoc doctor wins"))
	_, err := io.WriteString(w, b.String())
	return err
}

// sizeText is an exact size, "<floor" or "≥low".
func sizeText(r engine.Range, floor string) string {
	switch {
	case r.Exact():
		return engine.DecimalBytes(r.Low)
	case r.Low == 0:
		return "<" + floor
	}
	return "≥" + engine.DecimalBytes(r.Low)
}

// changeText is a signed change, or a bound when a side wasn't stored.
func changeText(r engine.Range, floor string) string {
	signed := func(n int64) string {
		if n < 0 {
			return "−" + engine.DecimalBytes(-n)
		}
		return "+" + engine.DecimalBytes(n)
	}
	switch {
	case r.Exact() && r.Low > -10_000_000 && r.Low < 10_000_000:
		return "no change"
	case r.Exact():
		return signed(r.Low)
	case r.Low > 0:
		return signed(r.Low) + " or more"
	case r.High < 0:
		return signed(r.High) + " or more"
	}
	return "under " + engine.DecimalBytes(max(-r.Low, r.High))
}

func changeStyle(t *Theme, r engine.Range) lipgloss.Style {
	switch {
	case r.Low > 0:
		return t.warn
	case r.High < 0:
		return t.Ok
	}
	return t.muted
}
