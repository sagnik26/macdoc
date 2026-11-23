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

	"github.com/sagnik26/macdoc/internal/executor"
	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/planner"
)

// This file is `macdoc reclaim --tier safe|caution --yes` (ADR 0014 §5): the
// one place a cleanup runs with no person confirming it at a terminal. It only
// moves SAFE or CAUTION items into quarantine; everything else, and every
// other command, still needs a person. `macdoc apply` has no --yes.

// checkYes validates the --yes request before anything is scanned or listed.
func checkYes(cmd *cobra.Command) (cleanOpts, error) {
	if t, _ := cmd.Flags().GetString("tier"); t == "" {
		return cleanOpts{}, errors.New("--yes needs --tier safe or --tier caution, so it is clear what gets cleaned")
	}
	o, ok, err := cleanOptions(cmd, nil)
	if err != nil {
		return o, err
	}
	if !ok {
		return o, errors.New("--yes cleans only the safe or caution tier; risky items need you at the terminal (macdoc apply)")
	}
	return o, nil
}

// cleanYes cleans one tier with no prompt. toolchain, when given, limits it.
func cleanYes(cmd *cobra.Command, g *GlobalFlags, o cleanOpts, args []string) error {
	out := cmd.OutOrStdout()
	if len(args) == 1 {
		tc, err := parseToolchain(args[0])
		if err != nil {
			return err
		}
		o.toolchain = tc
	}
	home, err := UserHome()
	if err != nil {
		return err
	}
	if err := ensureFreshScan(cmd, home); err != nil {
		return err
	}
	res, _, err := loadOpportunities(context.Background(), minBytesFor(cmd))
	if err != nil {
		return err
	}
	pick, _ := splitForCleaning(forReclaim(res), o)
	pick, left := onlyQuarantine(pick)
	t := themeFor(out)
	if len(pick) == 0 {
		_, _ = fmt.Fprintln(out, t.done("Nothing to clean at the "+strings.ToLower(tierNames[o.tier])+" level with --yes."))
		return nil
	}
	_, _ = fmt.Fprintf(out, " --yes: cleaning %d %s (%s) at the %s level, with no prompt.\n",
		len(pick), plural(len(pick), "item", "items"), sizeSummary(pick), strings.ToLower(tierNames[o.tier]))
	if left > 0 {
		_, _ = fmt.Fprintf(out, " %d %s left for you (not a move into quarantine; use macdoc reclaim at a terminal).\n", left, plural(left, "item", "items"))
	}
	ids := make([]string, len(pick))
	for i, op := range pick {
		ids[i] = op.ID
	}
	plan, home, err := sealPlan(g, planner.Selector{IDs: ids})
	if err != nil {
		return finishPlan(out, g, plan, home, err)
	}
	var b strings.Builder
	writeRefused(&b, t, plan.Refused)
	_, _ = io.WriteString(out, b.String())
	return runApply(cmd, ApplyDeps{Home: home, Now: time.Now, NonInteractive: true}, plan.Plan.ID, confirmYes, false)
}

// onlyQuarantine keeps the items --yes may clean and counts the rest.
func onlyQuarantine(ops []model.Opportunity) (keep []model.Opportunity, left int) {
	for _, op := range ops {
		if op.Method == model.MethodQuarantine {
			keep = append(keep, op)
		} else {
			left++
		}
	}
	return keep, left
}

// confirmYes approves a plan for --yes. It refuses a plan with anything but
// SAFE or CAUTION quarantine moves made here, so a plan that isn't one never
// runs without a person.
func confirmYes(_ *bufio.Reader, out io.Writer, _ *Theme, p model.Plan) (executor.Approvals, bool, error) {
	if p.Origin != model.OriginCLI && p.Origin != "" {
		return nil, false, errors.New("--yes only cleans a plan made here; use macdoc apply " + p.ID)
	}
	approved := executor.Approvals{}
	for _, it := range p.Items {
		if it.Method != model.MethodQuarantine || it.Privileged || (it.Risk != model.RiskSafe && it.Risk != model.RiskCaution) {
			return nil, false, fmt.Errorf("%s needs you at the terminal; use macdoc apply %s", it.OpportunityID, p.ID)
		}
		approved[it.OpportunityID] = executor.Approval{}
	}
	_, _ = fmt.Fprintln(out)
	return approved, true, nil
}
