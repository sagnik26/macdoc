package tests

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/sagnik26/macdoc/internal/executor"

	"github.com/sagnik26/macdoc/internal/audit"
	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/planner"
	"github.com/sagnik26/macdoc/internal/platform"
	"github.com/sagnik26/macdoc/internal/quarantine"
	"github.com/sagnik26/macdoc/internal/recipes"
	"github.com/sagnik26/macdoc/internal/store"
)

// The crash test re-runs this test binary as a child that applies a plan
// and dies (os.Exit, like a kill: no deferred cleanup) at a chosen step of
// the move. The parent then checks the audit log and quarantine.
const (
	crashEnv   = "MACDOC_TEST_CRASH_AT"
	crashHome  = "MACDOC_TEST_CRASH_HOME"
	crashPlan  = "MACDOC_TEST_CRASH_PLAN"
	crashCode  = 137
	crashStore = "MACDOC_TEST_CRASH_STORE"
)

func crashChild() {
	stage, home, sp, id := os.Getenv(crashEnv), os.Getenv(crashHome), os.Getenv(crashStore), os.Getenv(crashPlan)
	st, err := store.Open(sp)
	if err != nil {
		os.Exit(3)
	}
	p, err := planner.Load(planner.Dir(sp), id)
	if err != nil {
		os.Exit(4)
	}
	cat, err := recipes.Load()
	if err != nil {
		os.Exit(5)
	}
	stageHook := func(s string) {
		if s == stage {
			os.Exit(crashCode)
		}
	}
	runner := &platform.Fake{Responses: map[string]platform.Result{"ps -axww -o command=": {Stdout: []byte("/sbin/launchd\n")}}}
	cfg := executor.Config{Store: st, StorePath: sp, Home: home, Catalog: cat, Now: func() time.Time { return t0.Add(time.Minute) },
		Runner: runner, Exec: (&fakeExec{}).run, StageHook: stageHook}
	_, _ = executor.Apply(context.Background(), cfg, p, executor.Approvals{p.Items[0].OpportunityID: {Individually: true}})
	os.Exit(0)
}

// Killing the process mid-apply leaves a consistent audit log and a
// recoverable quarantine (Phase 6 done-when).
func TestCrashMidApply(t *testing.T) {
	for _, c := range []struct {
		stage      string
		state      quarantine.State
		movedAway  bool
		restorable bool
	}{
		{"pending", quarantine.Unmoved, false, false},
		{"moved", quarantine.Interrupted, true, true},
		{"done", quarantine.Interrupted, true, true},
	} {
		t.Run(c.stage, func(t *testing.T) {
			e := newEnv(t)
			target := e.folder(t, "Library/Logs/A", "a.log", "deep/b.log")
			p := e.plan(t, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, target))
			_ = e.st.Close() // the child opens it

			cmd := exec.Command(os.Args[0], "-test.run=^$") //nolint:gosec // G204: re-runs this test binary.
			cmd.Env = append(os.Environ(), crashEnv+"="+c.stage, crashHome+"="+e.home, crashStore+"="+e.storePath, crashPlan+"="+p.ID)
			err := cmd.Run()
			var ee *exec.ExitError
			if !errors.As(err, &ee) || ee.ExitCode() != crashCode {
				t.Fatalf("child = %v, want exit %d", err, crashCode)
			}

			st, err := store.Open(e.storePath)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = st.Close() }()
			e.st, e.cfg.Store = st, st

			// The audit log parses cleanly: started, no finish.
			if got := eventNames(e.events(t)); len(got) != 1 || got[0] != audit.Started {
				t.Errorf("audit after crash = %v", got)
			}
			// The plan can't be applied again.
			if rec, _ := st.Plan(p.ID); rec.Status != store.PlanApplied {
				t.Errorf("plan status %s", rec.Status)
			}
			if _, err := executor.Apply(context.Background(), e.cfg, p, all(p)); !errors.Is(err, planner.ErrApplied) {
				t.Errorf("re-apply after crash = %v", err)
			}

			root := quarantine.Root(e.storePath)
			l, err := quarantine.List(st, root, e.cfg.Now())
			if err != nil || len(l.Entries) != 1 {
				t.Fatalf("listing = %+v, %v", l, err)
			}
			entry := l.Entries[0]
			if entry.State != c.state || entry.Restorable() != c.restorable {
				t.Fatalf("entry state %s (restorable %v), want %s", entry.State, entry.Restorable(), c.state)
			}
			if exists(target) == c.movedAway {
				t.Fatalf("target exists = %v after crash at %s", exists(target), c.stage)
			}
			if c.restorable {
				if _, err := executor.Restore(context.Background(), e.cfg, entry.Record.ID); err != nil {
					t.Fatal(err)
				}
				if !exists(filepath.Join(target, "deep", "b.log")) {
					t.Fatal("restore after crash lost data")
				}
				return
			}
			// Not moved: purging just tidies the empty entry; the original
			// was never touched.
			res, _ := executor.Purge(context.Background(), e.cfg, []string{entry.Record.ID})
			if res[0].Outcome != executor.OutcomeDone || res[0].Bytes != 0 || !exists(filepath.Join(target, "deep", "b.log")) {
				t.Fatalf("tidy = %+v", res[0])
			}
		})
	}
}
