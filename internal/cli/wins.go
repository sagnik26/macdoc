package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"macdoc/internal/engine"
	"macdoc/internal/model"
	"macdoc/internal/recipes"
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
	model.RiskAdvise:  "Your call (advice only)",
}

// loadOpportunities runs the engine with the real home, catalog and Runner.
func loadOpportunities(ctx context.Context) (engine.OpportunityResult, *recipes.Catalog, error) {
	home, err := UserHome()
	if err != nil {
		return engine.OpportunityResult{}, nil, err
	}
	cat, err := LoadCatalog()
	if err != nil {
		return engine.OpportunityResult{}, nil, err
	}
	res, err := engine.Opportunities(ctx, engine.OpportunityOptions{Home: home, Runner: Runner, Catalog: cat})
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
			res, cat, err := loadOpportunities(context.Background())
			if err != nil {
				return err
			}
			if risk != "" {
				res = filterTier(res, risk)
			}
			if g.json {
				return writeJSONWarn(cmd.OutOrStdout(), "wins", res, res.Warnings)
			}
			return RenderWins(cmd.OutOrStdout(), res, cat, all, risk)
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "list every item instead of grouping repeated ones")
	cmd.Flags().StringVar(&tier, "tier", "", "only one tier: safe, caution, risky or advise")
	return cmd
}

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

func RenderWins(w io.Writer, res engine.OpportunityResult, cat *recipes.Catalog, all bool, only model.Risk) error {
	res = display(res) // disk-sourced text, cleaned for the terminal (display)
	var b strings.Builder
	t := themeFor(w)
	b.WriteString(t.commandHeader("wins", res.Scan.ID, res.ScanAge))
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
	b.WriteString(t.footer("macdoc doctor explain <id> · macdoc plan <id…> (a dry run)"))
	_, err := io.WriteString(w, b.String())
	return err
}

// SectionHeader renders " Name ─────── total" to a fixed width.
func SectionHeader(name, total string) string {
	return PlainTheme().section(lipgloss.Style{}, name, total)
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
func writeOpportunities(b *strings.Builder, t *theme, ops []model.Opportunity, cat *recipes.Catalog, all bool) {
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
			fmt.Fprintf(b, "  %-8s  %8s  %s  %s\n", "", "", t.text.Render(fmt.Sprintf("%-44s", title)), t.muted.Render("(macdoc doctor wins --all)"))
			continue
		}
		writeOpportunity(b, t, op)
	}
}

// writeOpportunity renders one line, plus the native command below it.
func writeOpportunity(b *strings.Builder, t *theme, op model.Opportunity) {
	fmt.Fprintf(b, "  %s  %s  %s  %s\n", t.Id.Render(fmt.Sprintf("%-8s", op.ID)), t.size.Render(fmt.Sprintf("%8s", sizeCell(op.Bytes))),
		t.text.Render(fmt.Sprintf("%-44s", Truncate(op.Title, 44))), t.muted.Render(RestoreNote(op)))
	if op.Method == model.MethodNative && len(op.Command) > 0 {
		fmt.Fprintf(b, "  %-8s  %8s  %s %s\n", "", "", t.faint.Render("→"), t.Command(op.Command))
	}
}

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
		return Truncate(r.Note, 40)
	}
	if r.Kind == model.RestoreIrreplaceable {
		return "can't be restored"
	}
	return ""
}

func writeNotes(b *strings.Builder, t *theme, warnings []string) {
	if len(warnings) == 0 {
		return
	}
	b.WriteString(" " + t.heading("Notes") + "\n")
	for _, w := range warnings {
		b.WriteString(t.muted.Render("  · "+w) + "\n")
	}
	b.WriteString("\n")
}
