package tests

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sagnik26/macdoc/internal/audit"
	"github.com/sagnik26/macdoc/internal/config"
	"github.com/sagnik26/macdoc/internal/executor"
	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/planner"
	"github.com/sagnik26/macdoc/internal/quarantine"
	"github.com/sagnik26/macdoc/internal/sandbox"
)

// finished returns the apply_finished record, the last one in the log.
func finished(t *testing.T, e *env) audit.Record {
	t.Helper()
	ev := e.events(t)
	last := ev[len(ev)-1]
	if last.Event != audit.Finished {
		t.Fatalf("last audit event = %s", last.Event)
	}
	return last
}

// The closing audit record states how many items there were, how much they
// were expected to free and how it went.
func TestApplyFinishedRecord(t *testing.T) { //nolint:gocyclo // A linear list of assertions about one scenario.
	withTools(t, "brew")
	t.Run("ok", func(t *testing.T) {
		e := newEnv(t)
		p := e.plan(t,
			e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, e.folder(t, "Library/Logs/A")),
			e.native(t, "O-bbbbbb", "brew.cleanup", model.RiskSafe, "brew", "cleanup", "--prune=all"))
		rep := mustApply(t, e, p)
		if rep.Count(executor.OutcomeDone) != 2 {
			t.Fatalf("report = %+v", rep.Items)
		}
		fin := finished(t, e)
		if fin.Result != "ok" || fin.Items != 2 || fin.Expected != 8192+1<<20 || fin.Plan != p.ID || fin.Origin != string(model.OriginCLI) {
			t.Errorf("finish = %+v", fin)
		}
		if fin.Actual != nil {
			t.Errorf("actual bytes %d recorded without a free-space measure", *fin.Actual)
		}
	})
	t.Run("partial", func(t *testing.T) {
		e := newEnv(t)
		a := e.folder(t, "Library/Logs/A")
		b := e.folder(t, "Library/Logs/B")
		p := e.plan(t,
			e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, a),
			e.item(t, "O-bbbbbb", "user.logs", model.RiskSafe, b))
		rep, err := executor.Apply(context.Background(), e.cfg, p, executor.Approvals{"O-aaaaaa": {Individually: true}})
		if err != nil {
			t.Fatal(err)
		}
		if rep.Items[1].Outcome != executor.OutcomeSkipped || rep.Items[1].Reason != "not approved" || !exists(b) || exists(a) {
			t.Fatalf("report = %+v", rep.Items)
		}
		if fin := finished(t, e); fin.Result != "partial" || fin.Items != 2 || fin.Expected != 8192 {
			t.Errorf("finish = %+v", fin)
		}
	})
	t.Run("deleted at once", func(t *testing.T) {
		e := newEnv(t)
		e.cfg.DeleteNow = true
		p := e.plan(t, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, e.folder(t, "Library/Logs/A")))
		if rep := mustApply(t, e, p); !rep.Items[0].Deleted {
			t.Fatalf("report = %+v", rep.Items)
		}
		if fin := finished(t, e); fin.Result != "ok" || fin.Expected != 8192 {
			t.Errorf("finish = %+v", fin)
		}
	})
}

// Free space is measured on both sides of the apply and recorded.
func TestApplyMeasuresFreeSpace(t *testing.T) {
	e := newEnv(t)
	calls := 0
	e.cfg.FreeSpace = func(context.Context) (int64, error) {
		calls++
		return int64(calls) * 1000, nil
	}
	p := e.plan(t, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, e.folder(t, "Library/Logs/A")))
	rep := mustApply(t, e, p)
	if rep.FreeBefore == nil || rep.FreeAfter == nil || *rep.FreeBefore != 1000 || *rep.FreeAfter != 2000 {
		t.Fatalf("free space = %v, %v", rep.FreeBefore, rep.FreeAfter)
	}
	if n, ok := rep.Freed(); !ok || n != 1000 {
		t.Errorf("freed = %d, %v", n, ok)
	}
	if fin := finished(t, e); fin.Actual == nil || *fin.Actual != 1000 {
		t.Errorf("finish = %+v", fin)
	}

	e2 := newEnv(t)
	e2.cfg.FreeSpace = func(context.Context) (int64, error) { return 0, errors.New("can't measure") }
	p2 := e2.plan(t, e2.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, e2.folder(t, "Library/Logs/A")))
	rep2 := mustApply(t, e2, p2)
	if rep2.FreeBefore != nil || rep2.FreeAfter != nil {
		t.Errorf("unknown free space reported as %v, %v", rep2.FreeBefore, rep2.FreeAfter)
	}
	if _, ok := rep2.Freed(); ok {
		t.Error("freed reported without a measure")
	}
	if fin := finished(t, e2); fin.Actual != nil {
		t.Errorf("unknown actual bytes recorded as %d", *fin.Actual)
	}
}

// Every early refusal changes nothing: no move, no audit record, plan still
// pending.
func TestApplyEarlyRefusals(t *testing.T) { //nolint:gocognit,gocyclo // A linear list of assertions about one scenario.
	t.Run("config incomplete", func(t *testing.T) {
		for name, mutate := range map[string]func(c *executor.Config){
			"no store": func(c *executor.Config) { c.Store = nil },
			"no path":  func(c *executor.Config) { c.StorePath = "" },
			"no home":  func(c *executor.Config) { c.Home = "" },
		} {
			e := newEnv(t)
			target := e.folder(t, "Library/Logs/A")
			p := e.plan(t, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, target))
			cfg := e.cfg
			mutate(&cfg)
			if _, err := executor.Apply(context.Background(), cfg, p, all(p)); err == nil || !strings.Contains(err.Error(), "required") {
				t.Errorf("%s: err = %v", name, err)
			}
			if !exists(target) || len(e.events(t)) != 0 {
				t.Errorf("%s: something changed", name)
			}
		}
	})
	t.Run("outside the sandbox", func(t *testing.T) {
		e := newEnv(t)
		target := e.folder(t, "Library/Logs/A")
		p := e.plan(t, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, target))
		t.Setenv(sandbox.Env, t.TempDir())
		_, err := executor.Apply(context.Background(), e.cfg, p, all(p))
		if err == nil || !strings.HasPrefix(err.Error(), "nothing was changed: ") {
			t.Fatalf("err = %v", err)
		}
		if !exists(target) {
			t.Fatal("moved")
		}
		if rec, _ := e.st.Plan(p.ID); rec.Status == "applied" {
			t.Error("the plan was locked by a refused apply")
		}
	})
	t.Run("plan not in the store", func(t *testing.T) {
		e := newEnv(t)
		target := e.folder(t, "Library/Logs/A")
		p := e.plan(t, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, target))
		p.ID = "P-ZZZZ"
		if _, err := executor.Apply(context.Background(), e.cfg, p, all(p)); !errors.Is(err, planner.ErrUnknown) {
			t.Fatalf("err = %v, want ErrUnknown", err)
		}
		if !exists(target) || len(e.events(t)) != 0 {
			t.Fatal("something changed")
		}
	})
	t.Run("store unavailable", func(t *testing.T) {
		e := newEnv(t)
		target := e.folder(t, "Library/Logs/A")
		p := e.plan(t, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, target))
		if err := e.st.Close(); err != nil {
			t.Fatal(err)
		}
		_, err := executor.Apply(context.Background(), e.cfg, p, all(p))
		if err == nil || errors.Is(err, planner.ErrUnknown) || errors.Is(err, planner.ErrTampered) {
			t.Fatalf("err = %v, want the store's own error", err)
		}
		if !exists(target) {
			t.Fatal("moved")
		}
	})
}

// One Enter over the SAFE and CAUTION items of a plan macdoc made is enough;
// only a Claude plan or a RISKY item needs its own confirmation.
func TestGroupApprovalCoversOrdinaryItems(t *testing.T) {
	e := newEnv(t)
	safe := e.folder(t, "Library/Logs/A")
	caution := e.folder(t, "Library/Caches/com.example.app")
	p := e.plan(t,
		e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, safe),
		e.item(t, "O-bbbbbb", "user.app_caches", model.RiskCaution, caution))
	rep, err := executor.Apply(context.Background(), e.cfg, p, executor.Approvals{
		"O-aaaaaa": {Individually: false}, "O-bbbbbb": {Individually: false}})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Count(executor.OutcomeDone) != 2 || exists(safe) || exists(caution) {
		t.Fatalf("report = %+v", rep.Items)
	}
}

// Without a catalog in the config the executor loads the built-in one.
func TestApplyLoadsTheCatalogByDefault(t *testing.T) {
	e := newEnv(t)
	target := e.folder(t, "Library/Logs/A")
	p := e.plan(t, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, target))
	e.cfg.Catalog = nil
	rep, err := executor.Apply(context.Background(), e.cfg, p, all(p))
	if err != nil || rep.Items[0].Outcome != executor.OutcomeDone || exists(target) {
		t.Fatalf("Apply = %+v, %v", rep.Items, err)
	}
}

// Without a clock in the config the executor uses the real one.
func TestApplyDefaultsToTheRealClock(t *testing.T) {
	e := newEnv(t)
	e.cfg.Now = nil
	target := e.folder(t, "Library/Logs/A")
	now := time.Now()
	p := e.plan(t, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, target))
	// Re-seal the plan around the real time, so it hasn't expired.
	p.CreatedAt, p.ExpiresAt, p.SHA256 = now, now.Add(planner.DefaultTTL), ""
	if err := planner.Save(e.st, planner.Dir(e.storePath), &p); err != nil {
		t.Fatal(err)
	}
	rep, err := executor.Apply(context.Background(), e.cfg, p, all(p))
	if err != nil || rep.Items[0].Outcome != executor.OutcomeDone {
		t.Fatalf("Apply = %+v, %v", rep.Items, err)
	}
	m, err := quarantine.Load(quarantine.Root(e.storePath), rep.Items[0].QuarantineID)
	if err != nil {
		t.Fatal(err)
	}
	if d := m.Created.Sub(now); d < -time.Minute || d > time.Minute {
		t.Errorf("manifest created %v, want about %v", m.Created, now)
	}
}

// A plan that is applied makes macdoc's state folder private.
func TestApplyTightensTheStateFolder(t *testing.T) {
	e := newEnv(t)
	dir := filepath.Dir(e.storePath)
	if err := os.Chmod(dir, 0o755); err != nil { //nolint:gosec // G302: a loose fixture folder.
		t.Fatal(err)
	}
	mustApply(t, e, e.plan(t, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, e.folder(t, "Library/Logs/A"))))
	if fi, err := os.Stat(dir); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("state folder = %v, %v", fi, err)
	}
}

// A state folder that is a file, or a symlink, stops the apply before any
// move; the plan stays pending.
func TestApplyRefusesABadStateFolder(t *testing.T) {
	e := newEnv(t)
	target := e.folder(t, "Library/Logs/A")
	p := e.plan(t, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, target))
	dir := filepath.Dir(e.storePath)
	moved := dir + ".moved"
	mustRename(t, dir, moved)
	mustSymlink(t, moved, dir)
	if _, err := executor.Apply(context.Background(), e.cfg, p, all(p)); err == nil {
		t.Fatal("apply ran with a symlinked state folder")
	}
	if !exists(target) {
		t.Fatal("moved")
	}
	if ents, _ := os.ReadDir(filepath.Join(moved, quarantine.Dir)); len(ents) != 0 {
		t.Fatalf("quarantine written through the symlink: %v", ents)
	}
}

// Native commands get the timeout from settings, a default of 15 minutes, or
// the one in the config.
func TestNativeTimeout(t *testing.T) {
	withTools(t, "brew")
	run := func(t *testing.T, e *env) time.Duration {
		t.Helper()
		var got time.Duration
		e.cfg.Exec = func(_ context.Context, _ []string, d time.Duration) ([]byte, int, error) {
			got = d
			return nil, 0, nil
		}
		p := e.plan(t, e.native(t, "O-aaaaaa", "brew.cleanup", model.RiskSafe, "brew", "cleanup", "--prune=all"))
		if rep := mustApply(t, e, p); rep.Items[0].Outcome != executor.OutcomeDone {
			t.Fatalf("report = %+v", rep.Items)
		}
		return got
	}
	if got := run(t, newEnv(t)); got != 15*time.Minute {
		t.Errorf("default timeout = %v", got)
	}
	e := newEnv(t)
	var c config.Config
	if err := c.Set("native_timeout_minutes", 3); err != nil {
		t.Fatal(err)
	}
	if err := config.Save(e.home, c); err != nil {
		t.Fatal(err)
	}
	if got := run(t, e); got != 3*time.Minute {
		t.Errorf("configured timeout = %v", got)
	}
	e = newEnv(t)
	e.cfg.NativeTimeout = 7 * time.Second
	if got := run(t, e); got != 7*time.Second {
		t.Errorf("explicit timeout = %v", got)
	}
}
