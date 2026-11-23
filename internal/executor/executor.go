// Package executor is the ONLY package allowed to delete, move, or run
// mutating commands (docs/adr/0003-executor-is-the-only-mutator.md). Only
// internal/cli and cmd/macdoc may import it (scripts/imports.sh), and it
// acts only on plans that pass planner.Verify (docs/SAFETY_MODEL.md §4).
//
// Invariants:
//   - Every item is re-checked immediately before it is acted on: the
//     target's identity, canonical path, allowlist and protected paths (with
//     a freshly built policy), preconditions and contents. Any mismatch skips
//     the item; the executor never adjusts a target to make it fit.
//   - Nothing changes unless the audit log can be written.
//   - A plan runs at most once, even after a crash: it is marked applied
//     before the first item is touched.
//   - Folders are moved into macdoc's quarantine, never deleted in place.
//     The only deletion is a purge of an entry macdoc itself created, which
//     with DeleteNow follows the move at once (ADR 0004).
//   - Native commands run from a fixed argv in a recipe, with no shell.
package executor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/sagnik26/macdoc/internal/audit"
	"github.com/sagnik26/macdoc/internal/config"
	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/planner"
	"github.com/sagnik26/macdoc/internal/platform"
	"github.com/sagnik26/macdoc/internal/precond"
	"github.com/sagnik26/macdoc/internal/quarantine"
	"github.com/sagnik26/macdoc/internal/recipes"
	"github.com/sagnik26/macdoc/internal/safety"
	"github.com/sagnik26/macdoc/internal/sandbox"
	"github.com/sagnik26/macdoc/internal/store"
)

// DefaultNativeTimeout bounds one native cleanup command.
const DefaultNativeTimeout = 15 * time.Minute

// NativeExec runs a fixed argv (no shell) and returns its combined output and
// exit code. err means it couldn't run or timed out.
type Native func(ctx context.Context, argv []string, timeout time.Duration) (out []byte, exit int, err error)

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
	Exec Native
	// Priv runs a privileged command (ADR 0009). Nil means the real one,
	// which asks the system for the password at the terminal and is refused
	// in sandbox mode.
	Priv Native
	// Sys removes one system item with administrator rights (ADR 0014). Nil
	// means the real one, which is refused in sandbox mode and without a terminal.
	Sys Native
	// AppDirs is where installed apps are looked for when a detector is run
	// again before an item is removed; nil means the usual folders. Tests set it.
	AppDirs []string
	// SystemBase is put in front of the system folders when a detector is run
	// again before a system item is removed. Tests set it; production leaves it empty.
	SystemBase    string
	Now           func() time.Time
	run           string        // this invocation's audit run ID
	QuarantineTTL time.Duration // default quarantine.DefaultTTL
	NativeTimeout time.Duration // default DefaultNativeTimeout
	// FreeSpace measures free space for the before/after report. Optional.
	FreeSpace func(ctx context.Context) (int64, error)
	// OnItem, if set, is told when an item starts and when it ends.
	OnItem func(ev Event)
	// StageHook is a test seam: it is called at each step of a move or purge
	// (for example "pending", "moved", "purged-payload") so a crash or a swap
	// can be simulated there. Production leaves it nil.
	StageHook func(stage string)
	// DeleteNow purges each item's quarantine entry right after the move,
	// through the same checks as Purge, so the space is freed at once. If
	// that purge is refused, the item stays restorable in quarantine.
	DeleteNow bool
	// NonInteractive marks a run started by reclaim --yes (ADR 0014 §5). Such
	// a run does only SAFE and CAUTION quarantine moves from a plan a person
	// made here; every other item is skipped, and each audit record says so.
	NonInteractive bool

	realExec bool // Exec is the real one
}

// Outcome is what happened to one item.
type Outcome string

// The outcomes an item can have.
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
	Deleted      bool           `json:"deleted,omitempty"`     // its quarantine entry was purged (DeleteNow), or a sweep removed its files
	Files        int            `json:"files,omitempty"`       // a sweep: how many files it removed
	ExitCode     *int           `json:"exit_code,omitempty"`
}

// ItemEvent reports progress: Result is nil when the item starts.
type Event struct {
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

// MovedBytes is what is held in quarantine (still on disk until purged).
func (r Report) MovedBytes() (n int64) {
	for _, it := range r.Items {
		if !it.Deleted {
			n += it.MovedBytes
		}
	}
	return n
}

// DeletedBytes is what was moved and then deleted at once (DeleteNow).
func (r Report) DeletedBytes() (n int64) {
	for _, it := range r.Items {
		if it.Deleted {
			n += it.MovedBytes
		}
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
	// ErrExcluded means a plan item's recipe is excluded in your settings.
	ErrExcluded = errors.New("excluded in your settings")
)

// Approval is one person's confirmation of one item. Individually is true
// when they confirmed this item on its own: a y/N answer for it, or the
// plan's typed code for a RISKY item. A group approval (one Enter over the
// SAFE and CAUTION items of a plan macdoc made) is Individually false.
type Approval struct{ Individually bool }

// Approvals maps opportunity IDs to the confirmations given for them.
type Approvals map[string]Approval

// approves reports whether item may run. A Claude-origin plan's items, and
// RISKY items, need their own confirmation: a group approval isn't enough
// (AGENTS invariant 6, Q2 of P6-R1).
func (a Approvals) approves(item model.PlanItem, origin model.Origin) bool {
	ap, ok := a[item.OpportunityID]
	if !ok {
		return false
	}
	if (origin == model.OriginClaude || item.Risk == model.RiskRisky || item.Privileged) && !ap.Individually {
		return false
	}
	return true
}

// Apply verifies plan, locks it as applied, then performs every approved
// item in order, re-checking each one first. approved holds the opportunity
// IDs the person confirmed; anything else is skipped. An error means nothing
// was changed (the plan didn't verify, couldn't be locked, or the sandbox is
// misconfigured); once items start, failures are reported per item.
func Apply(ctx context.Context, cfg Config, plan model.Plan, approved Approvals) (Report, error) {
	rep := Report{PlanID: plan.ID, Origin: plan.Origin, Items: []ItemResult{}}
	if err := cfg.defaults(); err != nil {
		return rep, err
	}
	if _, _, err := sandbox.Root(); err != nil {
		return rep, err
	}
	if err := guardStateDir(cfg.stateDir()); err != nil {
		return rep, fmt.Errorf("nothing was changed: %w", err)
	}
	if len(approved) == 0 {
		return rep, ErrNoApproval
	}
	// A fresh policy, so protected folders that became symlinks since the
	// plan are covered at their real location (P0-W3 Q2).
	pol, settings, err := config.Policy(cfg.Home)
	if err != nil {
		return rep, err
	}
	if cfg.SystemBase != "" {
		// A test fixture standing in for /Library: only in sandbox mode, never
		// against the real system folders.
		if _, active, _ := sandbox.Root(); !active {
			return rep, errors.New("executor: SystemBase is only for sandboxed tests")
		}
		pol.SetSystemBase(cfg.SystemBase)
	}
	// Settings can exclude a recipe after the plan was made; refuse before
	// anything changes.
	for _, it := range plan.Items {
		if settings.Excludes(it.RecipeID) {
			return rep, fmt.Errorf("%w: %s (%s); nothing was changed", ErrExcluded, it.OpportunityID, it.RecipeID)
		}
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
	if err := cfg.audit(audit.Record{Event: audit.Started, Plan: plan.ID, Origin: string(plan.Origin),
		Items: len(plan.Items), Expected: plan.TotalBytes, NonInteractive: cfg.NonInteractive}); err != nil {
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
	fin := audit.Record{Event: audit.Finished, Plan: plan.ID, Origin: string(plan.Origin),
		Items: len(rep.Items), Expected: rep.NativeExpected() + rep.MovedBytes() + rep.DeletedBytes(), Result: finishResult(rep)}
	if n, ok := rep.Freed(); ok {
		fin.Actual = &n
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
	if c.run == "" {
		c.run = audit.NewRunID()
	}
	if c.QuarantineTTL <= 0 {
		c.QuarantineTTL = quarantine.TTL
	}
	if c.NativeTimeout <= 0 {
		c.NativeTimeout = DefaultNativeTimeout
		if s, err := config.Load(c.Home); err == nil {
			c.NativeTimeout = s.Timeout()
		}
	}
	if c.Exec == nil {
		c.Exec, c.realExec = realExec, true
	}
	if c.Priv == nil {
		c.Priv = realPrivileged
	}
	if c.Sys == nil {
		c.Sys = realSystem
	}
	return nil
}

func (c *Config) stateDir() string       { return filepath.Dir(c.StorePath) }
func (c *Config) quarantineRoot() string { return quarantine.Root(c.StorePath) }

func (c *Config) audit(rec audit.Record) error {
	if rec.TS.IsZero() {
		rec.TS = c.Now()
	}
	rec.Run = c.run
	return audit.Append(audit.Path(c.StorePath), rec)
}

// tightenStateDir makes macdoc's state directory private (P2-W2 Q4). It
// refuses a state directory that is outside the sandbox, a symlink, or not
// owned by the user, before anything is changed (P6-R1 F4, F6).
func tightenStateDir(dir string) error {
	if err := sandbox.Check(dir); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := checkPrivateDir(dir); err != nil {
		return err
	}
	return os.Chmod(dir, 0o700) //nolint:gosec // G302: a private folder, owner rwx only.
}

// guardStateDir refuses to act at all when sandbox mode is on and the state
// directory is outside the sandbox. Apply, Restore and Purge call it before
// any write, the audit log included (P6-R1 F6).
func guardStateDir(dir string) error {
	return sandbox.Check(dir)
}

// checkPrivateDir accepts a folder only if it is a real folder (not a
// symlink) owned by the user, at its canonical path. Anything macdoc makes
// private (the state folder, the quarantine root) must pass this before
// it is chmodded or written into (P6-R1 F4).
func checkPrivateDir(dir string) error {
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
		return fmt.Errorf("%s isn't a plain folder", filepath.Base(dir))
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Getuid() {
		return fmt.Errorf("%s isn't owned by you", filepath.Base(dir))
	}
	if c, err := safety.Canonicalize(dir); err != nil || c != dir {
		return fmt.Errorf("%s isn't at its canonical path", filepath.Base(dir))
	}
	return nil
}
