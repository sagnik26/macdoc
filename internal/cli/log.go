package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sagnik26/macdoc/internal/audit"
	"github.com/sagnik26/macdoc/internal/engine"
)

func newLogCmd(g *GlobalFlags) *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "log",
		Short: "What macdoc has changed: plans, applies, restores and purges (changes nothing)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := UserHome()
			if err != nil {
				return err
			}
			res, err := engine.AuditLog(engine.OpportunityOptions{Home: home}, limit)
			if err != nil {
				return err
			}
			if g.json {
				return writeJSON(cmd.OutOrStdout(), "log", res)
			}
			return RenderLog(cmd.OutOrStdout(), res, home)
		},
	}
	cmd.Flags().IntVarP(&limit, "limit", "n", 50, "show the last N records (0: all)")
	return cmd
}

// RenderLog writes the audit log in the human format.
func RenderLog(w io.Writer, res engine.AuditResult, home string) error {
	res = display(res) // disk-sourced text, cleaned for the terminal (display)
	var b strings.Builder
	t := themeFor(w)
	if len(res.Records) == 0 {
		b.WriteString(" Nothing recorded yet. Plans, applies, restores and purges appear here.\n")
	}
	for _, r := range res.Records {
		fmt.Fprintf(&b, " %s  %s\n", t.muted.Render(r.TS.Local().Format("Jan 02 15:04:05")), logLine(t, r, home))
	}
	if res.BadLines > 0 {
		fmt.Fprintf(&b, "\n %s\n", t.muted.Render(fmt.Sprintf("%d unreadable line(s) skipped (an interrupted write).", res.BadLines)))
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func logLine(t *Theme, r audit.Record, home string) string {
	what := r.Target
	if what != "" {
		what = TildePath(home, what)
	} else if len(r.Argv) > 0 {
		what = strings.Join(r.Argv, " ")
	}
	reason := ""
	if r.Reason != "" {
		reason = " · " + r.Reason
	}
	switch r.Event {
	case audit.Planned:
		return fmt.Sprintf("%s %s · %d item(s) · %s%s", t.text.Render("plan"), t.ID.Render(r.Plan), r.Items, engine.DecimalBytes(r.Expected), t.muted.Render(" ("+r.Origin+")"))
	case audit.Started:
		return fmt.Sprintf("%s %s · %d item(s)", t.text.Render("apply started"), t.ID.Render(r.Plan), r.Items)
	case audit.Done:
		q := ""
		if r.Entry != "" {
			q = " → " + r.Entry
		}
		return fmt.Sprintf("  %s %s %s%s", t.Ok.Render("done"), t.ID.Render(r.Item), what, q)
	case audit.Skipped:
		return fmt.Sprintf("  %s %s %s%s", t.warn.Render("skipped"), t.ID.Render(r.Item), what, t.muted.Render(reason))
	case audit.Failed:
		return fmt.Sprintf("  %s %s %s%s", t.bad.Render("failed"), t.ID.Render(r.Item), what, t.muted.Render(reason))
	case audit.Finished:
		s := fmt.Sprintf("%s %s · %s", t.text.Render("apply finished"), t.ID.Render(r.Plan), r.Result)
		if r.Actual != nil {
			s += " · free space " + signedBytes(*r.Actual)
		}
		return s
	case audit.Restored:
		return fmt.Sprintf("%s %s to %s", t.text.Render("restored"), t.ID.Render(r.Entry), what)
	case audit.Purged:
		if r.Result == "ok" {
			return fmt.Sprintf("%s %s %s", t.text.Render("purged"), t.ID.Render(r.Entry), what)
		}
		return fmt.Sprintf("%s %s%s", t.warn.Render("purge refused"), t.ID.Render(r.Entry), t.muted.Render(reason))
	}
	return r.Event
}
