package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/spf13/cobra"

	"macdoc/internal/engine"
	"macdoc/internal/executor"
	"macdoc/internal/model"
	"macdoc/internal/planner"
	"macdoc/internal/store"
)

// realTTY reports whether r and w are both a real terminal (an ioctl check,
// so /dev/null and pipes don't pass). Tests replace it; nothing else does.
var realTTY = func(r io.Reader, w io.Writer) bool {
	in, ok1 := r.(*os.File)
	out, ok2 := w.(*os.File)
	return ok1 && ok2 && term.IsTerminal(in.Fd()) && term.IsTerminal(out.Fd())
}

// applyDeps are what apply touches, so tests can point it at a sandbox.
type applyDeps struct {
	home string
	now  func() time.Time
	exec executor.NativeExec // nil: the real one
}

func newApplyCmd(g *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "apply <P-id>",
		Short: "Carry out a sealed plan, after you confirm it here in Terminal",
		Long: `macdoc apply carries out a plan made with "macdoc plan". It shows the dry
run again and asks you to type the plan's code. RISKY items, and every item
of a plan Claude proposed, are also confirmed one by one.

Each item is checked again right before it's touched; anything that changed
since the plan is skipped. Folders are moved into macdoc's quarantine, where
they stay restorable for 7 days (macdoc quarantine). Native commands are the
tools' own cleanups. Everything is recorded in the audit log (macdoc log).

It needs a real Terminal: piped input is refused, and there is no --yes.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if g.json {
				return errors.New("apply asks you to confirm in Terminal, so it has no --json; see the result with macdoc log --json")
			}
			home, err := userHome()
			if err != nil {
				return err
			}
			return runApply(cmd, applyDeps{home: home, now: time.Now}, args[0])
		},
	}
}

func runApply(cmd *cobra.Command, d applyDeps, arg string) error {
	in, out := cmd.InOrStdin(), cmd.OutOrStdout()
	id := strings.ToUpper(strings.TrimSpace(arg))
	if !planner.ValidID(id) {
		return fmt.Errorf("%q isn't a plan ID (they look like P-7F3K; see macdoc plan list)", arg)
	}
	if !realTTY(in, out) {
		return errors.New("apply needs you at a Terminal to confirm; piped or redirected input isn't accepted")
	}
	cat, err := loadCatalog()
	if err != nil {
		return err
	}
	opts := engine.OpportunityOptions{Home: d.home, Runner: runner, Catalog: cat, Now: d.now()}
	res, err := engine.ShowPlan(context.Background(), opts, id)
	if err != nil {
		return err
	}
	if err := renderApplyPlan(out, res, d.home, d.now()); err != nil {
		return err
	}
	switch res.Status {
	case planner.StatusExpired:
		return errors.New("this plan has expired; make a new one with macdoc plan")
	case planner.StatusApplied:
		return errors.New("this plan was already applied; see macdoc log")
	case planner.StatusInvalid:
		return errors.New("this plan doesn't verify, so it can't be applied")
	}

	t := themeFor(out)
	rd := bufio.NewReader(in)
	approved, err := confirmItems(rd, out, t, res.Plan)
	if err != nil {
		return err
	}
	if len(approved) == 0 {
		_, _ = fmt.Fprintln(out, " "+t.done("Nothing approved. Nothing has been changed."))
		return nil
	}
	if !confirmCode(rd, out, t, res.Plan, approved) {
		_, _ = fmt.Fprintln(out, " "+t.done("Not confirmed. Nothing has been changed."))
		return nil
	}

	st, err := store.Open(store.DefaultPath(d.home))
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	_, _ = fmt.Fprintln(out)
	rep, err := executor.Apply(ctx, executor.Config{
		Store: st, StorePath: store.DefaultPath(d.home), Home: d.home, Catalog: cat, Runner: runner, Exec: d.exec, Now: d.now,
		FreeSpace: func(ctx context.Context) (int64, error) { return engine.FreeSpace(ctx, runner) },
		OnItem:    func(ev executor.ItemEvent) { writeProgress(out, t, ev) },
	}, res.Plan, approved)
	if err != nil {
		return fmt.Errorf("nothing was changed: %w", err)
	}
	return renderApplyReport(out, t, rep, res.Snapshot.Count)
}

// renderApplyPlan shows the dry run again, without the "to apply" footer.
func renderApplyPlan(w io.Writer, res engine.PlanResult, home string, now time.Time) error {
	if res.Status == planner.StatusPending {
		res.Status = "" // renderPlan prints the apply hint only for pending plans
	}
	return renderPlan(w, res, home, now)
}

// confirmItems asks y/N for RISKY items, and for every item of a plan
// Claude proposed. Others are approved here and confirmed by the code.
func confirmItems(rd *bufio.Reader, w io.Writer, t *theme, p model.Plan) (map[string]bool, error) {
	approved := map[string]bool{}
	claude := p.Origin == model.OriginClaude
	if claude {
		_, _ = fmt.Fprintln(w, " "+t.warn.Render("Claude proposed this plan. Confirm each item yourself."))
	}
	for _, it := range p.Items {
		if !claude && it.Risk != model.RiskRisky {
			approved[it.OpportunityID] = true
			continue
		}
		q := fmt.Sprintf(" %s %s %s (%s)? [y/N] ", t.riskStyle(it.Risk).Render(string(it.Risk)), it.OpportunityID, it.Title,
			sizeCell(model.Bytes{Value: it.ExpectedBytes, Source: it.BytesSource}))
		_, _ = io.WriteString(w, q)
		ans, err := readLine(rd)
		if err != nil {
			return nil, errors.New("no answer; nothing has been changed")
		}
		if a := strings.ToLower(ans); a == "y" || a == "yes" {
			approved[it.OpportunityID] = true
		}
	}
	return approved, nil
}

// confirmCode asks for the plan's code: the part after "P-".
func confirmCode(rd *bufio.Reader, w io.Writer, t *theme, p model.Plan, approved map[string]bool) bool {
	code := strings.TrimPrefix(p.ID, "P-")
	var bytes int64
	tiers := map[model.Risk]int{}
	n := 0
	for _, it := range p.Items {
		if !approved[it.OpportunityID] {
			continue
		}
		n++
		tiers[it.Risk]++
		if it.BytesSource != model.SourceUnknown {
			bytes += it.ExpectedBytes
		}
	}
	var parts []string
	for _, r := range []model.Risk{model.RiskSafe, model.RiskCaution, model.RiskRisky} {
		if tiers[r] > 0 {
			parts = append(parts, fmt.Sprintf("%s: %d", r, tiers[r]))
		}
	}
	_, _ = fmt.Fprintf(w, "\n Type %s to apply %d item(s), %s (%s): ", t.id.Render(`"`+code+`"`), n, engine.DecimalBytes(bytes), strings.Join(parts, ", "))
	ans, err := readLine(rd)
	return err == nil && strings.EqualFold(strings.TrimSpace(ans), code)
}

func readLine(rd *bufio.Reader) (string, error) {
	s, err := rd.ReadString('\n')
	if err != nil && (s == "" || !errors.Is(err, io.EOF)) {
		return "", err
	}
	return strings.TrimSpace(s), nil
}

func writeProgress(w io.Writer, t *theme, ev executor.ItemEvent) {
	it := ev.Item
	if ev.Result == nil {
		_, _ = fmt.Fprintf(w, " [%d/%d] %s %s … ", ev.Index, ev.Total, t.id.Render(it.OpportunityID), it.Title)
		return
	}
	r := ev.Result
	switch r.Outcome {
	case executor.OutcomeDone:
		if r.QuarantineID != "" {
			_, _ = fmt.Fprintf(w, "%s %s\n", t.ok.Render("moved to quarantine"), t.id.Render(r.QuarantineID))
		} else {
			_, _ = fmt.Fprintln(w, t.ok.Render("done"))
		}
	case executor.OutcomeSkipped:
		_, _ = fmt.Fprintf(w, "%s %s\n", t.warn.Render("skipped:"), t.muted.Render(r.Reason))
	default:
		_, _ = fmt.Fprintf(w, "%s %s\n", t.bad.Render("failed:"), t.muted.Render(r.Reason))
	}
}

// renderApplyReport summarizes an apply: counts, measured space, what's in
// quarantine and why the numbers may differ.
func renderApplyReport(w io.Writer, t *theme, rep executor.Report, snapshots int) error {
	var b strings.Builder
	fmt.Fprintf(&b, "\n %s\n", t.heading(fmt.Sprintf("Plan %s applied: %d done · %d skipped · %d failed",
		rep.PlanID, rep.Count(executor.OutcomeDone), rep.Count(executor.OutcomeSkipped), rep.Count(executor.OutcomeFailed))))
	if freed, ok := rep.Freed(); ok {
		fmt.Fprintf(&b, " %s %s\n", t.muted.Render("Free space change (measured):"), t.size.Render(signedBytes(freed)))
	} else {
		fmt.Fprintf(&b, " %s\n", t.muted.Render("Free space couldn't be measured before and after."))
	}
	if n := rep.NativeExpected(); n > 0 {
		fmt.Fprintf(&b, " %s %s\n", t.muted.Render("Expected from the tools' own commands:"), t.size.Render(engine.DecimalBytes(n)))
	}
	if n := rep.MovedBytes(); n > 0 {
		fmt.Fprintf(&b, " %s %s %s\n", t.muted.Render("Moved to quarantine:"), t.size.Render(engine.DecimalBytes(n)),
			t.muted.Render("(still on disk; restorable for 7 days with macdoc quarantine restore <Q-id>, freed by macdoc quarantine purge)"))
	}
	if freed, ok := rep.Freed(); ok && freed < rep.NativeExpected() {
		var why []string
		if snapshots > 0 {
			why = append(why, fmt.Sprintf("%d local Time Machine snapshot(s) still reference some of it", snapshots))
		}
		why = append(why, "macOS may count freed space as purgeable for a while", "other apps may have written files meanwhile")
		fmt.Fprintf(&b, " %s\n", t.muted.Render("Less than expected: "+strings.Join(why, "; ")+"."))
	}
	writeNotes(&b, t, rep.Warnings)
	fmt.Fprintf(&b, " %s\n", t.muted.Render("Every step is in the audit log: macdoc log"))
	_, err := io.WriteString(w, b.String())
	return err
}

func signedBytes(n int64) string {
	if n < 0 {
		return "-" + engine.DecimalBytes(-n)
	}
	return "+" + engine.DecimalBytes(n)
}
