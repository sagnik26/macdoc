package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sagnik26/macdoc/internal/engine"
	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/recipes"
)

// UserHome and LoadCatalog are variables so tests can use a fixture home and
// test-only recipes.
var (
	UserHome    = os.UserHomeDir
	LoadCatalog = recipes.Load
)

// groupThreshold is how many opportunities of one recipe wins lists before
// folding them into one line (without --all).
const groupThreshold = 3

// tierNames are the wins section headings.
var tierNames = map[model.Risk]string{
	model.RiskSafe:    "Free & safe",
	model.RiskCaution: "Worth considering",
	model.RiskRisky:   "Needs care",
	model.RiskAdvise:  "Do it yourself (macdoc can't clean these)",
}

// smallBytes is the smallest item --small shows: any item with at least one
// byte, so empty folders stay out.
const smallBytes = 1

// minBytesFor is the size floor for a command: the engine's default (1 MB),
// or one byte when --small was given.
func minBytesFor(cmd *cobra.Command) int64 {
	if small, _ := cmd.Flags().GetBool("small"); small {
		return smallBytes
	}
	return 0
}

// loadOpportunities runs the engine with the real home, catalog and Runner.
// minBytes is the size floor (0 for the default of 1 MB).
func loadOpportunities(ctx context.Context, minBytes int64) (engine.OpportunityResult, *recipes.Catalog, error) {
	home, err := UserHome()
	if err != nil {
		return engine.OpportunityResult{}, nil, err
	}
	cat, err := LoadCatalog()
	if err != nil {
		return engine.OpportunityResult{}, nil, err
	}
	res, err := engine.Opportunities(ctx, engine.OpportunityOptions{Home: home, Runner: Runner, Catalog: cat, MinBytes: minBytes})
	return res, cat, err
}

func newWinsCmd(g *GlobalFlags) *cobra.Command {
	var all bool
	var tier string
	cmd := &cobra.Command{
		Use:   "wins",
		Short: "Top space wins from the latest scan, by risk tier (read-only)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			risk, err := ParseTier(tier)
			if err != nil {
				return err
			}
			res, cat, err := loadOpportunities(context.Background(), minBytesFor(cmd))
			if err != nil {
				return err
			}
			res = forReclaim(res) // installed apps are for retire
			if risk != "" {
				res = filterTier(res, risk)
			}
			if g.json {
				return writeJSONWarn(cmd.OutOrStdout(), "reclaim", res, res.Warnings)
			}
			return RenderWins(cmd.OutOrStdout(), res, cat, all, risk)
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "list every item instead of grouping repeated ones")
	cmd.Flags().Bool("small", false, "also list items that would free under 1 MB")
	cmd.Flags().StringVar(&tier, "tier", "", "only one tier: safe, caution, risky or advise")
	return cmd
}

// ParseTier turns a tier name (any case) into a risk class; an empty name
// means no filter and returns an empty risk.
func ParseTier(s string) (model.Risk, error) {
	if s == "" {
		return "", nil
	}
	r := model.Risk(strings.ToUpper(s))
	if _, ok := tierNames[r]; !ok {
		return "", fmt.Errorf("unknown tier %q (use safe, caution, risky or advise)", s)
	}
	return r, nil
}

// filterTier keeps only one tier's opportunities and total.
func filterTier(res engine.OpportunityResult, r model.Risk) engine.OpportunityResult {
	ops := []model.Opportunity{}
	for _, op := range res.Opportunities {
		if op.Risk == r {
			ops = append(ops, op)
		}
	}
	res.Opportunities = ops
	res.Totals = map[model.Risk]int64{r: res.Totals[r]}
	return res
}

// RenderWins writes the suggestions in the human format; all lists repeated
// items individually and only restricts the output to one tier.
func RenderWins(w io.Writer, res engine.OpportunityResult, cat *recipes.Catalog, all bool, only model.Risk) error {
	return renderWinsList(w, res, cat, all, only, "macdoc apply <id…> (shows the plan, then asks you to confirm)")
}

// renderWinsList is RenderWins with the footer as a parameter ("" for none),
// so the clean-on-Enter flow can put its own prompt there.
func renderWinsList(w io.Writer, res engine.OpportunityResult, cat *recipes.Catalog, all bool, only model.Risk, footer string) error {
	res = display(res) // disk-sourced text, cleaned for the terminal (display)
	var b strings.Builder
	t := themeFor(w)
	b.WriteString(t.commandHeader("reclaim", res.Scan.ID, res.ScanAge))
	if len(res.Opportunities) == 0 {
		if only != "" {
			fmt.Fprintf(&b, " Nothing in %q in the latest scan.\n", tierNames[only])
		} else {
			b.WriteString(" Nothing to reclaim was found in the latest scan.\n")
		}
	}
	for _, risk := range engine.TierOrder {
		var ops []model.Opportunity
		for _, op := range res.Opportunities {
			if op.Risk == risk {
				ops = append(ops, op)
			}
		}
		if len(ops) == 0 {
			continue
		}
		b.WriteString(t.section(t.riskStyle(risk), t.Dot(risk)+tierNames[risk], tierTotal(res.Totals[risk], ops)))
		writeOpportunities(&b, t, ops, cat, all)
		b.WriteString("\n")
	}
	writeNotes(&b, t, res.Warnings)
	if footer != "" {
		b.WriteString(t.footer(footer))
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// tierTotal is the known total, flagged when some sizes are unknown. A total
// made only of unknown sizes is "unknown", never "0 B".
func tierTotal(known int64, ops []model.Opportunity) string {
	unknown, allUnknown := false, true
	for _, op := range ops {
		if op.Bytes.Source == model.SourceUnknown {
			unknown = true
		} else {
			allUnknown = false
		}
	}
	switch {
	case allUnknown && len(ops) > 0:
		return "size unknown"
	case unknown:
		return engine.DecimalBytes(known) + " + unknown"
	}
	return engine.DecimalBytes(known)
}

// writeOpportunities lists ops, folding recipes with many items into one
// line unless all is set.
func writeOpportunities(b *strings.Builder, t *Theme, ops []model.Opportunity, cat *recipes.Catalog, all bool) {
	count := map[string]int{}
	sum := map[string]int64{}
	for _, op := range ops {
		count[op.RecipeID]++
		if op.Bytes.Source != model.SourceUnknown {
			sum[op.RecipeID] += op.Bytes.Value
		}
	}
	grouped := map[string]bool{}
	for _, op := range ops {
		if !all && count[op.RecipeID] > groupThreshold {
			if grouped[op.RecipeID] {
				continue
			}
			grouped[op.RecipeID] = true
			title := fmt.Sprintf("%d × %s · %s", count[op.RecipeID], RecipeTitle(cat, op.RecipeID), engine.DecimalBytes(sum[op.RecipeID]))
			fmt.Fprintf(b, "  %-8s  %8s  %s  %s\n", "", "", t.text.Render(fmt.Sprintf("%-44s", title)), t.muted.Render("(macdoc reclaim --all)"))
			continue
		}
		writeOpportunity(b, t, op)
	}
}

// writeOpportunity renders one line, plus the native command below it.
func writeOpportunity(b *strings.Builder, t *Theme, op model.Opportunity) {
	// The restore text is wrapped under its column, never cut off.
	lines := wrapWords(restoreText(op), restoreWidth)
	first := ""
	if len(lines) > 0 {
		first = lines[0]
	}
	fmt.Fprintf(b, "  %s  %s  %s  %s\n", t.ID.Render(fmt.Sprintf("%-8s", op.ID)), t.size.Render(fmt.Sprintf("%8s", sizeCell(op.Bytes))),
		t.text.Render(fmt.Sprintf("%-44s", Truncate(op.Title, 44))), t.muted.Render(first))
	for _, l := range lines[min(1, len(lines)):] {
		fmt.Fprintf(b, "  %-8s  %8s  %-44s  %s\n", "", "", "", t.muted.Render(l))
	}
	if op.Method == model.MethodNative && len(op.Command) > 0 {
		fmt.Fprintf(b, "  %-8s  %8s  %s %s\n", "", "", t.faint.Render("→"), t.Command(op.Command))
	}
	if op.Method == model.MethodAdvise {
		for i, step := range op.Steps {
			fmt.Fprintf(b, "  %-8s  %8s  %s %s\n", "", "", t.faint.Render(fmt.Sprintf("%d.", i+1)), t.muted.Render(Truncate(step, 96)))
		}
	}
}

// restoreWidth is how wide the restore column is before its text wraps.
const restoreWidth = 44

// wrapWords breaks s at spaces into lines of at most width characters. A word
// longer than width gets a line of its own.
func wrapWords(s string, width int) []string {
	var lines []string
	cur := ""
	for _, word := range strings.Fields(s) {
		switch {
		case cur == "":
			cur = word
		case len([]rune(cur))+1+len([]rune(word)) <= width:
			cur += " " + word
		default:
			lines = append(lines, cur)
			cur = word
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}

// RecipeTitle is the recipe's title, or id when the recipe is not in cat.
func RecipeTitle(cat *recipes.Catalog, id string) string {
	if cat != nil {
		if r, ok := cat.Get(id); ok {
			return r.Title
		}
	}
	return id
}

// sizeCell is a short size: "~" marks estimates, "—" unknown.
func sizeCell(v model.Bytes) string {
	switch v.Source {
	case model.SourceUnknown:
		return "—"
	case model.SourceEstimated:
		return "~" + engine.DecimalBytes(v.Value)
	}
	return engine.DecimalBytes(v.Value)
}

// RestoreNote is the short restore cost shown on every line.
func RestoreNote(op model.Opportunity) string {
	return Truncate(restoreText(op), 40)
}

// restoreText is the restore cost in full, for places that can wrap it.
func restoreText(op model.Opportunity) string {
	r := op.Restore
	if op.Method != model.MethodAdvise {
		switch r.Kind {
		case model.RestoreRebuild:
			return "rebuilds on next build"
		case model.RestoreRedownload:
			if r.RedownloadBytes > 0 {
				return "re-download ~" + engine.DecimalBytes(r.RedownloadBytes)
			}
			return "re-downloads on demand"
		}
	}
	if r.Note != "" {
		return r.Note
	}
	if r.Kind == model.RestoreIrreplaceable {
		return "can't be restored"
	}
	return ""
}

func writeNotes(b *strings.Builder, t *Theme, warnings []string) {
	if len(warnings) == 0 {
		return
	}
	b.WriteString(" " + t.heading("Notes") + "\n")
	for _, w := range warnings {
		b.WriteString(t.muted.Render("  · "+w) + "\n")
	}
	b.WriteString("\n")
}
