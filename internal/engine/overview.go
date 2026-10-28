package engine

import (
	"context"
	"errors"
	"time"

	"macdoc/internal/model"
	"macdoc/internal/platform"
	"macdoc/internal/recipes"
	"macdoc/internal/scanner"
	"macdoc/internal/store"
)

// staleOverviewScan is the scan age after which the overview suggests a
// refresh (it never rescans on its own unless there is no scan at all).
const staleOverviewScan = 24 * time.Hour

// OverviewOptions configures Overview.
type OverviewOptions struct {
	Home     string
	Runner   platform.Runner
	Version  string
	Rescan   bool                   // run a fresh scan first
	Progress func(scanner.Progress) // scan progress, if a scan runs
	Now      time.Time              // default time.Now()
	Catalog  *recipes.Catalog       // default recipes.Load()
	// ScanRoot overrides the scan root (tests); empty means the Data volume.
	ScanRoot string
	Workers  int // scanner concurrency (0 = default)
	// Step, if set, is called as each stage starts: StepHealth, StepScan
	// (a fresh scan), StepLoad (reading the latest scan), StepRank.
	Step func(OverviewStep)
}

// OverviewStep names a stage of Overview.
type OverviewStep string

const (
	StepHealth OverviewStep = "health"
	StepScan   OverviewStep = "scan"
	StepLoad   OverviewStep = "load"
	StepRank   OverviewStep = "rank"
)

// OverviewResult is everything `macdoc doctor` shows on one screen.
type OverviewResult struct {
	Health        DoctorResult      `json:"health"`
	Scan          model.ScanInfo    `json:"scan"`
	ScanAge       string            `json:"scan_age"`
	Scanned       bool              `json:"scanned"`                // a scan ran as part of this overview
	ScanSummary   *ScanSummary      `json:"scan_summary,omitempty"` // set when Scanned
	ScanStale     bool              `json:"scan_stale"`             // older than 24h
	TopLevel      []model.Node      `json:"top_level"`
	DataVolume    model.Bytes       `json:"data_volume_used"`
	Opportunities OpportunityResult `json:"opportunities"`
	Quarantine    *QuarantineNote   `json:"quarantine,omitempty"` // nil when it's empty
	Warnings      []string          `json:"warnings,omitempty"`
}

// QuarantineNote summarizes what apply moved aside. Expired entries are only
// reported, never purged.
type QuarantineNote struct {
	Entries      int   `json:"entries"`
	HeldBytes    int64 `json:"held_bytes"`
	ExpiredCount int   `json:"expired_count"`
	ExpiredBytes int64 `json:"expired_bytes"`
}

// Overview runs the health check, makes sure there is a scan (scanning only
// if none exists or Rescan is set), and computes the reclaimable space. Only
// a failure to scan or rank is an error; health problems become warnings.
func Overview(ctx context.Context, o OverviewOptions) (OverviewResult, error) {
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
	step := func(s OverviewStep) {
		if o.Step != nil {
			o.Step(s)
		}
	}
	step(StepHealth)
	res := OverviewResult{Health: Doctor(ctx, o.Runner)}
	sp := store.DefaultPath(o.Home)

	info, err := latestComplete(sp)
	switch {
	case o.Rescan || errors.Is(err, store.ErrNoScan):
		step(StepScan)
		sum, err := Scan(ctx, ScanOptions{Home: o.Home, Version: o.Version, Progress: o.Progress, Runner: o.Runner, Root: o.ScanRoot, Workers: o.Workers})
		if err != nil {
			return res, err
		}
		res.Scanned = true
		res.ScanSummary = &sum
		res.TopLevel, res.DataVolume = sum.TopLevel, sum.DataVolumeUsed
		info = sum.Info
	case err != nil:
		return res, err
	default:
		step(StepLoad)
		res.DataVolume = dataVolumeUsed(ctx, o.Runner)
		if res.TopLevel, err = topLevel(sp, info); err != nil {
			return res, err
		}
	}
	res.Scan = info
	age := o.Now.Sub(scanTime(info))
	res.ScanAge = FormatAge(age)
	res.ScanStale = age > staleOverviewScan

	step(StepRank)
	opps, err := Opportunities(ctx, OpportunityOptions{Home: o.Home, Runner: o.Runner, Now: o.Now, Catalog: o.Catalog})
	if err != nil {
		return res, err
	}
	res.Opportunities = opps
	res.Warnings = append(res.Warnings, opps.Warnings...)
	if q, err := Quarantine(OpportunityOptions{Home: o.Home, Now: o.Now}); err == nil && len(q.Entries) > 0 {
		res.Quarantine = &QuarantineNote{Entries: len(q.Entries), HeldBytes: q.HeldBytes, ExpiredCount: q.ExpiredCount, ExpiredBytes: q.ExpiredBytes}
	}
	return res, nil
}

func latestComplete(storePath string) (model.ScanInfo, error) {
	st, err := store.Open(storePath)
	if err != nil {
		return model.ScanInfo{}, err
	}
	defer func() { _ = st.Close() }()
	return st.LatestScan(model.ScanComplete)
}

// topLevel returns the stored direct children of the scan root.
func topLevel(storePath string, info model.ScanInfo) ([]model.Node, error) {
	st, err := store.Open(storePath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = st.Close() }()
	root := info.Root
	if root == scanner.DataVolume {
		root = "/" // the Data volume is reported with firmlinks mapped back to /
	}
	nodes, err := st.Nodes(info.ID, store.NodeQuery{Prefix: root, MaxDepth: 1})
	if err != nil {
		return nil, err
	}
	return children(nodes, root), nil
}

// LastScan returns the latest complete scan and its age, or store.ErrNoScan.
// It only reads macdoc's own store, so it is fast enough for a welcome screen.
func LastScan(home string, now time.Time) (model.ScanInfo, string, error) {
	info, err := latestComplete(store.DefaultPath(home))
	if err != nil {
		return model.ScanInfo{}, "", err
	}
	return info, FormatAge(now.Sub(scanTime(info))), nil
}
