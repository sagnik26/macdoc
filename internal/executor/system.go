package executor

import (
	"fmt"
	"os"

	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/planner"
	"github.com/sagnik26/macdoc/internal/recipes"
)

// System implements planner.Performer for a system removal (ADR 0014 §1-2).
func (p *performer) System(item model.PlanItem, target string) error {
	return p.do(item, func() (ItemResult, error) { return p.system(item, target) })
}

// system re-checks one system item and removes it with administrator rights.
// Right before acting it confirms the plan is a person's, the item is still
// the one planned (same device and inode, not a symlink), still one item of its
// kind that the safety rules accept, and still found by its own detector (an
// orphan is still an orphan). The command is built here from the kind and the
// target, never taken from the plan. Nothing is quarantined: root-owned data
// can't be, so the removal is permanent.
func (p *performer) system(item model.PlanItem, target string) (ItemResult, error) {
	res := ItemResult{}
	r, _ := p.cfg.Catalog.Get(item.RecipeID)
	kind := recipes.SystemKindOf(r)
	switch {
	case p.plan.Origin == model.OriginClaude:
		return res, skip{"it needs administrator rights, and a plan Claude proposed can't include that"}
	case kind == "":
		return res, skip{"its recipe isn't a system location"}
	case !item.Privileged:
		return res, skip{"its administrator-rights setting doesn't match its recipe's"}
	case item.Target != target:
		return res, skip{"its path doesn't match the plan"}
	}
	if err := p.systemUnchanged(item, target, kind); err != nil {
		return res, err
	}
	if err := p.stillFound(r, target); err != nil {
		return res, err
	}
	argv := recipes.SystemArgv(kind, target)
	out, code, err := p.cfg.Sys(p.ctx, argv, p.cfg.NativeTimeout)
	res.ExitCode = &code
	p.lastOut = out
	if err != nil {
		return res, fmt.Errorf("%s couldn't run: %w", argv[0], err)
	}
	if code != 0 {
		return res, fmt.Errorf("%s exited with status %d", argv[0], code)
	}
	if _, err := os.Lstat(target); err == nil {
		return res, fmt.Errorf("%s ran, but the item is still there", argv[0])
	}
	res.Deleted = true
	if item.BytesSource != model.SourceUnknown {
		res.MovedBytes = item.ExpectedBytes
	}
	return res, nil
}

// systemUnchanged skips unless the item is still the one that was planned.
func (p *performer) systemUnchanged(item model.PlanItem, target, kind string) error {
	fi, err := os.Lstat(target)
	switch {
	case err != nil:
		return skipf("it can't be read: %v", err)
	case fi.Mode()&os.ModeSymlink != 0:
		return skip{"it is now a symlink"}
	}
	if fp := planner.Fingerprint(fi); fp.Dev != item.Fingerprint.Dev || fp.Ino != item.Fingerprint.Ino {
		return skip{"it was replaced since the plan"}
	}
	if err := p.pol.CheckSystemTarget(target, kind); err != nil {
		return skipf("the safety rules refuse it: %v", err)
	}
	return nil
}

// stillFound runs the recipe's detector again, read-only, and skips unless it
// still finds target, and (when the recipe wants idle time) it is still idle.
func (p *performer) stillFound(r recipes.Recipe, target string) error {
	d, ok := recipes.Lookup(r.Detector)
	if !ok {
		return skipf("its detector %s is gone", r.Detector)
	}
	env := recipes.Env{Home: p.cfg.Home, Runner: p.cfg.Runner, Now: p.cfg.Now(), AppDirs: p.cfg.AppDirs, SystemBase: p.cfg.SystemBase}
	fs, err := d(p.ctx, env, r)
	if err != nil {
		return skipf("couldn't confirm it is still there: %v", err)
	}
	for _, f := range fs {
		if f.Target != target {
			continue
		}
		if r.StaleDays > 0 && (f.LastUsed.IsZero() || !recipes.Stale(env, f.LastUsed.Unix(), r.StaleDays)) {
			return skip{"it changed recently"}
		}
		return nil
	}
	return skip{"macdoc no longer finds it (for example its app is installed again)"}
}
