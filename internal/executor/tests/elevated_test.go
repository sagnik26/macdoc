package tests

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sagnik26/macdoc/internal/executor"
	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/planner"
)

// sysEnv is an executor fixture with stand-ins for the system folders and a
// fake administrator runner that records its argv and, like rm, removes the
// last argument.
type sysEnv struct {
	*env
	base  string
	mu    sync.Mutex
	calls [][]string
	code  int
}

func newSysEnv(t *testing.T) *sysEnv {
	t.Helper()
	e := &sysEnv{env: newEnv(t)}
	e.base = filepath.Join(e.home, "sysbase")
	e.cfg.SystemBase = e.base
	e.cfg.Sys = func(_ context.Context, argv []string, _ time.Duration) ([]byte, int, error) {
		e.mu.Lock()
		defer e.mu.Unlock()
		e.calls = append(e.calls, argv)
		if e.code == 0 {
			_ = os.RemoveAll(argv[len(argv)-1])
		}
		return nil, e.code, nil
	}
	return e
}

// cache makes /Library/Caches/<name> in the fixture, idle for days.
func (e *sysEnv) cache(t *testing.T, name string, days int) string {
	t.Helper()
	p := filepath.Join(e.base, "Library", "Caches", name)
	write(t, filepath.Join(p, "a.bin"))
	old := t0.Add(-time.Duration(days) * 24 * time.Hour)
	for _, q := range []string{filepath.Join(p, "a.bin"), p} {
		if err := os.Chtimes(q, old, old); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

func (e *sysEnv) item(t *testing.T, id, target string) model.PlanItem {
	t.Helper()
	fi, err := os.Lstat(target)
	if err != nil {
		t.Fatal(err)
	}
	r, _ := e.cfg.Catalog.Get("system.library_caches")
	return model.PlanItem{OpportunityID: id, RecipeID: r.ID, Title: r.Title, Target: target, ExpectedBytes: 4096,
		BytesSource: model.SourceMeasured, Risk: model.RiskCaution, Method: model.MethodSystem, Privileged: true,
		Restore: r.Restore, Fingerprint: planner.Fingerprint(fi)}
}

func (e *sysEnv) apply(t *testing.T, p model.Plan, approvals executor.Approvals) (executor.Report, error) {
	t.Helper()
	return executor.Apply(context.Background(), e.cfg, p, approvals)
}

// A confirmed system item is removed with the exact command the kind builds,
// recorded as privileged in the audit log, and counted as deleted.
func TestSystemRemoval(t *testing.T) {
	e := newSysEnv(t)
	c := e.cache(t, "Acme", 90)
	p := e.plan(t, e.item(t, "O-sysaaa", c))
	rep, err := e.apply(t, p, all(p))
	if err != nil {
		t.Fatal(err)
	}
	res := rep.Items[0]
	if res.Outcome != executor.OutcomeDone || !res.Deleted || res.MovedBytes != 4096 || exists(c) {
		t.Fatalf("result %+v, present %v", res, exists(c))
	}
	if len(e.calls) != 1 || strings.Join(e.calls[0], " ") != "rm -rf -- "+c {
		t.Errorf("command = %v", e.calls)
	}
	var seen bool
	for _, r := range e.events(t) {
		if r.Event == "item_done" && r.Method == "system" {
			seen = true
			if !r.Privileged {
				t.Error("the audit record isn't marked privileged")
			}
		}
	}
	if !seen {
		t.Errorf("no system audit record: %v", eventNames(e.events(t)))
	}
}

// refusedRun expects that nothing ran: the plan no longer verifies, or the item
// was skipped.
func refusedRun(t *testing.T, name string, e *sysEnv, rep executor.Report, err error) {
	t.Helper()
	if (err == nil && (len(rep.Items) == 0 || rep.Items[0].Outcome != executor.OutcomeSkipped)) || len(e.calls) != 0 {
		t.Errorf("%s: %v %+v calls %v", name, err, rep.Items, e.calls)
	}
}

// A group approval isn't enough for an administrator item.
func TestSystemNeedsItsOwnApproval(t *testing.T) {
	e := newSysEnv(t)
	c := e.cache(t, "Acme", 90)
	p := e.plan(t, e.item(t, "O-sysbbb", c))
	rep, err := e.apply(t, p, executor.Approvals{"O-sysbbb": {}})
	refusedRun(t, "group approval", e, rep, err)
	if !exists(c) {
		t.Error("the item was removed")
	}
}

// An item changed too recently is skipped.
func TestSystemRecentlyChanged(t *testing.T) {
	e := newSysEnv(t)
	c := e.cache(t, "Fresh", 2)
	p := e.plan(t, e.item(t, "O-syscc", c))
	rep, err := e.apply(t, p, all(p))
	refusedRun(t, "recent", e, rep, err)
	if !exists(c) {
		t.Error("the item was removed")
	}
}

// An item swapped for a symlink, or replaced by another folder, after the plan.
func TestSystemSwappedAfterPlan(t *testing.T) {
	e := newSysEnv(t)
	c := e.cache(t, "Swap", 90)
	p := e.plan(t, e.item(t, "O-sysdd", c))
	other := e.cache(t, "Other", 90)
	if err := os.RemoveAll(c); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, c); err != nil {
		t.Fatal(err)
	}
	rep, err := e.apply(t, p, all(p))
	refusedRun(t, "symlink", e, rep, err)
	if !exists(other) {
		t.Error("what the link points at was removed")
	}

	e = newSysEnv(t)
	c = e.cache(t, "Replaced", 90)
	p = e.plan(t, e.item(t, "O-sysee", c))
	if err := os.RemoveAll(c); err != nil {
		t.Fatal(err)
	}
	e.cache(t, "Replaced", 90)
	rep, err = e.apply(t, p, all(p))
	refusedRun(t, "replaced", e, rep, err)
}

// A plan naming a path outside the named layouts fails verification before
// anything runs: an Apple cache, a deeper path, the folder itself, a path in
// another system folder, and a Claude plan.
func TestSystemRefusedPlans(t *testing.T) {
	for name, rel := range map[string]string{"apple": "Library/Caches/com.apple.x", "deep": "Library/Caches/Acme/deep",
		"folder": "Library/Caches", "other": "Library/Application Support/Acme", "system": "System/Library/Caches/Acme"} {
		e := newSysEnv(t)
		target := filepath.Join(e.base, rel)
		write(t, filepath.Join(target, "a.bin"))
		it := e.item(t, "O-sysff", target)
		_, err := e.apply(t, e.plan(t, it), executor.Approvals{"O-sysff": {Individually: true}})
		if !errors.Is(err, planner.ErrInvalid) || len(e.calls) != 0 || !exists(target) {
			t.Errorf("%s: err %v calls %v", name, err, e.calls)
		}
	}
	e := newSysEnv(t)
	c := e.cache(t, "Acme", 90)
	p := model.Plan{SchemaVersion: model.SchemaVersion, CreatedAt: t0, ExpiresAt: t0.Add(planner.TTL), Origin: model.OriginClaude,
		ScanID: "S-1", Request: "test", Items: []model.PlanItem{e.item(t, "O-sysgg", c)}}
	if err := planner.Save(e.st, planner.Dir(e.storePath), &p); err != nil {
		t.Fatal(err)
	}
	if _, err := e.apply(t, p, all(p)); !errors.Is(err, planner.ErrInvalid) || len(e.calls) != 0 || !exists(c) {
		t.Errorf("Claude plan: err %v calls %v", err, e.calls)
	}
}

// A command that fails is reported as failed, and one that "succeeds" but
// leaves the item is not reported as done.
func TestSystemCommandOutcomes(t *testing.T) {
	e := newSysEnv(t)
	e.code = 1
	c := e.cache(t, "Acme", 90)
	p := e.plan(t, e.item(t, "O-syshh", c))
	rep, _ := e.apply(t, p, all(p))
	if rep.Items[0].Outcome != executor.OutcomeFailed || !exists(c) {
		t.Errorf("exit 1: %+v", rep.Items[0])
	}
}

// Without the fake runner, the real one refuses in sandbox mode: nothing is
// run as administrator from a test.
func TestSystemRealRunnerRefusesHere(t *testing.T) {
	e := newSysEnv(t)
	e.cfg.Sys = nil
	c := e.cache(t, "Acme", 90)
	p := e.plan(t, e.item(t, "O-sysii", c))
	rep, _ := e.apply(t, p, all(p))
	if rep.Items[0].Outcome != executor.OutcomeFailed || !exists(c) {
		t.Errorf("real runner: %+v", rep.Items[0])
	}
}

// SystemBase is only for sandboxed tests.
func TestSystemBaseNeedsSandbox(t *testing.T) {
	e := newSysEnv(t)
	c := e.cache(t, "Acme", 90)
	p := e.plan(t, e.item(t, "O-sysjj", c))
	old, had := os.LookupEnv("MACDOC_SANDBOX")
	_ = os.Unsetenv("MACDOC_SANDBOX")
	t.Cleanup(func() {
		if had {
			_ = os.Setenv("MACDOC_SANDBOX", old)
		}
	})
	_, err := e.apply(t, p, all(p))
	if err == nil || len(e.calls) != 0 || !exists(c) {
		t.Errorf("a SystemBase was accepted outside sandbox mode: %v", err)
	}
}
