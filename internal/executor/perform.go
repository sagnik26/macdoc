package executor

import (
	"context"
	"errors"
	"fmt"

	"macdoc/internal/audit"
	"macdoc/internal/model"
	"macdoc/internal/precond"
	"macdoc/internal/recipes"
	"macdoc/internal/safety"
	"macdoc/internal/sandbox"
)

// performer implements planner.Performer: every action the dry run showed,
// and nothing else, reaches the executor through it.
type performer struct {
	cfg      Config
	pol      *safety.Policy
	plan     model.Plan
	approved map[string]bool
	pre      *precond.Checker
	ctx      context.Context

	index, total int
	results      []ItemResult
	warnings     []string
	lastOut      []byte // the last native command's output, for its audit record
}

// skip is a failed check: nothing was changed.
type skip struct{ reason string }

func (s skip) Error() string { return s.reason }

func skipf(format string, a ...any) error { return skip{fmt.Sprintf(format, a...)} }

func (p *performer) RunNative(item model.PlanItem, argv []string) error {
	return p.do(item, func() (ItemResult, error) { return p.native(item, argv) })
}

func (p *performer) Quarantine(item model.PlanItem, target string) error {
	return p.do(item, func() (ItemResult, error) { return p.quarantine(item, target) })
}

// do runs one item: approval, common checks, the action, then the audit
// record and progress report.
func (p *performer) do(item model.PlanItem, act func() (ItemResult, error)) error {
	p.index++
	if p.cfg.OnItem != nil {
		p.cfg.OnItem(ItemEvent{Index: p.index, Total: p.total, Item: item})
	}
	res := ItemResult{Item: item}
	var err error
	switch {
	case !p.approved[item.OpportunityID]:
		err = skip{"not approved"}
	case p.ctx.Err() != nil:
		err = skip{"apply was interrupted"}
	default:
		if err = p.common(item); err == nil {
			res, err = act()
			res.Item = item
		}
	}
	var sk skip
	switch {
	case err == nil:
		res.Outcome = OutcomeDone
	case errors.As(err, &sk):
		res.Outcome, res.Reason = OutcomeSkipped, sk.reason
	default:
		res.Outcome, res.Reason = OutcomeFailed, err.Error()
	}
	rec := audit.Record{Plan: p.plan.ID, Origin: string(p.plan.Origin), Item: item.OpportunityID, Recipe: item.RecipeID,
		Target: item.Target, Method: string(item.Method), Argv: item.Argv, QuarantineID: res.QuarantineID,
		ExpectedBytes: item.ExpectedBytes, ExitCode: res.ExitCode, Reason: res.Reason, Output: lastOutput(p, res)}
	switch res.Outcome {
	case OutcomeDone:
		rec.Event, rec.Result = audit.ItemDone, "ok"
		if res.MovedBytes > 0 {
			n := res.MovedBytes
			rec.ActualBytes = &n
		}
	case OutcomeSkipped:
		rec.Event, rec.Result = audit.ItemSkipped, "skipped"
	default:
		rec.Event, rec.Result = audit.ItemFailed, "failed"
	}
	if aerr := p.cfg.audit(rec); aerr != nil {
		p.warnings = append(p.warnings, fmt.Sprintf("%s: couldn't write the audit record: %v", item.OpportunityID, aerr))
	}
	p.results = append(p.results, res)
	if p.cfg.OnItem != nil {
		r := res
		p.cfg.OnItem(ItemEvent{Index: p.index, Total: p.total, Item: item, Result: &r})
	}
	return err
}

// common re-checks what applies to every item: the recipe still exists
// with this method and argv, and its preconditions hold.
func (p *performer) common(item model.PlanItem) error {
	r, ok := p.cfg.Catalog.Get(item.RecipeID)
	if !ok {
		return skip{"its recipe isn't in this version of macdoc"}
	}
	if r.Method != item.Method {
		return skip{"its recipe's method changed"}
	}
	if !precond.Known(item.Preconditions) {
		return skip{"it has a precondition macdoc can't check"}
	}
	if err := p.pre.Check(p.ctx, item); err != nil {
		return skip{err.Error()}
	}
	return nil
}

// native re-checks and runs a native command.
func (p *performer) native(item model.PlanItem, argv []string) (ItemResult, error) {
	res := ItemResult{}
	r, _ := p.cfg.Catalog.Get(item.RecipeID)
	if !recipes.MatchesNative(r, argv) {
		return res, skip{"its command doesn't match the recipe's"}
	}
	if !recipes.ToolInstalled(argv[0]) {
		return res, skipf("%s isn't installed any more", argv[0])
	}
	if _, active, _ := sandbox.Root(); active && p.cfg.realExec {
		return res, skip{"native commands don't run in sandbox mode"}
	}
	// A dynamic identifier (a simulator, an AVD, a model) must still be in
	// the tool's own listing.
	if arg, ok := nativeArg(r, argv); ok {
		listed, err := stillListed(p.ctx, p.cfg, r, arg)
		if err != nil {
			return res, skipf("couldn't confirm %s is still there: %v", arg, err)
		}
		if !listed {
			return res, skipf("%s is no longer listed by %s", arg, argv[0])
		}
	}
	out, code, err := p.cfg.Exec(p.ctx, argv, p.cfg.NativeTimeout)
	res.ExitCode = &code
	p.lastOut = out
	if err != nil {
		return res, fmt.Errorf("%s couldn't run: %w", argv[0], err)
	}
	if code != 0 {
		return res, fmt.Errorf("%s exited with status %d", argv[0], code)
	}
	return res, nil
}

// nativeArg returns the identifier argv puts in the recipe's {id} slot.
func nativeArg(r recipes.Recipe, argv []string) (string, bool) {
	for i, a := range r.Native.Argv {
		if a == recipes.Placeholder && i < len(argv) {
			return argv[i], true
		}
	}
	return "", false
}

// stillListed re-runs the recipe's detector (read-only) and reports whether
// it still finds arg.
func stillListed(ctx context.Context, cfg Config, r recipes.Recipe, arg string) (bool, error) {
	d, ok := recipes.Lookup(r.Detector)
	if !ok {
		return false, fmt.Errorf("unknown detector %q", r.Detector)
	}
	fs, err := d(ctx, recipes.Env{Home: cfg.Home, Runner: cfg.Runner, Now: cfg.Now()}, r)
	if err != nil {
		return false, err
	}
	for _, f := range fs {
		if f.NativeArg == arg {
			return true, nil
		}
	}
	return false, nil
}

func lastOutput(p *performer, res ItemResult) string {
	if res.Item.Method != model.MethodNative || p.lastOut == nil {
		return ""
	}
	out := audit.Tail(p.lastOut)
	p.lastOut = nil
	return out
}
