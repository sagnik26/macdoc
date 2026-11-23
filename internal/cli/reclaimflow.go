package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sagnik26/macdoc/internal/engine"
	"github.com/sagnik26/macdoc/internal/executor"
	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/planner"
	"github.com/sagnik26/macdoc/internal/scanner"
)

// This file is `macdoc reclaim` at a real terminal: it shows what will be
// cleaned, and cleans it when the person presses Enter (ADR 0013). It is the
// same executor path as the dashboard's remove, with the confirmation being
// that one Enter instead of a typed code. Only SAFE items (or CAUTION when asked for
// with --tier caution) are ever cleaned this way; RISKY items, administrator
// steps and Claude-proposed plans still need their own confirmations.

// cleanOpts is what reclaim was asked to show and clean.
type cleanOpts struct {
	tier      model.Risk // SAFE (the default) or CAUTION; used when tierSet
	tierSet   bool       // --tier was given: one tier, one Enter, no chooser
	toolchain string     // "" for every toolchain
	all       bool       // list repeated items one by one
}

// canCleanOnEnter reports whether reclaim may offer to clean: only at a real
// terminal, and never with --json, --quiet or --dry-run.
func canCleanOnEnter(cmd *cobra.Command, g *GlobalFlags, dryRun bool) bool {
	return !g.json && !g.quiet && !dryRun && RealTTY(cmd.InOrStdin(), cmd.OutOrStdout())
}

// cleanOnEnter lists what can be cleaned and cleans it on Enter: the chosen
// risk types, or with --tier just that one.
func cleanOnEnter(cmd *cobra.Command, g *GlobalFlags, o cleanOpts) error {
	if !o.tierSet {
		return cleanChosen(cmd, g, o)
	}
	return cleanOneTier(cmd, g, o)
}

// cleanOneTier is the --tier flow: show one tier, clean it on Enter.
func cleanOneTier(cmd *cobra.Command, g *GlobalFlags, o cleanOpts) error {
	out := cmd.OutOrStdout()
	home, err := UserHome()
	if err != nil {
		return err
	}
	if err := ensureFreshScan(cmd, home); err != nil {
		return err
	}
	res, cat, err := loadOpportunities(context.Background(), minBytesFor(cmd))
	if err != nil {
		return err
	}
	res = forReclaim(res)
	pick, other := splitForCleaning(res, o)
	t := themeFor(out)
	if len(pick) == 0 {
		_, _ = fmt.Fprintln(out, t.done("Nothing to clean at the "+strings.ToLower(tierNames[o.tier])+" level."))
		if note := notIncludedNote(other, o); note != "" {
			_, _ = fmt.Fprintln(out, note)
		}
		return nil
	}

	shown := res
	shown.Opportunities = pick
	shown.Totals = map[model.Risk]int64{o.tier: knownTotal(pick)}
	if err := renderWinsList(out, shown, cat, o.all, o.tier, ""); err != nil {
		return err
	}
	if note := notIncludedNote(other, o); note != "" {
		_, _ = fmt.Fprintln(out, note)
	}
	_, _ = fmt.Fprintf(out, "\n Press %s to clean %d %s (%s), or type anything else and Enter to cancel: ",
		t.accent.Render("Enter"), len(pick), plural(len(pick), "item", "items"), sizeSummary(pick))
	line, err := readLine(bufio.NewReader(cmd.InOrStdin()))
	if err != nil {
		return errors.New("no answer; nothing has been changed")
	}
	if strings.TrimSpace(line) != "" {
		_, _ = fmt.Fprintln(out, " "+t.done("Canceled. Nothing has been changed."))
		return nil
	}

	ids := make([]string, len(pick))
	for i, op := range pick {
		ids[i] = op.ID
	}
	plan, home, err := sealPlan(g, planner.Selector{IDs: ids})
	if err != nil {
		return finishPlan(out, plan, err)
	}
	var b strings.Builder
	writeRefused(&b, t, plan.Refused)
	_, _ = io.WriteString(out, b.String())
	return runApply(cmd, ApplyDeps{Home: home, Now: time.Now}, plan.Plan.ID, confirmEnteredList, false)
}

// splitForCleaning separates what this run cleans from the other actionable
// items it leaves out. Only the asked-for tier is cleaned, advice-only items
// never are, and a toolchain limits both lists.
func splitForCleaning(res engine.OpportunityResult, o cleanOpts) (pick, other []model.Opportunity) {
	for _, op := range res.Opportunities {
		if op.Method == model.MethodAdvise || (o.toolchain != "" && op.Toolchain != o.toolchain) {
			continue
		}
		switch op.Risk {
		case o.tier:
			pick = append(pick, op)
		case model.RiskSafe, model.RiskCaution:
			other = append(other, op)
		}
	}
	return pick, other
}

// notIncludedNote says how many other actionable items were left out and how
// to see them.
func notIncludedNote(other []model.Opportunity, o cleanOpts) string {
	next := model.RiskCaution
	if o.tier == model.RiskCaution {
		next = model.RiskSafe
	}
	var left []model.Opportunity
	for _, op := range other {
		if op.Risk == next {
			left = append(left, op)
		}
	}
	if len(left) == 0 {
		return ""
	}
	return fmt.Sprintf(" %s: %d %s (%s) not included. See them with macdoc reclaim --tier %s.",
		next, len(left), plural(len(left), "item", "items"), sizeSummary(left), strings.ToLower(string(next)))
}

// knownTotal is the sum of the sizes that are known.
func knownTotal(ops []model.Opportunity) int64 {
	var sum int64
	for _, op := range ops {
		if op.Bytes.Source != model.SourceUnknown {
			sum += op.Bytes.Value
		}
	}
	return sum
}

// sizeSummary is "1.4 GB", or "1.4 GB + unknown" when some sizes are unknown.
func sizeSummary(ops []model.Opportunity) string {
	s := engine.DecimalBytes(knownTotal(ops))
	for _, op := range ops {
		if op.Bytes.Source == model.SourceUnknown {
			return s + " + unknown"
		}
	}
	return s
}

// confirmEnteredList approves a plan whose items the person has just seen and
// accepted with Enter. It refuses any plan that needs the typed or per-item
// confirmation, so this path can never clean more than SAFE and CAUTION items
// from a plan made here by a person.
func confirmEnteredList(_ *bufio.Reader, out io.Writer, _ *Theme, p model.Plan) (executor.Approvals, bool, error) {
	if p.Origin != model.OriginCLI && p.Origin != "" {
		return nil, false, errors.New("only a plan you made here is cleaned on Enter")
	}
	approved := executor.Approvals{}
	for _, it := range p.Items {
		if it.Risk == model.RiskRisky || it.Privileged || it.Method == model.MethodAdvise {
			return nil, false, fmt.Errorf("%s needs your own confirmation; choose it by name (risky) so each one is confirmed", it.OpportunityID)
		}
		approved[it.OpportunityID] = executor.Approval{} // a group approval, like the dashboard's
	}
	_, _ = fmt.Fprintln(out)
	return approved, true, nil
}

// ensureFreshScan always scans first: reclaim cleans from what is on disk now,
// never from a stored scan. Only the scan view reuses a stored scan.
func ensureFreshScan(cmd *cobra.Command, home string) error {
	errOut := cmd.ErrOrStderr()
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), " Scanning your Mac first…")
	Background(scanner.SetBackgroundPriority, errOut)(engine.StepScan)
	expected := ExpectedScanBytes(home)
	_, err := engine.Scan(context.Background(), engine.ScanOptions{
		Home: home, Version: version, Workers: 4, Runner: Runner, Root: ScanRoot,
		Progress: func(p scanner.Progress) {
			if IsTerminal(errOut) {
				_, _ = fmt.Fprintf(errOut, "\r\033[K%s", ScanStatusLine(p, expected, p.CurrentDir))
			}
		},
	})
	if IsTerminal(errOut) {
		_, _ = fmt.Fprint(errOut, "\r\033[K")
	}
	return err
}

// TierChoice is one row of the chooser: a risk type, how many items it holds,
// and what picking it means.
type TierChoice struct {
	Risk  model.Risk
	Items []model.Opportunity
	Note  string
}

// cleanChosen shows every risk type with its items, asks which to clean, and
// on Enter cleans those. SAFE is the default. CAUTION is included only when
// named; RISKY only when named, and then every RISKY item is confirmed on its
// own and the plan's code is typed, exactly as in apply. Advice-only items have
// no action, so they are shown but can never be chosen.
func cleanChosen(cmd *cobra.Command, g *GlobalFlags, o cleanOpts) error {
	out := cmd.OutOrStdout()
	home, err := UserHome()
	if err != nil {
		return err
	}
	if err := ensureFreshScan(cmd, home); err != nil {
		return err
	}
	res, cat, err := loadOpportunities(context.Background(), minBytesFor(cmd))
	if err != nil {
		return err
	}
	res = forReclaim(res)
	res.Opportunities = forToolchain(res.Opportunities, o.toolchain)
	rows := TierChoices(res.Opportunities)
	t := themeFor(out)
	if cleanableCount(rows) == 0 {
		_, _ = fmt.Fprintln(out, t.done("Nothing here macdoc can clean."))
		return nil
	}
	if err := renderWinsList(out, res, cat, o.all, "", ""); err != nil {
		return err
	}
	tiers, canceled, err := chooseTiers(cmd, t, rows)
	if err != nil {
		return err
	}
	if canceled || len(tiers) == 0 {
		_, _ = fmt.Fprintln(out, " "+t.done("Canceled. Nothing has been changed."))
		return nil
	}
	pick, risky := chosenItems(rows, tiers)
	if len(pick) == 0 {
		_, _ = fmt.Fprintln(out, " "+t.done("Nothing in what you chose. Nothing has been changed."))
		return nil
	}
	_, _ = fmt.Fprintf(out, "\n Cleaning %s: %d %s (%s).\n", joinRisks(tiers), len(pick), plural(len(pick), "item", "items"), sizeSummary(pick))
	return sealAndClean(cmd, g, pick, risky)
}

// installedAppRecipes are the recipes that remove an app that is still
// installed. reclaim leaves them to `retire`, the way Mole's clean leaves them
// to its uninstall: reclaim cleans caches, junk and what removed apps left
// behind, and never an app you still have.
var installedAppRecipes = map[string]bool{"apps.user_bundle": true, "apps.system_bundle": true}

// forReclaim drops the installed-app items from a result and recomputes its
// tier totals to match, so the numbers shown never include them.
func forReclaim(res engine.OpportunityResult) engine.OpportunityResult {
	res.Opportunities = withoutInstalledApps(res.Opportunities)
	res.Totals = map[model.Risk]int64{}
	for _, op := range res.Opportunities {
		if op.Bytes.Source != model.SourceUnknown {
			res.Totals[op.Risk] += op.Bytes.Value
		}
	}
	return res
}

// withoutInstalledApps returns ops without the items that remove an installed app.
func withoutInstalledApps(ops []model.Opportunity) []model.Opportunity {
	out := make([]model.Opportunity, 0, len(ops))
	for _, op := range ops {
		if !installedAppRecipes[op.RecipeID] {
			out = append(out, op)
		}
	}
	return out
}

// forToolchain keeps the items of one toolchain, or all of them for "".
func forToolchain(ops []model.Opportunity, toolchain string) []model.Opportunity {
	if toolchain == "" {
		return ops
	}
	var keep []model.Opportunity
	for _, op := range ops {
		if op.Toolchain == toolchain {
			keep = append(keep, op)
		}
	}
	return keep
}

// chosenItems returns the items of the chosen risk types, and whether any of
// them is RISKY (which needs apply's full confirmation).
func chosenItems(rows []TierChoice, tiers []model.Risk) (pick []model.Opportunity, risky bool) {
	for _, r := range rows {
		if !slices.Contains(tiers, r.Risk) {
			continue
		}
		pick = append(pick, r.Items...)
		if r.Risk == model.RiskRisky && len(r.Items) > 0 {
			risky = true
		}
		for _, op := range r.Items {
			if op.Method == model.MethodSystem {
				risky = true // administrator removals keep apply's per-item y/N and typed code
			}
		}
	}
	return pick, risky
}

// sealAndClean seals a plan from exactly the items shown and cleans it. A plan
// with RISKY items keeps apply's full confirmation: the plan is shown, each
// RISKY item asks y/N, and the plan's code is typed. Otherwise the one Enter
// the person just gave is the confirmation.
func sealAndClean(cmd *cobra.Command, g *GlobalFlags, pick []model.Opportunity, risky bool) error {
	out := cmd.OutOrStdout()
	ids := make([]string, len(pick))
	for i, op := range pick {
		ids[i] = op.ID
	}
	plan, home, err := sealPlan(g, planner.Selector{IDs: ids})
	if err != nil {
		return finishPlan(out, plan, err)
	}
	var b strings.Builder
	writeRefused(&b, themeFor(out), plan.Refused)
	_, _ = io.WriteString(out, b.String())
	confirm, showPlan := confirmEnteredList, false
	if risky {
		confirm, showPlan = typedConfirm, true
	}
	return runApply(cmd, ApplyDeps{Home: home, Now: time.Now}, plan.Plan.ID, confirm, showPlan)
}

// TierChoices groups the actionable items by risk type (SAFE, CAUTION, RISKY)
// in a fixed order. Advice-only items have no row: macdoc can't remove them,
// and the list above shows them under "Do it yourself".
func TierChoices(ops []model.Opportunity) []TierChoice {
	rows := []TierChoice{
		{Risk: model.RiskSafe, Note: "caches and files the tools rebuild"},
		{Risk: model.RiskCaution, Note: "worth a look first; apps should be closed"},
		{Risk: model.RiskRisky, Note: "may include app data; each one is confirmed on its own"},
	}
	for _, op := range ops {
		for i := range rows {
			if op.Method != model.MethodAdvise && op.Risk == rows[i].Risk {
				rows[i].Items = append(rows[i].Items, op)
			}
		}
	}
	return withoutEmptyRisky(rows)
}

// cleanableCount is how many items the rows can clean.
func cleanableCount(rows []TierChoice) int {
	n := 0
	for _, r := range rows {
		if r.Risk != model.RiskAdvise {
			n += len(r.Items)
		}
	}
	return n
}

// withoutEmptyRisky drops the RISKY row when nothing is in it: reclaim leaves
// installed apps to retire, so most Macs have no RISKY item to offer.
func withoutEmptyRisky(rows []TierChoice) []TierChoice {
	out := rows[:0:0]
	for _, r := range rows {
		if r.Risk == model.RiskRisky && len(r.Items) == 0 {
			continue
		}
		out = append(out, r)
	}
	return out
}

// writeChooser prints the "what do you want to clean?" section and its prompt.
func writeChooser(w io.Writer, t *Theme, rows []TierChoice) {
	var b strings.Builder
	b.WriteString("\n " + t.heading("What do you want to clean?") + "\n")
	for _, r := range rows {
		mark := "  "
		if r.Risk == model.RiskSafe {
			mark = "▸ " // the default
		}
		count := fmt.Sprintf("%d %s", len(r.Items), plural(len(r.Items), "item", "items"))
		label := string(r.Risk)
		if r.Risk == model.RiskAdvise {
			label = "ADVICE" // as the dashboard calls it
		}
		fmt.Fprintf(&b, "  %s%s %s %s  %s\n", mark, t.riskStyle(r.Risk).Render(fmt.Sprintf("%-8s", label)),
			t.text.Render(fmt.Sprintf("%-9s", count)), t.size.Render(fmt.Sprintf("%-18s", sizeSummary(r.Items))), t.muted.Render(r.Note))
	}
	b.WriteString("\n Type the risk types to clean (safe, caution, risky; or all), then Enter. Enter alone cleans SAFE only; n cancels: ")
	_, _ = io.WriteString(w, b.String())
}

// parseTierChoice turns the typed answer into the risk types to clean. Empty
// means SAFE only; "all" means SAFE, CAUTION and RISKY (never advice-only
// items, which can't be removed). An answer it doesn't understand cancels.
func parseTierChoice(line string) (tiers []model.Risk, notes []string, canceled bool) {
	line = strings.ToLower(strings.TrimSpace(line))
	if line == "" {
		return []model.Risk{model.RiskSafe}, nil, false
	}
	add := func(r model.Risk) {
		if !slices.Contains(tiers, r) {
			tiers = append(tiers, r)
		}
	}
	for _, w := range strings.FieldsFunc(line, func(r rune) bool { return r == ' ' || r == ',' || r == '+' }) {
		switch w {
		case "s", "safe", "1":
			add(model.RiskSafe)
		case "c", "caution", "2":
			add(model.RiskCaution)
		case "r", "risky", "3":
			add(model.RiskRisky)
		case "a", "all":
			add(model.RiskSafe)
			add(model.RiskCaution)
			add(model.RiskRisky)
		case "advice", "advise", "4":
			notes = append(notes, "Advice-only items can't be removed by macdoc, so they are skipped.")
		case "n", "no", "q", "quit", "cancel":
			return nil, nil, true
		default:
			return nil, []string{fmt.Sprintf("I didn't understand %q.", w)}, true
		}
	}
	order := []model.Risk{model.RiskSafe, model.RiskCaution, model.RiskRisky}
	var sorted []model.Risk
	for _, r := range order {
		if slices.Contains(tiers, r) {
			sorted = append(sorted, r)
		}
	}
	return sorted, notes, false
}

// joinRisks is "SAFE", "SAFE and CAUTION" or "SAFE, CAUTION and RISKY".
func joinRisks(rs []model.Risk) string {
	s := make([]string, len(rs))
	for i, r := range rs {
		s[i] = string(r)
	}
	if len(s) <= 1 {
		return strings.Join(s, "")
	}
	return strings.Join(s[:len(s)-1], ", ") + " and " + s[len(s)-1]
}

// chooseTiers asks which risk types to clean. At a real terminal it shows the
// checkbox list; otherwise (a faked terminal in tests) it falls back to the
// typed prompt, which accepts the same choices.
func chooseTiers(cmd *cobra.Command, t *Theme, rows []TierChoice) (tiers []model.Risk, canceled bool, err error) {
	if in, ok := cmd.InOrStdin().(*os.File); ok {
		if out, ok := cmd.OutOrStdout().(*os.File); ok {
			return pickTiers(in, out, t, rows)
		}
	}
	out := cmd.OutOrStdout()
	writeChooser(out, t, rows)
	line, err := readLine(bufio.NewReader(cmd.InOrStdin()))
	if err != nil {
		return nil, false, errors.New("no answer; nothing has been changed")
	}
	tiers, notes, canceled := parseTierChoice(line)
	for _, n := range notes {
		_, _ = fmt.Fprintln(out, " "+t.muted.Render(n))
	}
	return tiers, canceled, nil
}
