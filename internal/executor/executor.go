// Package executor is the ONLY package allowed to delete, move, or run
// mutating commands (AGENTS.md invariant 2). It is imported solely by the
// CLI apply and quarantine commands, and it acts only on plans that pass
// planner.Verify (docs/SAFETY_MODEL.md §4).
//
// For each item it re-checks everything immediately before acting (the
// target's identity, canonical path, allowlist and protected paths with a
// freshly built policy, preconditions, contents), skips the item on any
// mismatch, and records every step in the audit log. Folders are moved into
// macdoc's quarantine, never deleted; the only deletion is a quarantine purge
// of an entry macdoc itself created.
package executor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"macdoc/internal/audit"
	"macdoc/internal/model"
	"macdoc/internal/planner"
	"macdoc/internal/platform"
	"macdoc/internal/precond"
	"macdoc/internal/quarantine"
	"macdoc/internal/recipes"
	"macdoc/internal/safety"
	"macdoc/internal/sandbox"
	"macdoc/internal/store"
)

// DefaultNativeTimeout bounds one native cleanup command.
const DefaultNativeTimeout = 15 * time.Minute

// NativeExec runs a fixed argv (no shell) and returns its combined output and
// exit code. err means it couldn't run or timed out.
type NativeExec func(ctx context.Context, argv []string, timeout time.Duration) (out []byte, exit int, err error)

// Config is what the executor needs. Store and StorePath are required.
type Config struct {
	Store     *store.Store
	StorePath string // the state directory is its folder
	Home      string
	Catalog   *recipes.Catalog
	// Runner runs read-only commands: preconditions and re-listing tool
	// identifiers.
	Runner platform.Runner
	// Exec runs native cleanup commands. Nil means the real one, which is
	// refused in sandbox mode.
	Exec          NativeExec
	Now           func() time.Time
	QuarantineTTL time.Duration // default quarantine.DefaultTTL
	NativeTimeout time.Duration // default DefaultNativeTimeout
	// FreeSpace measures free space for the before/after report. Optional.
	FreeSpace func(ctx context.Context) (int64, error)
	// OnItem, if set, is told when an item starts and when it ends.
	OnItem func(ev ItemEvent)

	realExec bool // Exec is the real one
}

// Outcome is what happened to one item.
type Outcome string

const (
	OutcomeDone    Outcome = "done"
	OutcomeSkipped Outcome = "skipped" // a check failed or it wasn't approved; nothing was changed
	OutcomeFailed  Outcome = "failed"  // the action ran and didn't succeed
)

// ItemResult is one item's outcome.
type ItemResult struct {
	Item         model.PlanItem `json:"item"`
	Outcome      Outcome        `json:"outcome"`
	Reason       string         `json:"reason,omitempty"`
	QuarantineID string         `json:"quarantine_id,omitempty"`
	MovedBytes   int64          `json:"moved_bytes,omitempty"` // into quarantine
	ExitCode     *int           `json:"exit_code,omitempty"`
}

// ItemEvent reports progress: Result is nil when the item starts.
type ItemEvent struct {
	Index, Total int
	Item         model.PlanItem
	Result       *ItemResult
}

// Report is the outcome of an apply.
type Report struct {
	PlanID     string       `json:"plan_id"`
	Origin     model.Origin `json:"origin"`
	Items      []ItemResult `json:"items"`
	FreeBefore *int64       `json:"free_before,omitempty"`
	FreeAfter  *int64       `json:"free_after,omitempty"`
	Warnings   []string     `json:"warnings,omitempty"`
}

// Count returns how many items had outcome o.
func (r Report) Count(o Outcome) (n int) {
	for _, it := range r.Items {
		if it.Outcome == o {
			n++
		}
	}
	return n
}

// MovedBytes is what went into quarantine (still on disk until purged).
func (r Report) MovedBytes() (n int64) {
	for _, it := range r.Items {
		n += it.MovedBytes
	}
	return n
}

// NativeExpected is the expected size of native items that ran
// successfully, with known sizes only.
func (r Report) NativeExpected() (n int64) {
	for _, it := range r.Items {
		if it.Outcome == OutcomeDone && it.Item.Method == model.MethodNative && it.Item.BytesSource != model.SourceUnknown {
			n += it.Item.ExpectedBytes
		}
	}
	return n
}

// Freed is the measured change in free space, if both ends were measured.
func (r Report) Freed() (int64, bool) {
	if r.FreeBefore == nil || r.FreeAfter == nil {
		return 0, false
	}
	return *r.FreeAfter - *r.FreeBefore, true
}

// Errors that stop an apply before anything is changed.
var (
	ErrNoApproval = errors.New("no item was approved")
)

// Apply verifies plan, locks it as applied, then performs every approved
// item in order, re-checking each one first. approved holds the opportunity
// IDs the person confirmed; anything else is skipped. An error means nothing
// was changed (the plan didn't verify, couldn't be locked, or the sandbox is
// misconfigured); once items start, failures are reported per item.
func Apply(ctx context.Context, cfg Config, plan model.Plan, approved map[string]bool) (Report, error) {
	rep := Report{PlanID: plan.ID, Origin: plan.Origin, Items: []ItemResult{}}
	if err := cfg.defaults(); err != nil {
		return rep, err
	}
	if _, _, err := sandbox.Root(); err != nil {
		return rep, err
	}
	if len(approved) == 0 {
		return rep, ErrNoApproval
	}
	// A fresh policy, so protected folders that became symlinks since the
	// plan are covered at their real location (P0-W3 Q2).
	pol, err := safety.NewPolicy(cfg.Home, nil)
	if err != nil {
		return rep, err
	}
	rec, err := cfg.Store.Plan(plan.ID)
	if errors.Is(err, store.ErrNoPlan) {
		return rep, planner.ErrUnknown
	}
	if err != nil {
		return rep, err
	}
	if err := planner.Verify(plan, rec, cfg.Catalog, pol, cfg.Now()); err != nil {
		return rep, err
	}
	if err := tightenStateDir(cfg.stateDir()); err != nil {
		return rep, err
	}
	// The lock: from here the plan can never be applied again, even if this
	// process dies. What happened is in the audit log and the quarantine.
	if err := cfg.Store.MarkApplied(plan.ID); err != nil {
		if errors.Is(err, store.ErrPlanNotPending) {
			return rep, planner.ErrApplied
		}
		return rep, err
	}
	if cfg.FreeSpace != nil {
		if n, err := cfg.FreeSpace(ctx); err == nil {
			rep.FreeBefore = &n
		}
	}
	if err := cfg.audit(audit.Record{Event: audit.ApplyStarted, Plan: plan.ID, Origin: string(plan.Origin),
		Items: len(plan.Items), ExpectedBytes: plan.TotalBytes}); err != nil {
		// Without an audit trail nothing may change.
		return rep, fmt.Errorf("couldn't write the audit log, nothing was changed: %w", err)
	}

	p := &performer{cfg: cfg, pol: pol, plan: plan, approved: approved,
		pre: &precond.Checker{Runner: cfg.Runner, Home: cfg.Home}, ctx: ctx, total: len(plan.Items)}
	planner.Perform(plan, p)
	rep.Items = p.results
	rep.Warnings = p.warnings

	if cfg.FreeSpace != nil {
		if n, err := cfg.FreeSpace(ctx); err == nil {
			rep.FreeAfter = &n
		}
	}
	fin := audit.Record{Event: audit.ApplyFinished, Plan: plan.ID, Origin: string(plan.Origin),
		Items: len(rep.Items), ExpectedBytes: rep.NativeExpected() + rep.MovedBytes(), Result: finishResult(rep)}
	if n, ok := rep.Freed(); ok {
		fin.ActualBytes = &n
	}
	if err := cfg.audit(fin); err != nil {
		rep.Warnings = append(rep.Warnings, "couldn't write the final audit record: "+err.Error())
	}
	return rep, nil
}

func finishResult(r Report) string {
	switch {
	case r.Count(OutcomeDone) == len(r.Items):
		return "ok"
	case r.Count(OutcomeDone) == 0:
		return "nothing"
	}
	return "partial"
}

func (c *Config) defaults() error {
	if c.Store == nil || c.StorePath == "" {
		return errors.New("executor: store is required")
	}
	if c.Home == "" {
		return errors.New("executor: home is required")
	}
	if c.Catalog == nil {
		cat, err := recipes.Load()
		if err != nil {
			return err
		}
		c.Catalog = cat
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.QuarantineTTL <= 0 {
		c.QuarantineTTL = quarantine.DefaultTTL
	}
	if c.NativeTimeout <= 0 {
		c.NativeTimeout = DefaultNativeTimeout
	}
	if c.Exec == nil {
		c.Exec, c.realExec = realExec, true
	}
	return nil
}

func (c *Config) stateDir() string       { return filepath.Dir(c.StorePath) }
func (c *Config) quarantineRoot() string { return quarantine.Root(c.StorePath) }

func (c *Config) audit(rec audit.Record) error {
	if rec.TS.IsZero() {
		rec.TS = c.Now()
	}
	return audit.Append(audit.Path(c.StorePath), rec)
}

// tightenStateDir makes macdoc's state directory private (P2-W2 Q4).
func tightenStateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.Chmod(dir, 0o700) //nolint:gosec // G302: a private folder, owner rwx only.
}
