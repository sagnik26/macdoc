package cli

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"macdoc/internal/engine"
	"macdoc/internal/model"
	"macdoc/internal/recipes"
	"macdoc/internal/scanner"
)

// overviewTopLevel is how many top-level folders the overview lists.
const overviewTopLevel = 5

// overviewPerTier is how many recipe groups each reclaimable tier names.
const overviewPerTier = 3

// runOverview is `macdoc` / `macdoc doctor`.
func runOverview(cmd *cobra.Command, g *globalFlags) error {
	home, err := userHome()
	if err != nil {
		return err
	}
	cat, err := loadCatalog()
	if err != nil {
		return err
	}
	errOut := cmd.ErrOrStderr()
	tty := !g.json && !g.quiet && isTerminal(errOut)
	status := func(s string) {
		if tty {
			_, _ = fmt.Fprintf(errOut, "\r\033[K%s", s)
		}
	}

	opts := engine.OverviewOptions{Home: home, Runner: runner, Version: version, Rescan: g.rescan, Catalog: cat}
	if tty {
		opts.Progress = func(p scanner.Progress) {
			status(fmt.Sprintf("Scanning… %s · %d files · %s · %s",
				engine.DecimalBytes(p.Bytes), p.Files, p.Elapsed.Round(time.Second), truncate(p.CurrentDir, 50)))
		}
		if !g.rescan {
			status("Checking your Mac…")
		} else if err := scanner.SetBackgroundPriority(); err != nil {
			status("")
		}
	}
	res, err := engine.Overview(context.Background(), opts)
	status("")
	if err != nil {
		return err
	}
	if g.json {
		return writeJSONWarn(cmd.OutOrStdout(), "doctor", res, res.Warnings)
	}
	return renderOverview(cmd.OutOrStdout(), res, cat)
}

func renderOverview(w io.Writer, res engine.OverviewResult, cat *recipes.Catalog) error {
	var b strings.Builder

	scanNote := " · scan " + res.ScanAge
	if res.Scanned {
		scanNote = " · scanned just now"
	}
	writeVitals(&b, res.Health, scanNote)
	b.WriteString("\n")
	writeFindings(&b, res.Health.Findings, 3, false)

	// Where the space is.
	measured := res.Scan.TotalBytes
	fmt.Fprintf(&b, "\nWhere your space is (Data volume, %s measured)\n  ", engine.DecimalBytes(measured))
	var parts []string
	for i, n := range res.TopLevel {
		if i == overviewTopLevel {
			break
		}
		parts = append(parts, fmt.Sprintf("%s %s", n.Path, engine.DecimalBytes(n.Bytes)))
	}
	if du := res.DataVolume; du.Source != model.SourceUnknown && du.Value > measured+du.Value/50 {
		parts = append(parts, engine.DecimalBytes(du.Value-measured)+" not visible (needs Full Disk Access)")
	}
	if len(parts) == 0 {
		parts = append(parts, "—")
	}
	b.WriteString(strings.Join(parts, " · "))
	b.WriteString("\n")

	// Reclaimable.
	ops := res.Opportunities.Opportunities
	var actionable, advice int64
	for _, op := range ops {
		if op.Bytes.Source == model.SourceUnknown {
			continue
		}
		if op.Risk == model.RiskAdvise {
			advice += op.Bytes.Value
		} else {
			actionable += op.Bytes.Value
		}
	}
	fmt.Fprintf(&b, "\nReclaimable%s%s actionable · %s advice\n",
		strings.Repeat(" ", 30), engine.DecimalBytes(actionable), engine.DecimalBytes(advice))
	if len(ops) == 0 {
		b.WriteString("  Nothing to reclaim was found in the latest scan.\n")
	}
	for _, tier := range engine.TierOrder {
		tierOps := opsInTier(ops, tier)
		if len(tierOps) == 0 {
			continue
		}
		fmt.Fprintf(&b, "  %-18s %9s   %s\n", shortTierName(tier),
			tierTotal(res.Opportunities.Totals[tier], tierOps), tierSummary(tierOps, cat))
	}

	if res.ScanStale {
		fmt.Fprintf(&b, "\nNote: the scan is %s old. Run `macdoc doctor --scan` to refresh.\n", strings.TrimSuffix(res.ScanAge, " ago"))
	}
	if n := len(res.Warnings); n > 0 {
		fmt.Fprintf(&b, "\n%d note(s) about skipped items: macdoc doctor wins\n", n)
	}
	b.WriteString("\nMore: macdoc doctor wins (every item + ID) · explain <id> · space · health · --scan\n")
	b.WriteString("Nothing has been changed.\n")
	_, err := io.WriteString(w, b.String())
	return err
}

func opsInTier(ops []model.Opportunity, r model.Risk) []model.Opportunity {
	var out []model.Opportunity
	for _, op := range ops {
		if op.Risk == r {
			out = append(out, op)
		}
	}
	return out
}

func shortTierName(r model.Risk) string {
	switch r {
	case model.RiskSafe:
		return "Free & safe"
	case model.RiskCaution:
		return "Worth considering"
	case model.RiskRisky:
		return "Needs care"
	default:
		return "Your call"
	}
}

// tierSummary names the biggest recipe groups in a tier, e.g.
// "uv cache 394 MB · Homebrew old versions 412 MB · 4 × Old app logs 20 MB".
func tierSummary(ops []model.Opportunity, cat *recipes.Catalog) string {
	type group struct {
		title string
		n     int
		bytes int64
		known bool
	}
	byRecipe := map[string]*group{}
	var order []string
	for _, op := range ops {
		g, ok := byRecipe[op.RecipeID]
		if !ok {
			g = &group{title: op.Title}
			byRecipe[op.RecipeID] = g
			order = append(order, op.RecipeID)
		}
		g.n++
		if op.Bytes.Source != model.SourceUnknown {
			g.bytes += op.Bytes.Value
			g.known = true
		}
	}
	groups := make([]*group, 0, len(order))
	for _, id := range order {
		g := byRecipe[id]
		if g.n > 1 {
			g.title = fmt.Sprintf("%d × %s", g.n, recipeTitle(cat, id))
		}
		groups = append(groups, g)
	}
	sort.SliceStable(groups, func(i, j int) bool { return groups[i].bytes > groups[j].bytes })
	var parts []string
	for i, g := range groups {
		if i == overviewPerTier {
			parts = append(parts, fmt.Sprintf("+%d more", len(groups)-overviewPerTier))
			break
		}
		size := "size unknown"
		if g.known {
			size = engine.DecimalBytes(g.bytes)
		}
		parts = append(parts, fmt.Sprintf("%s %s", truncate(g.title, 40), size))
	}
	return strings.Join(parts, " · ")
}
