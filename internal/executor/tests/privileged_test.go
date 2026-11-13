package tests

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/sagnik26/macdoc/internal/audit"
	"github.com/sagnik26/macdoc/internal/executor"
	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/planner"
)

// fakePriv records what the privileged function was asked to run. Nothing here
// ever starts sudo: the real function refuses in the sandbox these tests run in.
type fakePriv struct{ calls [][]string }

func (f *fakePriv) run(_ context.Context, argv []string, _ time.Duration) ([]byte, int, error) {
	f.calls = append(f.calls, argv)
	return nil, 0, nil
}

func privItem(e *env, t *testing.T, recipe string, argv ...string) model.PlanItem {
	t.Helper()
	it := e.native(t, "O-privat", recipe, model.RiskCaution, argv...)
	it.Privileged = true
	return it
}

func planFrom(e *env, t *testing.T, origin model.Origin, items ...model.PlanItem) model.Plan {
	t.Helper()
	p := model.Plan{SchemaVersion: model.SchemaVersion, CreatedAt: t0, ExpiresAt: t0.Add(planner.TTL),
		Origin: origin, ScanID: "S-1", Request: "test", Items: items}
	if err := planner.Save(e.st, planner.Dir(e.storePath), &p); err != nil {
		t.Fatal(err)
	}
	return p
}

// Positive: a person's plan, confirmed item by item, runs the exact command
// through the privileged function only, and the audit record says so.
func TestPrivilegedItemRunsThroughThePrivilegedFunction(t *testing.T) {
	withTools(t, "purge")
	e := newEnv(t)
	f := &fakePriv{}
	e.cfg.Priv = f.run
	p := planFrom(e, t, model.OriginCLI, privItem(e, t, "maintenance.purge_memory", "purge"))
	rep := mustApply(t, e, p)
	if rep.Items[0].Outcome != executor.OutcomeDone || len(f.calls) != 1 || !slices.Equal(f.calls[0], []string{"purge"}) {
		t.Fatalf("outcome %v, privileged calls %v", rep.Items[0].Outcome, f.calls)
	}
	if len(e.exec.calls) != 0 {
		t.Errorf("an ordinary exec ran a privileged item: %v", e.exec.calls)
	}
	var priv bool
	for _, r := range e.events(t) {
		priv = priv || (r.Event == audit.Done && r.Privileged)
	}
	if !priv {
		t.Error("the audit log doesn't record the privileged step")
	}
}

// applyWith applies p and reports whether the item was refused: either the
// whole plan failed to verify, or the item was skipped or failed.
func applyWith(e *env, p model.Plan, ap executor.Approvals) (refused bool) {
	rep, err := executor.Apply(context.Background(), e.cfg, p, ap)
	return err != nil || len(rep.Items) == 0 || rep.Items[0].Outcome != executor.OutcomeDone
}

// A group approval is not enough, a plan Claude proposed never runs a
// privileged item, and a plan item whose privilege flag disagrees with its
// recipe is skipped. Nothing runs in any of these cases.
func TestPrivilegedItemRefusals(t *testing.T) {
	withTools(t, "purge", "brew")
	for name, c := range map[string]struct {
		origin model.Origin
		item   func(e *env) model.PlanItem
		ap     bool // approved individually
	}{
		"group approval": {model.OriginCLI, func(e *env) model.PlanItem { return privItem(e, t, "maintenance.purge_memory", "purge") }, false},
		"claude plan":    {model.OriginClaude, func(e *env) model.PlanItem { return privItem(e, t, "maintenance.purge_memory", "purge") }, true},
		"flag not in recipe": {model.OriginCLI, func(e *env) model.PlanItem {
			return privItem(e, t, "brew.cleanup", "brew", "cleanup", "--prune=all")
		}, true},
		"recipe privileged, item not": {model.OriginCLI, func(e *env) model.PlanItem {
			it := privItem(e, t, "maintenance.purge_memory", "purge")
			it.Privileged = false
			return it
		}, true},
		"argv off the list": {model.OriginCLI, func(e *env) model.PlanItem {
			return privItem(e, t, "maintenance.purge_memory", "purge", "-f")
		}, true},
	} {
		e := newEnv(t)
		f := &fakePriv{}
		e.cfg.Priv = f.run
		it := c.item(e)
		p := planFrom(e, t, c.origin, it)
		refused := applyWith(e, p, executor.Approvals{it.OpportunityID: {Individually: c.ap}})
		if !refused || len(f.calls) != 0 || len(e.exec.calls) != 0 {
			t.Errorf("%s: refused %v, privileged %v, ordinary %v; want refused and nothing run", name, refused, f.calls, e.exec.calls)
		}
	}
}

// With no injected function the real one is used, and in a sandbox (or
// without a terminal) it refuses, so a test or a pipe can never reach sudo.
func TestRealPrivilegedRefusesHere(t *testing.T) {
	withTools(t, "purge")
	e := newEnv(t)
	p := planFrom(e, t, model.OriginCLI, privItem(e, t, "maintenance.purge_memory", "purge"))
	rep := mustApply(t, e, p)
	if rep.Items[0].Outcome != executor.OutcomeFailed {
		t.Errorf("outcome = %v (%s); want failed", rep.Items[0].Outcome, rep.Items[0].Reason)
	}
}
