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

	"github.com/sagnik26/macdoc/internal/engine"
	"github.com/sagnik26/macdoc/internal/executor"
	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/planner"
	"github.com/sagnik26/macdoc/internal/store"
)

// RealTTY reports whether r and w are both a real terminal (an ioctl check,
// so /dev/null and pipes don't pass). Tests replace it; nothing else does.
var RealTTY = func(r io.Reader, w io.Writer) bool {
	in, ok1 := r.(*os.File)
	out, ok2 := w.(*os.File)
	return ok1 && ok2 && term.IsTerminal(in.Fd()) && term.IsTerminal(out.Fd())
}

// ApplyDeps are what apply touches, so tests can point it at a sandbox.
type ApplyDeps struct {
	Home string
	Now  func() time.Time
	exec executor.Native // nil: the real one
	Keep bool            // tests: leave moved items in quarantine instead of deleting them
}

func newApplyCmd(g *GlobalFlags) *cobra.Command {
	var safe, dryRun, list bool
	var tier, recipe, show string
	cmd := &cobra.Command{
		Use:   "apply [P-id | O-id…]",
		Short: "Plan and apply cleanups in one step, after you confirm in Terminal",
		Long: `macdoc apply picks what to remove, shows exactly what would happen, asks you
to confirm it at a real Terminal, and carries it out.

  macdoc apply O-ab12cd O-k3m9pq   the suggestions with these IDs (from macdoc reclaim)
  macdoc apply --safe              every SAFE suggestion
  macdoc apply --tier caution      every suggestion of one tier
  macdoc apply --recipe <id>       every suggestion of one recipe
  macdoc apply P-7F3K              a sealed plan, including one Claude proposed
  macdoc apply --dry-run <ids>     seal and show the plan only; nothing changes
  macdoc apply --list              recent plans and their status
  macdoc apply --show P-7F3K       a plan again, with its status

The plan is written down and sealed first and expires after 30 minutes. You
type the plan's code to confirm; RISKY items, administrator steps and every
item of a plan Claude proposed are also confirmed one by one. Each item is
checked again right before it is touched, and everything is recorded in the
audit log (macdoc log). It needs a real Terminal: piped input is refused, and
there is no --yes. Suggestions take IDs, never paths.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			selecting := safe || tier != "" || recipe != ""
			switch {
			case list:
				return listPlans(cmd, g)
			case show != "":
				return showPlan(cmd, g, show)
			case len(args) == 1 && !selecting && planner.Valid(strings.ToUpper(args[0])):
				if dryRun {
					return showPlan(cmd, g, args[0])
				}
				return applyStored(cmd, g, args[0])
			case len(args) == 0 && !selecting:
				return applyGuide(cmd, g)
			}
			return applySelection(cmd, g, args, safe, tier, recipe, dryRun)
		},
	}
	cmd.Flags().BoolVar(&safe, "safe", false, "every SAFE suggestion")
	cmd.Flags().StringVar(&tier, "tier", "", "every suggestion of one tier: safe, caution or risky")
	cmd.Flags().StringVar(&recipe, "recipe", "", "every suggestion of one recipe, e.g. xcode.derived_data")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "seal and show the plan only; change nothing")
	cmd.Flags().BoolVar(&list, "list", false, "recent plans and their status")
	cmd.Flags().StringVar(&show, "show", "", "show a plan again, with its status")
	return cmd
}

// errApplyJSON explains why applying has no JSON mode.
var errApplyJSON = errors.New("apply asks you to confirm in Terminal, so it has no --json; use --dry-run --json for the plan, or see the result with macdoc log --json")

// applyStored carries out an existing sealed plan.
func applyStored(cmd *cobra.Command, g *GlobalFlags, id string) error {
	if g.json {
		return errApplyJSON
	}
	home, err := UserHome()
	if err != nil {
		return err
	}
	return RunApply(cmd, ApplyDeps{Home: home, Now: time.Now}, id)
}

// applySelection seals a plan from what was asked for, then shows it, asks
// for confirmation and applies it, unless dryRun stops after the plan.
func applySelection(cmd *cobra.Command, g *GlobalFlags, args []string, safe bool, tier, recipe string, dryRun bool) error {
	sel, err := PlanSelector(args, safe, tier, recipe)
	if err != nil {
		return err
	}
	if !dryRun {
		if g.json {
			return errApplyJSON
		}
		// Check before sealing anything: a plan nobody can confirm is only clutter.
		if !RealTTY(cmd.InOrStdin(), cmd.OutOrStdout()) {
			return errors.New("apply needs you at a Terminal to confirm; use --dry-run to only see the plan")
		}
	}
	res, home, err := sealPlan(g, sel)
	if dryRun {
		return finishPlan(cmd.OutOrStdout(), g, res, home, err)
	}
	if err != nil {
		return finishPlan(cmd.OutOrStdout(), g, res, home, err)
	}
	var b strings.Builder
	writeRefused(&b, themeFor(cmd.OutOrStdout()), res.Refused)
	_, _ = io.WriteString(cmd.OutOrStdout(), b.String())
	return RunApply(cmd, ApplyDeps{Home: home, Now: time.Now}, res.Plan.ID)
}

// applyGuide is `macdoc apply` with nothing to apply: it removes nothing and
// says how to choose.
func applyGuide(cmd *cobra.Command, g *GlobalFlags) error {
	if g.json {
		return errors.New("say what to apply: suggestion IDs, --safe, --tier, --recipe or a plan ID")
	}
	w := cmd.OutOrStdout()
	t := themeFor(w)
	_, _ = fmt.Fprintln(w, t.heading("Nothing chosen, so nothing was changed."))
	_, _ = fmt.Fprintln(w, "  macdoc reclaim            see what you can get back, with IDs")
	_, _ = fmt.Fprintln(w, "  macdoc apply --safe       remove every SAFE suggestion (after you confirm)")
	_, _ = fmt.Fprintln(w, "  macdoc apply O-ab12cd     remove the suggestions you pick (after you confirm)")
	_, _ = fmt.Fprintln(w, "  macdoc apply --dry-run …  seal and show a plan only")
	_, _ = fmt.Fprintln(w)
	return listPlans(cmd, g)
}

// RunApply carries out plan arg after showing it again, at a real terminal and
// after the typed confirmation. It is the only path from the CLI to the
// executor's Apply.
func RunApply(cmd *cobra.Command, d ApplyDeps, arg string) error {
	return runApply(cmd, d, arg, typedConfirm, true)
}

// confirmFunc is how a plan is approved at the terminal. It returns what was
// approved and whether the person confirmed; nothing runs unless it did.
type confirmFunc func(rd *bufio.Reader, out io.Writer, t *Theme, p model.Plan) (executor.Approvals, bool, error)

// typedConfirm is apply's confirmation: y/N for RISKY items, administrator
// steps and Claude-proposed plans, then the plan's typed code.
func typedConfirm(rd *bufio.Reader, out io.Writer, t *Theme, p model.Plan) (executor.Approvals, bool, error) {
	approved, err := ConfirmItems(rd, out, t, p)
	if err != nil {
		return nil, false, err
	}
	if len(approved) == 0 {
		_, _ = fmt.Fprintln(out, " "+t.done("Nothing approved. Nothing has been changed."))
		return nil, false, nil
	}
	if !confirmCode(rd, out, t, p, approved) {
		_, _ = fmt.Fprintln(out, " "+t.done("Not confirmed. Nothing has been changed."))
		return nil, false, nil
	}
	return approved, true, nil
}

// runApply is RunApply with the confirmation step and whether to show the
// plan first as parameters. It still checks for a real terminal itself.
func runApply(cmd *cobra.Command, d ApplyDeps, arg string, confirm confirmFunc, showPlan bool) error {
	in, out := cmd.InOrStdin(), cmd.OutOrStdout()
	id := strings.ToUpper(strings.TrimSpace(arg))
	if !planner.Valid(id) {
		return fmt.Errorf("%q isn't a plan ID (they look like P-7F3K; see macdoc apply --list)", arg)
	}
	if !RealTTY(in, out) {
		return errors.New("apply needs you at a Terminal to confirm; piped or redirected input isn't accepted")
	}
	cat, err := LoadCatalog()
	if err != nil {
		return err
	}
	opts := engine.OpportunityOptions{Home: d.Home, Runner: Runner, Catalog: cat, Now: d.Now()}
	res, err := engine.ShowPlan(context.Background(), opts, id)
	if err != nil {
		return err
	}
	if showPlan {
		if err := renderApplyPlan(out, res, d.Home, d.Now()); err != nil {
			return err
		}
	}
	switch res.Status {
	case planner.Expired:
		return errors.New("this plan has expired; make a new one with macdoc apply")
	case planner.Applied:
		return errors.New("this plan was already applied; see macdoc log")
	case planner.Invalid:
		return errors.New("this plan doesn't verify, so it can't be applied")
	}

	t := themeFor(out)
	rd := bufio.NewReader(in)
	approved, ok, err := confirm(rd, out, t, res.Plan)
	if err != nil || !ok {
		return err
	}

	st, err := store.Open(store.DefaultPath(d.Home))
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	_, _ = fmt.Fprintln(out)
	rep, err := executor.Apply(ctx, executor.Config{
		Store: st, StorePath: store.DefaultPath(d.Home), Home: d.Home, Catalog: cat, Runner: Runner, Exec: d.exec, Now: d.Now,
		DeleteNow: !d.Keep,
		FreeSpace: func(ctx context.Context) (int64, error) { return engine.FreeSpace(ctx, Runner) },
		OnItem:    func(ev executor.Event) { writeProgress(out, t, ev) },
	}, res.Plan, approved)
	if err != nil {
		return fmt.Errorf("nothing was changed: %w", err)
	}
	return RenderApply(out, rep, res.Snapshot.Count)
}

// renderApplyPlan shows the dry run again, without the "to apply" footer.
func renderApplyPlan(w io.Writer, res engine.PlanResult, home string, now time.Time) error {
	if res.Status == planner.Pending {
		res.Status = "" // RenderPlan prints the apply hint only for pending plans
	}
	return RenderPlan(w, res, home, now)
}

// ConfirmItems asks y/N for RISKY items, for items that need administrator
// rights, and for every item of a plan Claude proposed. Others are approved here and confirmed by the code.
func ConfirmItems(rd *bufio.Reader, w io.Writer, t *Theme, p model.Plan) (executor.Approvals, error) {
	p = display(p) // disk-sourced text, cleaned for the terminal (display)
	approved := executor.Approvals{}
	claude := p.Origin == model.OriginClaude
	if claude {
		_, _ = fmt.Fprintln(w, " "+t.warn.Render("Claude proposed this plan "+ProposedAt(p.CreatedAt)+". Confirm each item yourself."))
	}
	for _, it := range p.Items {
		if !claude && it.Risk != model.RiskRisky && !it.Privileged {
			approved[it.OpportunityID] = executor.Approval{} // a group approval
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
			approved[it.OpportunityID] = executor.Approval{Individually: true}
		}
	}
	return approved, nil
}

// confirmCode asks for the plan's code: the part after "P-".
func confirmCode(rd *bufio.Reader, w io.Writer, t *Theme, p model.Plan, approved executor.Approvals) bool {
	p = display(p) // disk-sourced text, cleaned for the terminal (display)
	code := strings.TrimPrefix(p.ID, "P-")
	var bytes int64
	tiers := map[model.Risk]int{}
	n := 0
	for _, it := range p.Items {
		if _, ok := approved[it.OpportunityID]; !ok {
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
	_, _ = fmt.Fprintf(w, "\n Type %s to apply %d item(s), %s (%s): ", t.ID.Render(`"`+code+`"`), n, engine.DecimalBytes(bytes), strings.Join(parts, ", "))
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

func writeProgress(w io.Writer, t *Theme, ev executor.Event) {
	ev = display(ev) // disk-sourced text, cleaned for the terminal (display)
	it := ev.Item
	if ev.Result == nil {
		_, _ = fmt.Fprintf(w, " [%d/%d] %s %s … ", ev.Index, ev.Total, t.ID.Render(it.OpportunityID), it.Title)
		return
	}
	r := ev.Result
	switch r.Outcome {
	case executor.OutcomeDone:
		if r.QuarantineID != "" && !r.Deleted {
			_, _ = fmt.Fprintf(w, "%s %s\n", t.warn.Render("moved aside, not deleted:"), t.ID.Render(r.QuarantineID))
		} else {
			_, _ = fmt.Fprintln(w, t.Ok.Render("done"))
		}
	case executor.OutcomeSkipped:
		_, _ = fmt.Fprintf(w, "%s %s\n", t.warn.Render("skipped:"), t.muted.Render(r.Reason))
	default:
		_, _ = fmt.Fprintf(w, "%s %s\n", t.bad.Render("failed:"), t.muted.Render(r.Reason))
	}
}

// RenderApply writes the summary of an apply in the human format: counts,
// measured space, anything
// left in quarantine and why the numbers may differ.
// snapshots is how many local Time Machine snapshots existed.
func RenderApply(w io.Writer, rep executor.Report, snapshots int) error {
	t := themeFor(w)
	rep = display(rep) // disk-sourced text, cleaned for the terminal (display)
	var b strings.Builder
	fmt.Fprintf(&b, "\n %s\n", t.heading(fmt.Sprintf("Plan %s applied: %d done · %d skipped · %d failed",
		rep.PlanID, rep.Count(executor.OutcomeDone), rep.Count(executor.OutcomeSkipped), rep.Count(executor.OutcomeFailed))))
	if freed, ok := rep.Freed(); ok {
		fmt.Fprintf(&b, " %s %s\n", t.muted.Render("Free space change (measured):"), t.size.Render(signedBytes(freed)))
	} else {
		fmt.Fprintf(&b, " %s\n", t.muted.Render("Free space couldn't be measured before and after."))
	}
	expected := rep.NativeExpected() + rep.DeletedBytes()
	if expected > 0 {
		fmt.Fprintf(&b, " %s %s\n", t.muted.Render("Expected:"), t.size.Render(engine.DecimalBytes(expected)))
	}
	if n := rep.MovedBytes(); n > 0 {
		fmt.Fprintf(&b, " %s %s %s\n", t.muted.Render("Moved to quarantine:"), t.size.Render(engine.DecimalBytes(n)),
			t.muted.Render("(couldn't be deleted, so it's still on disk; macdoc quarantine restore <Q-id> or macdoc quarantine purge)"))
	}
	if freed, ok := rep.Freed(); ok && freed < expected {
		var why []string
		if snapshots > 0 {
			why = append(why, fmt.Sprintf("%d local Time Machine snapshot(s) still reference some of it", snapshots))
		}
		why = append(why, "files that share space with copies elsewhere free less than their size", "macOS may count freed space as purgeable for a while", "other apps may have written files meanwhile")
		fmt.Fprintf(&b, " %s\n", t.muted.Render("Less than expected: "+strings.Join(why, "; ")+"."))
	}
	if freed, ok := rep.Freed(); ok && expected > 0 && freed > expected {
		fmt.Fprintf(&b, " %s\n", t.muted.Render("Expected is the space removal was certain to free; more can be freed when the last copy of shared files goes."))
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

// ProposedAt says when a plan was made, in local time: "at 14:02 on 2 Nov".
func ProposedAt(t time.Time) string {
	return t.Local().Format("at 15:04 on 2 Jan")
}
