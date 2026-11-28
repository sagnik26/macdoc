package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/sagnik26/macdoc/internal/engine"
	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/planner"
)

// preconditionText describes a precondition for the dry run.
var preconditionText = map[string]string{
	"docker_reachable":        "Docker is running",
	"emulator_not_running":    "no Android emulator is running",
	"simulator_not_running":   "no iOS simulator is running",
	"gradle_daemons_stopped":  "Gradle daemons are stopped",
	"metro_not_running":       "Metro isn't running",
	"ollama_model_not_loaded": "the model isn't loaded in Ollama",
	"git_clean":               "the project has no uncommitted changes",
	"no_installed_app":        "no installed app owns it",
	"app_bundle_not_running":  "the app isn't running",
}

func preconditionLabel(p string) string {
	if app, ok := strings.CutPrefix(p, "app_not_running:"); ok {
		return app + " isn't running"
	}
	if s, ok := preconditionText[p]; ok {
		return s
	}
	return p
}

// sealPlan seals a plan from the selection, as `macdoc reclaim` does,
// and returns it with the home folder it was made for.
func sealPlan(g *GlobalFlags, sel planner.Selector) (engine.PlanResult, string, error) {
	home, err := UserHome()
	if err != nil {
		return engine.PlanResult{}, "", err
	}
	cat, err := LoadCatalog()
	if err != nil {
		return engine.PlanResult{}, "", err
	}
	if sel.Recipe != "" {
		if _, ok := cat.Get(sel.Recipe); !ok {
			return engine.PlanResult{}, "", fmt.Errorf("unknown recipe %q (see docs/RECIPE_CATALOG.md for the recipe IDs)", sel.Recipe)
		}
	}
	res, err := engine.CreatePlan(context.Background(), engine.PlanOptions{
		OpportunityOptions: engine.OpportunityOptions{Home: home, Runner: Runner, Catalog: cat},
		Selector:           sel,
	})
	return res, home, err
}

// finishPlan explains why nothing was planned, or returns nil to go on.
func finishPlan(w io.Writer, res engine.PlanResult, err error) error {
	switch {
	case errors.Is(err, planner.ErrNothing):
		var b strings.Builder
		writeRefused(&b, themeFor(w), res.Refused)
		_, _ = io.WriteString(w, b.String())
		return errors.New("nothing to clean: none of the selected suggestions can be acted on")
	case err != nil:
		return err
	}
	return nil
}

// RenderPlan writes a sealed dry-run plan in the human format.
func RenderPlan(w io.Writer, res engine.PlanResult, home string, now time.Time) error {
	res = display(res) // disk-sourced text, cleaned for the terminal (display)
	var b strings.Builder
	t := themeFor(w)
	p := res.Plan
	b.WriteString(planHeader(t, res, now))

	acts := planner.Actions(p)
	var freedBytes int64
	unknown := 0
	for i := 0; i < len(acts); {
		r := acts[i].Item.Risk
		j := i
		var sum int64
		for ; j < len(acts) && acts[j].Item.Risk == r; j++ {
			sum += acts[j].Item.ExpectedBytes
		}
		b.WriteString(t.section(t.riskStyle(r), string(r), engine.DecimalBytes(sum)))
		for _, a := range acts[i:j] {
			writeAction(&b, t, a, home)
			if a.Item.BytesSource == model.SourceUnknown {
				unknown++
			} else {
				freedBytes += a.Item.ExpectedBytes
			}
		}
		b.WriteString("\n")
		i = j
	}

	if freedBytes > 0 {
		fmt.Fprintf(&b, " %s %s\n", t.muted.Render("Freed right away:"), t.size.Render(engine.DecimalBytes(freedBytes)))
	}
	if unknown > 0 {
		fmt.Fprintf(&b, " %s\n", t.muted.Render(fmt.Sprintf("Plus %d item(s) of unknown size.", unknown)))
	}
	if res.Snapshot.Count > 0 {
		fmt.Fprintf(&b, " %s\n", t.warn.Render(fmt.Sprintf("%d local Time Machine snapshot(s) exist: macOS may keep some of the freed space until they expire.", res.Snapshot.Count)))
	}
	b.WriteString("\n")
	writeRefused(&b, t, res.Refused)
	writeNotes(&b, t, res.Warnings)
	b.WriteString(" " + t.done("Nothing has been changed.") + "\n")
	_, err := io.WriteString(w, b.String())
	return err
}

// planHeader is the first line: ID, items, size, expiry, origin, status.
func planHeader(t *Theme, res engine.PlanResult, now time.Time) string {
	p := res.Plan
	expiry := "expired"
	if left := p.ExpiresAt.Sub(now); left > 0 {
		expiry = fmt.Sprintf("expires in %d min", int((left+time.Minute-1)/time.Minute))
	}
	origin := "made by you (" + p.Request + ")"
	if p.Origin == model.OriginClaude {
		origin = "proposed by Claude " + ProposedAt(p.CreatedAt)
	}
	line := fmt.Sprintf("Plan %s · %d item(s) · %s · %s · %s", p.ID, len(p.Items), engine.DecimalBytes(p.TotalBytes), expiry, origin)
	var out string
	if t.on {
		out = t.Card(t.brand.Render("◆ ")+t.Title.Render("Plan ")+t.ID.Render(p.ID)+t.muted.Render(strings.TrimPrefix(line, "Plan "+p.ID))) + "\n"
	} else {
		out = line + "\n\n"
	}
	switch res.Status {
	case planner.Expired:
		out += " " + t.warn.Render("This plan has expired. Run macdoc reclaim again.") + "\n\n"
	case planner.Applied:
		out += " " + t.muted.Render("This plan was already applied.") + "\n\n"
	case planner.Invalid:
		out += " " + t.bad.Render("This plan doesn't verify and can't be applied.") + "\n\n"
	}
	return out
}

// writeAction writes one item: what it is, its size, and exactly what apply
// would do. Every action has exactly one "→" line.
func writeAction(b *strings.Builder, t *Theme, a planner.Action, home string) {
	it := a.Item
	size := sizeCell(model.Bytes{Value: it.ExpectedBytes, Source: it.BytesSource})
	fmt.Fprintf(b, "  %s  %s %s\n", t.ID.Render(it.OpportunityID), t.text.Render(padRight(truncateANSI(it.Title, 44), 44)), t.size.Render(fmt.Sprintf("%9s", size)))
	const indent = "            "
	switch a.Kind {
	case planner.Native:
		fmt.Fprintf(b, "%s%s %s\n", indent, t.accent.Render("→ runs"), t.Command(a.Argv))
		fmt.Fprintf(b, "%s%s\n", indent, t.muted.Render("Not undoable by macdoc: "+restoreDetail(it.Restore)))
	case planner.Quarantine:
		fmt.Fprintf(b, "%s%s %s\n", indent, t.accent.Render("→ deletes"), t.text.Render(TildePath(home, a.Target)))
		fmt.Fprintf(b, "%s%s\n", indent, t.muted.Render("Not undoable by macdoc: "+restoreDetail(it.Restore)))
	case planner.System:
		fmt.Fprintf(b, "%s%s %s\n", indent, t.accent.Render("→ deletes"), t.text.Render(a.Target+" (administrator rights)"))
		fmt.Fprintf(b, "%s%s\n", indent, t.muted.Render("Permanent: root-owned data can't be quarantined. "+restoreDetail(it.Restore)))
	case planner.Sweep:
		fmt.Fprintf(b, "%s%s %s\n", indent, t.accent.Render("→ deletes"), t.text.Render(".DS_Store files in "+TildePath(home, a.Target)))
		fmt.Fprintf(b, "%s%s\n", indent, t.muted.Render("No quarantine copy; "+restoreDetail(it.Restore)))
	}
	if it.RiskReason != "" {
		fmt.Fprintf(b, "%s%s\n", indent, t.warn.Render("Risk raised: "+it.RiskReason))
	}
	if it.Privileged {
		runs := ""
		if len(it.Argv) > 0 {
			runs = " Runs: " + strings.Join(it.Argv, " ")
		}
		fmt.Fprintf(b, "%s%s\n", indent, t.warn.Render("Needs administrator rights: macOS asks for your password in Terminal."+runs))
	}
	if len(it.Preconditions) > 0 {
		labels := make([]string, len(it.Preconditions))
		for i, p := range it.Preconditions {
			labels[i] = preconditionLabel(p)
		}
		fmt.Fprintf(b, "%s%s\n", indent, t.muted.Render("Checks first: "+strings.Join(labels, " · ")))
	}
}

func padRight(s string, n int) string {
	if w := len([]rune(s)); w < n {
		return s + strings.Repeat(" ", n-w)
	}
	return s
}

// writeRefused lists what was asked for but left out.
func writeRefused(b *strings.Builder, t *Theme, refused []planner.Refusal) {
	if len(refused) == 0 {
		return
	}
	b.WriteString(" " + t.heading("Not included") + "\n")
	for _, r := range refused {
		name := r.ID
		if r.Title != "" {
			name += " " + r.Title
		}
		fmt.Fprintf(b, "  %s %s\n", t.text.Render(name+":"), t.muted.Render(r.Reason))
	}
	b.WriteString("\n")
}
