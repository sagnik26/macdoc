package tests

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/sagnik26/macdoc/internal/config"
	"github.com/sagnik26/macdoc/internal/executor"
	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/platform"
)

// An app in ~/Applications is moved aside only when it is not running, and
// only with its own confirmation (it is RISKY). The fixture is a folder in the
// sandbox; nothing real is touched.
func TestUserAppBundleMovedOnlyWhenClosed(t *testing.T) {
	for name, c := range map[string]struct {
		ps   string
		gone bool
	}{
		"closed":  {"/sbin/launchd\n", true},
		"running": {"", false},
	} {
		e := newEnv(t)
		app := e.folder(t, "Applications/Mine.app", "Contents/MacOS/Mine")
		ps := c.ps
		if !c.gone {
			ps = "/sbin/launchd\n" + app + "/Contents/MacOS/Mine\n"
		}
		e.runner.Responses["ps -axww -o command="] = platform.Result{Stdout: []byte(ps)}
		it := e.item(t, "O-appaaa", "apps.user_bundle", model.RiskRisky, app)
		p := e.plan(t, it)
		rep := mustApply(t, e, p)
		moved := !exists(app)
		if moved != c.gone {
			t.Errorf("%s: moved=%v outcome=%v (%s), want moved=%v", name, moved, rep.Items[0].Outcome, rep.Items[0].Reason, c.gone)
		}
		if !c.gone && rep.Items[0].Outcome != executor.OutcomeSkipped {
			t.Errorf("%s: outcome = %v, want skipped", name, rep.Items[0].Outcome)
		}
	}
}

// A recipe turned off in settings after a plan was made stops the whole apply
// before anything changes, and settings can only subtract: a look-alike
// recipe ID that is not excluded still runs.
func TestApplyRefusesAnExcludedRecipe(t *testing.T) {
	for name, c := range map[string]struct {
		excluded string
		moved    bool
	}{
		"excluded":   {`"apps.user_bundle"`, false},
		"prefix":     {`"apps.*"`, false},
		"look-alike": {`"apps.user_bundle_x"`, true},
	} {
		e := newEnv(t)
		app := e.folder(t, "Applications/Mine.app", "Contents/MacOS/Mine")
		cfg := config.Path(e.home)
		if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(cfg, []byte(`{"excluded_recipes":[`+c.excluded+`]}`), 0o600); err != nil {
			t.Fatal(err)
		}
		p := e.plan(t, e.item(t, "O-appaaa", "apps.user_bundle", model.RiskRisky, app))
		_, err := executor.Apply(context.Background(), e.cfg, p, all(p))
		if c.moved == errors.Is(err, executor.ErrExcluded) || c.moved != !exists(app) {
			t.Errorf("%s: err %v, app moved %v; want moved=%v", name, err, !exists(app), c.moved)
		}
	}
}

// ADR 0011 end to end in the sandbox: a bundle in a fixture standing in for
// /Applications is moved with its own confirmation, can be restored, and a
// different folder with the same recipe is refused. Nothing real is touched.
func TestSystemAppBundleMoveAndRestore(t *testing.T) {
	e := newEnv(t)
	apps := filepath.Join(e.home, "FakeApplications")
	if err := os.MkdirAll(apps, 0o755); err != nil {
		t.Fatal(err)
	}
	config.AppsFolder = apps
	t.Cleanup(func() { config.AppsFolder = "" })
	app := filepath.Join(apps, "Mine.app")
	if err := os.MkdirAll(filepath.Join(app, "Contents", "MacOS"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app, "Contents", "MacOS", "Mine"), []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	it := e.item(t, "O-sysapp", "apps.system_bundle", model.RiskRisky, app)

	// A group approval is not enough for a RISKY item.
	p := e.plan(t, it)
	rep, err := executor.Apply(context.Background(), e.cfg, p, executor.Approvals{it.OpportunityID: {}})
	if err != nil || rep.Items[0].Outcome == executor.OutcomeDone || !exists(app) {
		t.Fatalf("group approval moved the app: %v %v", rep.Items, err)
	}

	e.cfg.DeleteNow = false // keep it in quarantine so it can be restored
	p2 := e.plan(t, it)
	rep = mustApply(t, e, p2)
	if rep.Items[0].Outcome != executor.OutcomeDone || exists(app) {
		t.Fatalf("outcome %v (%s), app still there %v", rep.Items[0].Outcome, rep.Items[0].Reason, exists(app))
	}
	if _, err := executor.Restore(context.Background(), e.cfg, rep.Items[0].QuarantineID); err != nil || !exists(app) {
		t.Fatalf("restore: %v, app back %v", err, exists(app))
	}

	// The same recipe never reaches a folder that isn't the apps folder.
	other := e.folder(t, "Elsewhere/Other.app", "Contents/x")
	bad := e.item(t, "O-other", "apps.system_bundle", model.RiskRisky, other)
	if refused := applyWith(e, e.plan(t, bad), executor.Approvals{bad.OpportunityID: {Individually: true}}); !refused || !exists(other) {
		t.Errorf("a bundle outside the apps folder was moved (refused=%v)", refused)
	}
}
