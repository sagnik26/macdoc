package tests

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/sagnik26/macdoc/internal/executor"
	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/planner"
)

// A non-interactive run (reclaim --yes) does SAFE and CAUTION quarantine moves
// and nothing else: a RISKY move, a native command, a sweep and a Claude plan
// are all skipped, and every audit record says no person confirmed it.
func TestNonInteractiveRunLimits(t *testing.T) {
	e := newEnv(t)
	e.cfg.NonInteractive = true
	withTools(t, "brew")
	safe := e.folder(t, "Library/Logs/A")
	caution := e.folder(t, "Library/Logs/B")
	risky := e.folder(t, "Library/Logs/C")
	docs := filepath.Join(e.home, "Documents")
	write(t, filepath.Join(docs, ".DS_Store"))
	items := []model.PlanItem{
		e.item(t, "O-yesaaa", "user.logs", model.RiskSafe, safe),
		e.item(t, "O-yesbbb", "user.logs", model.RiskCaution, caution),
		e.item(t, "O-yesccc", "user.logs", model.RiskRisky, risky),
		e.native(t, "O-yesddd", "brew.cleanup", model.RiskSafe, "brew", "cleanup", "--prune=all"),
		e.sweepItem(t, "O-yeseee", docs),
	}
	rep := mustApply(t, e, e.plan(t, items...))
	got := map[string]executor.Outcome{}
	for _, r := range rep.Items {
		got[r.Item.OpportunityID] = r.Outcome
	}
	want := map[string]executor.Outcome{"O-yesaaa": executor.OutcomeDone, "O-yesbbb": executor.OutcomeDone,
		"O-yesccc": executor.OutcomeSkipped, "O-yesddd": executor.OutcomeSkipped, "O-yeseee": executor.OutcomeSkipped}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("%s: %s, want %s", id, got[id], w)
		}
	}
	if !exists(risky) || !exists(filepath.Join(docs, ".DS_Store")) || len(e.exec.calls) != 0 {
		t.Error("a RISKY item, the sweep or a native command ran without a person")
	}
	for _, r := range e.events(t) {
		if r.Event == "item_done" && !r.NonInteractive {
			t.Errorf("item %s isn't marked non-interactive", r.Item)
		}
	}
}

// A Claude plan never runs non-interactively.
func TestNonInteractiveSkipsClaudePlans(t *testing.T) {
	e := newEnv(t)
	e.cfg.NonInteractive = true
	safe := e.folder(t, "Library/Logs/A")
	p := model.Plan{SchemaVersion: model.SchemaVersion, CreatedAt: t0, ExpiresAt: t0.Add(planner.TTL), Origin: model.OriginClaude,
		ScanID: "S-1", Request: "test", Items: []model.PlanItem{e.item(t, "O-yesfff", "user.logs", model.RiskSafe, safe)}}
	if err := planner.Save(e.st, planner.Dir(e.storePath), &p); err != nil {
		t.Fatal(err)
	}
	rep, err := executor.Apply(context.Background(), e.cfg, p, all(p))
	if err != nil || rep.Items[0].Outcome != executor.OutcomeSkipped || !exists(safe) {
		t.Errorf("%v %+v", err, rep.Items)
	}
}
