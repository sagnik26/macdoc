package engine

import (
	"context"
	"errors"
	"time"

	"macdoc/internal/audit"
	"macdoc/internal/config"
	"macdoc/internal/model"
	"macdoc/internal/planner"
	"macdoc/internal/platform"
	"macdoc/internal/probe"
	"macdoc/internal/recipes"
	"macdoc/internal/store"
)

// PlanOptions configure CreatePlan.
type PlanOptions struct {
	OpportunityOptions
	Selector planner.Selector
	Origin   model.Origin  // default cli
	TTL      time.Duration // default planner.DefaultTTL
	// Precomputed reuses opportunities the caller already has (the
	// dashboard). They're used only if they come from the latest complete
	// scan; otherwise they're recomputed.
	Precomputed *OpportunityResult
}

// PlanResult is the data behind `macdoc plan`.
type PlanResult struct {
	Plan     model.Plan        `json:"plan"`
	Status   planner.Status    `json:"status"`
	Refused  []planner.Refusal `json:"refused"`
	Snapshot SnapshotNote      `json:"snapshots"`
	Warnings []string          `json:"warnings,omitempty"`
}

// SnapshotNote says whether local Time Machine snapshots may hold on to
// freed space. Count is -1 if it couldn't be checked.
type SnapshotNote struct {
	Count int `json:"count"`
}

// CreatePlan builds, seals and saves a dry-run plan from the latest complete
// scan. It writes only the plan file and its store row; nothing on the Mac
// changes. Errors wrap planner.ErrNothing (with the refusals in the result)
// or planner.ErrStaleScan.
func CreatePlan(ctx context.Context, o PlanOptions) (PlanResult, error) {
	if o.Home == "" {
		return PlanResult{}, errors.New("plan: home directory is required")
	}
	if o.StorePath == "" {
		o.StorePath = store.DefaultPath(o.Home)
	}
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
	if o.Catalog == nil {
		cat, err := recipes.Load()
		if err != nil {
			return PlanResult{}, err
		}
		o.Catalog = cat
	}
	pol, cfg, err := config.Policy(o.Home)
	if err != nil {
		return PlanResult{}, err
	}
	if o.TTL <= 0 {
		o.TTL = cfg.PlanTTL()
	}
	st, err := store.Open(o.StorePath)
	if err != nil {
		return PlanResult{}, err
	}
	defer func() { _ = st.Close() }()
	info, err := st.LatestScan(model.ScanComplete)
	if errors.Is(err, store.ErrNoScan) {
		return PlanResult{}, ErrNoScan
	}
	if err != nil {
		return PlanResult{}, err
	}

	ops := o.Precomputed
	if ops == nil || ops.Scan.ID != info.ID {
		res, err := Opportunities(ctx, o.OpportunityOptions)
		if err != nil {
			return PlanResult{}, err
		}
		ops = &res
	}
	plan, refused, err := planner.Build(planner.Input{
		ScanID: info.ID, ScanFinished: scanTime(info), Opportunities: ops.Opportunities,
		Catalog: o.Catalog, Policy: pol, Now: o.Now, TTL: o.TTL, Origin: o.Origin,
	}, o.Selector)
	out := PlanResult{Plan: plan, Refused: refused, Snapshot: snapshotNote(ctx, o.Runner)}
	if out.Refused == nil {
		out.Refused = []planner.Refusal{}
	}
	if err != nil {
		return out, err
	}
	if err := planner.Save(st, planner.Dir(o.StorePath), &out.Plan); err != nil {
		return out, err
	}
	out.Status = planner.StatusPending
	if err := audit.Append(audit.Path(o.StorePath), audit.Record{TS: o.Now, Event: audit.PlanCreated, Plan: out.Plan.ID,
		Origin: string(out.Plan.Origin), Items: len(out.Plan.Items), ExpectedBytes: out.Plan.TotalBytes, Reason: out.Plan.Request}); err != nil {
		out.Warnings = append(out.Warnings, "couldn't record the plan in the audit log: "+err.Error())
	}
	return out, nil
}

// ShowPlan loads a plan and verifies it, reporting its status (pending,
// expired, applied or invalid) rather than failing.
func ShowPlan(ctx context.Context, o OpportunityOptions, id string) (PlanResult, error) {
	if o.StorePath == "" {
		o.StorePath = store.DefaultPath(o.Home)
	}
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
	if o.Catalog == nil {
		cat, err := recipes.Load()
		if err != nil {
			return PlanResult{}, err
		}
		o.Catalog = cat
	}
	pol, _, err := config.Policy(o.Home)
	if err != nil {
		return PlanResult{}, err
	}
	p, err := planner.Load(planner.Dir(o.StorePath), id)
	if err != nil && !errors.Is(err, planner.ErrTampered) {
		return PlanResult{}, err
	}
	out := PlanResult{Plan: p, Refused: []planner.Refusal{}, Snapshot: snapshotNote(ctx, o.Runner)}
	if err != nil {
		out.Status, out.Warnings = planner.StatusInvalid, []string{err.Error()}
		return out, nil
	}
	st, err := store.Open(o.StorePath)
	if err != nil {
		return PlanResult{}, err
	}
	defer func() { _ = st.Close() }()
	rec, err := st.Plan(id)
	if errors.Is(err, store.ErrNoPlan) {
		out.Status, out.Warnings = planner.StatusInvalid, []string{"macdoc has no record of this plan"}
		return out, nil
	}
	if err != nil {
		return PlanResult{}, err
	}
	verr := planner.Verify(p, rec, o.Catalog, pol, o.Now)
	out.Status = planner.StatusOf(verr)
	if out.Status == planner.StatusInvalid {
		out.Warnings = []string{verr.Error()}
	}
	return out, nil
}

// ListPlans returns recent plan records, newest first.
func ListPlans(o OpportunityOptions, limit int) ([]store.PlanRecord, error) {
	if o.StorePath == "" {
		o.StorePath = store.DefaultPath(o.Home)
	}
	st, err := store.Open(o.StorePath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = st.Close() }()
	return st.Plans(limit)
}

// snapshotNote counts local Time Machine snapshots (read-only), -1 if
// unknown.
func snapshotNote(ctx context.Context, r platform.Runner) SnapshotNote {
	if r == nil {
		return SnapshotNote{Count: -1}
	}
	s, err := probe.CollectSnapshots(ctx, r)
	if err != nil {
		return SnapshotNote{Count: -1}
	}
	return SnapshotNote{Count: s.Count}
}
