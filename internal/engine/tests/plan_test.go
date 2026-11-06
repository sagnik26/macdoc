package tests

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sagnik26/macdoc/internal/engine"

	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/planner"
	"github.com/sagnik26/macdoc/internal/platform"
	"github.com/sagnik26/macdoc/internal/recipes"
)

func planFixture(t *testing.T) (fixture, engine.PlanOptions) {
	t.Helper()
	stubTools(t)
	f := newFixture(t, time.Hour)
	cache := f.p("Library/Caches/com.example")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	old := f.now.Add(-400 * 24 * time.Hour)
	setFindings(t, map[string][]recipes.Finding{
		"t.native": {{Bytes: model.Bytes{Value: 300_000_000, Source: model.SourceTool}, LastUsed: old}},
		"t.cache":  {{Target: cache, Bytes: model.Bytes{Value: 50_000_000, Source: model.SourceMeasured}, LastUsed: old}},
		"t.advice": {{Bytes: model.Bytes{Value: 9_000_000_000, Source: model.SourceMeasured}, LastUsed: old}},
	})
	o := f.opts(rec("t.native", model.RiskSafe, model.MethodNative), rec("t.cache", model.RiskCaution, model.MethodQuarantine),
		rec("t.advice", model.RiskAdvise, model.MethodAdvise))
	o.Runner = &platform.FakeRunner{Responses: map[string]platform.Result{
		"tmutil listlocalsnapshots /": {Stdout: []byte("Snapshots for disk /:\ncom.apple.TimeMachine.2026-10-27-101500.local\n")},
	}}
	return f, engine.PlanOptions{OpportunityOptions: o}
}

func TestCreateAndShowPlan(t *testing.T) {
	f, o := planFixture(t)
	o.Selector = planner.Selector{Tier: model.RiskSafe}
	res, err := engine.CreatePlan(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	p := res.Plan
	if !planner.ValidID(p.ID) || res.Status != planner.StatusPending || len(p.Items) != 1 || p.Items[0].RecipeID != "t.native" ||
		p.ScanID != f.scanID || p.Request != "--safe" {
		t.Fatalf("plan = %+v", res)
	}
	if _, err := os.Stat(filepath.Join(planner.Dir(f.storePath), p.ID+".json")); err != nil {
		t.Errorf("plan file: %v", err)
	}

	show := func(at time.Time) engine.PlanResult {
		t.Helper()
		oo := o.OpportunityOptions
		oo.Now = at
		r, err := engine.ShowPlan(context.Background(), oo, p.ID)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	if r := show(f.now.Add(time.Minute)); r.Status != planner.StatusPending || r.Plan.SHA256 != p.SHA256 {
		t.Errorf("show = %s", r.Status)
	}
	if r := show(f.now.Add(31 * time.Minute)); r.Status != planner.StatusExpired {
		t.Errorf("after 31 min: %s", r.Status)
	}
	path := filepath.Join(planner.Dir(f.storePath), p.ID+".json")
	b, _ := os.ReadFile(path)
	if err := os.WriteFile(path, append(b[:len(b)-2], []byte(", \"x\": 1}\n")...), 0o600); err != nil { //nolint:gosec // G703: test fixture in a temp dir
		t.Fatal(err)
	}
	if r := show(f.now.Add(time.Minute)); r.Status != planner.StatusInvalid || len(r.Warnings) == 0 {
		t.Errorf("edited: %s %v", r.Status, r.Warnings)
	}
	if _, err := engine.ShowPlan(context.Background(), o.OpportunityOptions, "P-ZZZZ"); !errors.Is(err, planner.ErrUnknown) {
		t.Errorf("unknown plan: %v", err)
	}

	recs, err := engine.ListPlans(o.OpportunityOptions, 10)
	if err != nil || len(recs) != 1 || recs[0].ID != p.ID {
		t.Errorf("list = %+v, %v", recs, err)
	}
}

func TestCreatePlanRefusals(t *testing.T) {
	_, o := planFixture(t)
	ops, err := engine.Opportunities(context.Background(), o.OpportunityOptions)
	if err != nil {
		t.Fatal(err)
	}
	var adviceID, cacheID string
	for _, op := range ops.Opportunities {
		switch op.RecipeID {
		case "t.advice":
			adviceID = op.ID
		case "t.cache":
			cacheID = op.ID
		}
	}
	o.Selector = planner.Selector{IDs: []string{adviceID}}
	o.Precomputed = &ops
	res, err := engine.CreatePlan(context.Background(), o)
	if !errors.Is(err, planner.ErrNothing) || len(res.Refused) != 1 || res.Plan.ID != "" {
		t.Errorf("advice only: %v %+v", err, res.Refused)
	}

	o.Selector = planner.Selector{IDs: []string{cacheID, adviceID}}
	res, err = engine.CreatePlan(context.Background(), o)
	if err != nil || len(res.Plan.Items) != 1 || len(res.Refused) != 1 || res.Snapshot.Count != 1 {
		t.Errorf("mixed: %v %+v", err, res)
	}
	if fp := res.Plan.Items[0].Fingerprint; fp.Ino == 0 {
		t.Errorf("fingerprint = %+v", fp)
	}
}

func TestCreatePlanNeedsScan(t *testing.T) {
	o := engine.PlanOptions{OpportunityOptions: engine.OpportunityOptions{Home: t.TempDir(), Catalog: &recipes.Catalog{}}, Selector: planner.Selector{Tier: model.RiskSafe}}
	if _, err := engine.CreatePlan(context.Background(), o); !errors.Is(err, engine.ErrNoScan) {
		t.Errorf("err = %v", err)
	}
}
