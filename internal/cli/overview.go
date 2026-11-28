package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sagnik26/macdoc/internal/engine"
	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/recipes"
	"github.com/sagnik26/macdoc/internal/scanner"
)

// overviewTopLevel is how many top-level folders the overview lists.
const overviewTopLevel = 5

// overviewPerTier is how many recipe groups each reclaimable tier names.
const overviewPerTier = 3

// ScanRoot overrides the scan root; tests point it at a fixture tree so
// `macdoc scan` never crawls the real disk. Empty means the Data volume.
var ScanRoot = ""

// overviewMode selects the short overview or the full report.
type overviewMode struct {
	full       bool // every section (macdoc scan)
	rescan     bool // scan first
	foreground bool // scan at normal priority
	workers    int
}

// runOverview runs `macdoc` / `macdoc` (short) and `macdoc scan`
// (rescan + full report).
func runOverview(cmd *cobra.Command, g *GlobalFlags, m overviewMode) error {
	home, err := UserHome()
	if err != nil {
		return err
	}
	cat, err := LoadCatalog()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	errOut := cmd.ErrOrStderr()
	tty := !g.json && !g.quiet && IsTerminal(errOut)
	status := func(s string) {
		if tty {
			_, _ = fmt.Fprintf(errOut, "\r\033[K%s", s)
		}
	}
	opts := engine.OverviewOptions{Home: home, Runner: Runner, Version: version, Rescan: m.rescan, Catalog: cat, Workers: m.workers, ScanRoot: ScanRoot}
	if !m.foreground {
		// A scan may run (always with rescan, or when none exists): keep the Mac
		// responsive then, and only then. Lowering priority for a read that runs
		// no scan made the cached overview four times slower for nothing.
		opts.Step = Background(scanner.SetBackgroundPriority, errOut)
	}
	if tty {
		opts.Progress = func(p scanner.Progress) {
			status(fmt.Sprintf("Scanning… %s · %d files · %s · %s",
				engine.DecimalBytes(p.Bytes), p.Files, p.Elapsed.Round(time.Second), Truncate(p.CurrentDir, 50)))
		}
		if !m.rescan {
			status("Checking your Mac…")
		}
	}
	res, err := engine.Overview(ctx, opts)
	status("")
	if err != nil {
		return err
	}
	if !m.full {
		if g.json {
			return writeJSONWarn(cmd.OutOrStdout(), "overview", res, res.Warnings)
		}
		return RenderOverview(cmd.OutOrStdout(), res, cat)
	}

	// Full report: every section, reusing the opportunities computed above.
	space, err := engine.Storage(ctx, engine.StorageOptions{
		OpportunityOptions: engine.OpportunityOptions{Home: home, Runner: Runner, Catalog: cat},
		Large:              true,
		Precomputed:        &res.Opportunities,
	})
	if err != nil {
		return err
	}
	dev := BuildDevResult(res.Opportunities, "")
	if g.json {
		return writeJSONWarn(cmd.OutOrStdout(), "scan", FullResult{Overview: res, Space: space, Dev: dev}, res.Warnings)
	}
	return RenderFull(cmd.OutOrStdout(), res, space, dev, cat)
}

// FullResult is the `macdoc scan --json` document.
type FullResult struct {
	Overview engine.OverviewResult `json:"overview"`
	Space    engine.StorageResult  `json:"space"`
	Dev      DevResult             `json:"dev"`
}

// RenderFull prints the overview followed by every section in full.
func RenderFull(w io.Writer, res engine.OverviewResult, space engine.StorageResult, dev DevResult, cat *recipes.Catalog) error {
	res, space, dev = display(res), display(space), display(dev) // disk-sourced text, cleaned for the terminal (display)
	sections := []struct {
		title  string
		render func(io.Writer) error
	}{
		{"OVERVIEW", func(w io.Writer) error { return RenderOverview(w, res, cat) }},
		{"SCAN", func(w io.Writer) error {
			if res.ScanSummary == nil {
				_, err := fmt.Fprintf(w, "Using scan %s from %s.\n", res.Scan.ID, res.ScanAge)
				return err
			}
			return RenderScan(w, *res.ScanSummary)
		}},
		{"HEALTH", func(w io.Writer) error { return RenderDoctor(w, res.Health) }},
		{"SPACE", func(w io.Writer) error { return RenderStorage(w, space, true) }},
		{"ALL SUGGESTIONS", func(w io.Writer) error { return RenderWins(w, res.Opportunities, cat, true, "") }},
		{"BY TOOLCHAIN", func(w io.Writer) error { return RenderDev(w, dev, cat) }},
	}
	t := themeFor(w)
	for i, s := range sections {
		if i > 0 {
			head := fmt.Sprintf("\n══════ %s %s\n\n", s.title, strings.Repeat("═", max(0, 70-len(s.title))))
			if t.on {
				head = "\n" + t.brand.Render("━━ "+s.title+" ") + t.faint.Render(strings.Repeat("━", max(0, 70-len(s.title)))) + "\n\n"
			}
			if _, err := io.WriteString(w, head); err != nil {
				return err
			}
		}
		if err := s.render(w); err != nil {
			return err
		}
	}
	return nil
}

// Background returns a step callback that lowers the process to background
// priority when a scan starts, and at no other step. If lowering fails it says
// so on w and carries on.
func Background(lower func() error, w io.Writer) func(engine.OverviewStep) {
	return func(s engine.OverviewStep) {
		if s != engine.StepScan {
			return
		}
		if err := lower(); err != nil {
			_, _ = fmt.Fprintln(w, "macdoc: could not lower priority:", err)
		}
	}
}

// RenderOverview writes the short `macdoc` summary.
func RenderOverview(w io.Writer, res engine.OverviewResult, cat *recipes.Catalog) error {
	res = display(res) // disk-sourced text, cleaned for the terminal (display)
	var b strings.Builder
	t := themeFor(w)

	scanNote := " · scan " + res.ScanAge
	if res.Scanned {
		scanNote = " · scanned just now"
	}
	writeVitals(&b, t, res.Health, scanNote)
	b.WriteString("\n")
	writeFindings(&b, t, res.Health.Findings, 3, false)

	// Where the space is.
	measured := res.Scan.TotalBytes
	fmt.Fprintf(&b, "\n%s %s\n  ", t.heading("Where your space is"),
		t.muted.Render(fmt.Sprintf("(Data volume, %s measured)", engine.DecimalBytes(measured))))
	var parts []string
	for i, n := range res.TopLevel {
		if i == overviewTopLevel {
			break
		}
		parts = append(parts, fmt.Sprintf("%s %s", t.text.Render(n.Path), t.size.Render(engine.DecimalBytes(n.Bytes))))
	}
	if du := res.DataVolume; du.Source != model.SourceUnknown && du.Value > measured+du.Value/50 {
		parts = append(parts, t.muted.Render(engine.DecimalBytes(du.Value-measured)+" not visible (needs Full Disk Access)"))
	}
	if len(parts) == 0 {
		parts = append(parts, "—")
	}
	b.WriteString(strings.Join(parts, t.faint.Render(" · ")))
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
	fmt.Fprintf(&b, "\n%s%s%s actionable · %s advice\n", t.heading("Reclaimable"),
		strings.Repeat(" ", 30), t.Ok.Bold(t.on).Render(engine.DecimalBytes(actionable)), t.info.Render(engine.DecimalBytes(advice)))
	if len(ops) == 0 {
		b.WriteString("  Nothing to reclaim was found in the latest scan.\n")
	}
	for _, tier := range engine.TierOrder {
		tierOps := opsInTier(ops, tier)
		if len(tierOps) == 0 {
			continue
		}
		fmt.Fprintf(&b, "  %s%s %s   %s\n", t.Dot(tier), t.riskStyle(tier).Render(fmt.Sprintf("%-18s", shortTierName(tier))),
			t.size.Render(fmt.Sprintf("%9s", tierTotal(res.Opportunities.Totals[tier], tierOps))), t.muted.Render(tierSummary(tierOps, cat)))
	}

	if q := res.Quarantine; q != nil {
		line := fmt.Sprintf("%d entr%s · %s held, restorable", q.Entries, plural(q.Entries, "y", "ies"), engine.DecimalBytes(q.HeldBytes))
		fmt.Fprintf(&b, "\n%s %s\n", t.heading("Quarantine"), t.muted.Render(line+" (macdoc quarantine)"))
		if q.ExpiredCount > 0 {
			fmt.Fprintf(&b, "  %s %s\n", t.warn.Render(engine.DecimalBytes(q.ExpiredBytes)+" ready to purge:"), t.Command([]string{"macdoc", "quarantine", "purge", "--expired"}))
		}
	}
	if res.ScanStale {
		fmt.Fprintf(&b, "\n%s the scan is %s old. Run `macdoc scan` to refresh.\n", t.warn.Render("Note:"), strings.TrimSuffix(res.ScanAge, " ago"))
	}
	if n := len(res.Warnings); n > 0 {
		fmt.Fprintln(&b, t.muted.Render(fmt.Sprintf("\n%d note(s) about skipped items: macdoc reclaim", n)))
	}
	if sw := res.Software; sw != nil {
		line := "\nSoftware: " + sw.Headline
		if len(sw.NewNames) > 0 {
			line += fmt.Sprintf(" · %d new since the last scan", len(sw.NewNames))
		}
		b.WriteString(line + " (macdoc retire)\n")
	}
	switch {
	case t.on:
		b.WriteString("\n" + t.faint.Render("More: ") + t.accent.Render("macdoc scan") + t.muted.Render(" (rescan + full report) · ") +
			t.accent.Render("reclaim") + t.muted.Render(" (every item + ID) · ") +
			t.accent.Render("reclaim --categories") + t.muted.Render(" · ") +
			t.accent.Render("vitals") + "\n")
	default:
		b.WriteString("\nMore: macdoc scan (rescan + full report) · reclaim (every item + ID) · reclaim --categories · vitals\n")
	}
	b.WriteString(t.done("Nothing has been changed.") + "\n")
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
		Bytes int64
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
			g.Bytes += op.Bytes.Value
			g.known = true
		}
	}
	groups := make([]*group, 0, len(order))
	for _, id := range order {
		g := byRecipe[id]
		if g.n > 1 {
			g.title = fmt.Sprintf("%d × %s", g.n, RecipeTitle(cat, id))
		}
		groups = append(groups, g)
	}
	sort.SliceStable(groups, func(i, j int) bool { return groups[i].Bytes > groups[j].Bytes })
	var parts []string
	for i, g := range groups {
		if i == overviewPerTier {
			parts = append(parts, fmt.Sprintf("+%d more", len(groups)-overviewPerTier))
			break
		}
		size := "size unknown"
		if g.known {
			size = engine.DecimalBytes(g.Bytes)
		}
		parts = append(parts, fmt.Sprintf("%s %s", Truncate(g.title, 40), size))
	}
	return strings.Join(parts, " · ")
}
