package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"macdoc/internal/engine"
	"macdoc/internal/executor"
	"macdoc/internal/quarantine"
	"macdoc/internal/store"
)

func newQuarantineCmd(g *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "quarantine",
		Short: "See, restore or purge folders that apply moved aside",
		Long: `Folders that apply cleans are moved into macdoc's quarantine instead of
being deleted. They stay restorable for 7 days; after that they're reported
as ready to purge, but nothing is ever purged without you.

  macdoc quarantine list            what's held, and what's expired
  macdoc quarantine restore Q-91XZ  put one back where it was
  macdoc quarantine purge --expired permanently delete expired entries
  macdoc quarantine purge Q-91XZ    permanently delete one entry`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return runQuarantineList(cmd, g, time.Now()) },
	}
	list := &cobra.Command{
		Use:   "list",
		Short: "What's held in quarantine (changes nothing)",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return runQuarantineList(cmd, g, time.Now()) },
	}
	restore := &cobra.Command{
		Use:   "restore <Q-id>",
		Short: "Move a quarantined folder back where it was (never overwrites)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			home, err := userHome()
			if err != nil {
				return err
			}
			return runRestore(cmd, g, home, args[0], nil)
		},
	}
	var expired bool
	purge := &cobra.Command{
		Use:   "purge (--expired | <Q-id>...)",
		Short: "Permanently delete quarantine entries, after you confirm in Terminal",
		RunE: func(cmd *cobra.Command, args []string) error {
			home, err := userHome()
			if err != nil {
				return err
			}
			return runPurge(cmd, g, home, args, expired, time.Now())
		},
	}
	purge.Flags().BoolVar(&expired, "expired", false, "every entry past its 7 days")
	cmd.AddCommand(list, restore, purge)
	return cmd
}

func runQuarantineList(cmd *cobra.Command, g *globalFlags, now time.Time) error {
	home, err := userHome()
	if err != nil {
		return err
	}
	res, err := engine.Quarantine(engine.OpportunityOptions{Home: home, Now: now})
	if err != nil {
		return err
	}
	if g.json {
		return writeJSON(cmd.OutOrStdout(), "quarantine list", res)
	}
	return renderQuarantine(cmd.OutOrStdout(), res, home, now)
}

func renderQuarantine(w io.Writer, res engine.QuarantineResult, home string, now time.Time) error {
	var b strings.Builder
	t := themeFor(w)
	if len(res.Entries) == 0 {
		b.WriteString(" The quarantine is empty.\n")
	} else {
		fmt.Fprintf(&b, " %s\n", t.heading(fmt.Sprintf("Quarantine · %d entr%s · %s held", len(res.Entries), plural(len(res.Entries), "y", "ies"), engine.DecimalBytes(res.HeldBytes))))
	}
	for _, e := range res.Entries {
		r := e.Record
		state := string(e.State)
		st := t.ok
		switch {
		case e.State == quarantine.StateHeld && e.Expired:
			state, st = "expired", t.warn
		case e.State == quarantine.StateHeld:
			state = "held"
		case e.Restorable():
			st = t.warn
		default:
			st = t.muted
		}
		when := "until " + r.ExpiresAt.Local().Format("Jan 2")
		if e.Expired {
			when = "expired " + engine.FormatAge(now.Sub(r.ExpiresAt)) + " ago"
		}
		fmt.Fprintf(&b, "  %s  %s  %s  %s\n", t.id.Render(r.ID), st.Render(fmt.Sprintf("%-11s", state)),
			t.size.Render(fmt.Sprintf("%9s", engine.DecimalBytes(r.Bytes))), t.text.Render(tildePath(home, r.Original)))
		note := fmt.Sprintf("from %s · %s · %s", r.PlanID, r.RecipeID, when)
		if e.Problem != "" {
			note += " · " + e.Problem
		}
		fmt.Fprintf(&b, "             %s\n", t.muted.Render(note))
	}
	if res.ExpiredCount > 0 {
		fmt.Fprintf(&b, "\n %s %s\n", t.warn.Render(fmt.Sprintf("%s in %d expired entr%s ready to purge:", engine.DecimalBytes(res.ExpiredBytes), res.ExpiredCount, plural(res.ExpiredCount, "y", "ies"))),
			t.command([]string{"macdoc", "quarantine", "purge", "--expired"}))
	}
	if len(res.Unknown) > 0 {
		fmt.Fprintf(&b, "\n %s\n", t.muted.Render("Not macdoc's (never touched): "+strings.Join(res.Unknown, ", ")))
	}
	if len(res.Entries) > 0 {
		fmt.Fprintf(&b, "\n %s %s\n", t.muted.Render("Put one back:"), t.command([]string{"macdoc", "quarantine", "restore", "<Q-id>"}))
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// executorConfig is the executor setup for quarantine commands.
func executorConfig(home string, st *store.Store, now func() time.Time) (executor.Config, error) {
	cat, err := loadCatalog()
	if err != nil {
		return executor.Config{}, err
	}
	return executor.Config{Store: st, StorePath: store.DefaultPath(home), Home: home, Catalog: cat, Runner: runner, Now: now}, nil
}

func runRestore(cmd *cobra.Command, g *globalFlags, home, arg string, now func() time.Time) error {
	if now == nil {
		now = time.Now
	}
	st, err := store.Open(store.DefaultPath(home))
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	cfg, err := executorConfig(home, st, now)
	if err != nil {
		return err
	}
	res, err := executor.Restore(context.Background(), cfg, strings.ToUpper(strings.TrimSpace(arg)))
	if err != nil {
		return err
	}
	if g.json {
		return writeJSON(cmd.OutOrStdout(), "quarantine restore", res)
	}
	t := themeFor(cmd.OutOrStdout())
	_, err = fmt.Fprintf(cmd.OutOrStdout(), " %s %s %s\n", t.done("Restored"), t.id.Render(res.ID), t.text.Render("to "+tildePath(home, res.Original)))
	return err
}

// runPurge permanently deletes entries after a typed confirmation in a
// real terminal.
func runPurge(cmd *cobra.Command, g *globalFlags, home string, args []string, expired bool, now time.Time) error {
	in, out := cmd.InOrStdin(), cmd.OutOrStdout()
	if expired == (len(args) > 0) {
		return errors.New("say what to purge: --expired, or quarantine IDs (macdoc quarantine purge Q-91XZ)")
	}
	if g.json {
		return errors.New("purge asks you to confirm in Terminal, so it has no --json")
	}
	if !realTTY(in, out) {
		return errors.New("purge needs you at a Terminal to confirm; piped or redirected input isn't accepted")
	}
	res, err := engine.Quarantine(engine.OpportunityOptions{Home: home, Now: now})
	if err != nil {
		return err
	}
	byID := map[string]quarantine.Entry{}
	for _, e := range res.Entries {
		byID[e.Record.ID] = e
	}
	var ids []string
	var bytes int64
	if expired {
		for _, e := range res.Expired() {
			ids = append(ids, e.Record.ID)
			bytes += e.Record.Bytes
		}
	} else {
		for _, a := range args {
			id := strings.ToUpper(strings.TrimSpace(a))
			e, ok := byID[id]
			if !quarantine.ValidID(id) || !ok {
				return fmt.Errorf("%q isn't an entry in macdoc's quarantine (see macdoc quarantine list)", a)
			}
			ids = append(ids, id)
			bytes += e.Record.Bytes
		}
	}
	t := themeFor(out)
	if len(ids) == 0 {
		_, _ = fmt.Fprintln(out, " Nothing has expired yet. Nothing has been changed.")
		return nil
	}
	for _, id := range ids {
		e := byID[id]
		_, _ = fmt.Fprintf(out, "  %s  %9s  %s\n", t.id.Render(id), engine.DecimalBytes(e.Record.Bytes), tildePath(home, e.Record.Original))
	}
	_, _ = fmt.Fprintf(out, "\n %s Type %s to permanently delete %d entr%s (%s): ", t.warn.Render("This can't be undone."),
		t.id.Render(`"purge"`), len(ids), plural(len(ids), "y", "ies"), engine.DecimalBytes(bytes))
	ans, err := readLine(bufio.NewReader(in))
	if err != nil || ans != "purge" {
		_, _ = fmt.Fprintln(out, " "+t.done("Not confirmed. Nothing has been changed."))
		return nil
	}
	st, err := store.Open(store.DefaultPath(home))
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	cfg, err := executorConfig(home, st, func() time.Time { return now })
	if err != nil {
		return err
	}
	results, err := executor.Purge(context.Background(), cfg, ids)
	if err != nil {
		return err
	}
	var freed int64
	for _, r := range results {
		switch r.Outcome {
		case executor.OutcomeDone:
			freed += r.Bytes
			_, _ = fmt.Fprintf(out, "  %s %s\n", t.id.Render(r.ID), t.ok.Render("purged"))
		default:
			_, _ = fmt.Fprintf(out, "  %s %s %s\n", t.id.Render(r.ID), t.warn.Render(string(r.Outcome)+":"), t.muted.Render(r.Reason))
		}
	}
	_, err = fmt.Fprintf(out, "\n %s\n", t.done(fmt.Sprintf("Purged %s. Recorded in the audit log (macdoc log).", engine.DecimalBytes(freed))))
	return err
}
