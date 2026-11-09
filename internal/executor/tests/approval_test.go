package tests

import (
	"context"
	"testing"

	"github.com/sagnik26/macdoc/internal/executor"

	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/planner"
)

// planWithOrigin seals a plan with the given origin (env.plan uses the CLI).
func (e *env) planWithOrigin(t *testing.T, origin model.Origin, items ...model.PlanItem) model.Plan {
	t.Helper()
	p := model.Plan{SchemaVersion: model.SchemaVersion, CreatedAt: t0, ExpiresAt: t0.Add(planner.TTL),
		Origin: origin, ScanID: "S-1", Request: "test", Items: items}
	for _, it := range items {
		p.TotalBytes += it.ExpectedBytes
	}
	if err := planner.Save(e.st, planner.Dir(e.storePath), &p); err != nil {
		t.Fatal(err)
	}
	return p
}

// A Claude plan's item approved only as part of a group doesn't run; the
// same item confirmed on its own does.
func TestClaudeItemNeedsOwnConfirmation(t *testing.T) {
	e := newEnv(t)
	target := e.folder(t, "Library/Logs/A", "a.log")
	p := e.planWithOrigin(t, model.OriginClaude, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, target))
	group := executor.Approvals{"O-aaaaaa": {Individually: false}}
	rep, err := executor.Apply(context.Background(), e.cfg, p, group)
	if err != nil {
		t.Fatal(err)
	}
	if it := rep.Items[0]; it.Outcome != executor.OutcomeSkipped || !exists(target) {
		t.Fatalf("Claude item approved as a group: outcome %s (%q), target kept %v; want skipped", it.Outcome, it.Reason, exists(target))
	}
	if rep2 := mustApply(t, e, e.planWithOrigin(t, model.OriginClaude, e.item(t, "O-bbbbbb", "user.logs", model.RiskSafe, target))); rep2.Items[0].Outcome != executor.OutcomeDone {
		t.Fatalf("Claude item confirmed on its own: outcome %s (%q), want done", rep2.Items[0].Outcome, rep2.Items[0].Reason)
	}
}

// A RISKY item approved as part of a group doesn't run.
func TestRiskyItemNeedsOwnConfirmation(t *testing.T) {
	e := newEnv(t)
	target := e.folder(t, "Library/Logs/A", "a.log")
	p := e.planWithOrigin(t, model.OriginCLI, e.item(t, "O-aaaaaa", "user.logs", model.RiskRisky, target))
	rep, err := executor.Apply(context.Background(), e.cfg, p, executor.Approvals{"O-aaaaaa": {Individually: false}})
	if err != nil {
		t.Fatal(err)
	}
	if it := rep.Items[0]; it.Outcome != executor.OutcomeSkipped || !exists(target) {
		t.Fatalf("RISKY item approved as a group: outcome %s (%q), target kept %v; want skipped", it.Outcome, it.Reason, exists(target))
	}
}
