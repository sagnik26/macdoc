package tests

import (
	"context"
	"github.com/sagnik26/macdoc/internal/engine"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/platform"
	"github.com/sagnik26/macdoc/internal/recipes"
)

// The Phase 4 rule end to end, with the real catalog: a project with
// uncommitted changes never gets a SAFE artifact suggestion, and a clean idle
// one does.
func TestProjectRecipesEndToEnd(t *testing.T) {
	old := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	dirs := []string{"code/app/android/app/build", "code/app/ios/Pods", "code/app/.git", "code/app/src"}
	f := newFixture(t, time.Hour)
	for _, d := range dirs {
		if err := os.MkdirAll(f.p(d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{"code/app/package.json", "code/app/android/build.gradle", "code/app/ios/Podfile", "code/app/src/index.js"} {
		if err := os.WriteFile(f.p(p), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	_ = filepath.WalkDir(f.p("code"), func(p string, _ fs.DirEntry, _ error) error { return os.Chtimes(p, old, old) }) //nolint:gosec // G122: test fixture in a temp dir

	// Replace the fixture's scan with one that kept the artifacts.
	f = newFixtureAt(t, f.home, model.Node{Path: f.home, Kind: model.NodeDir, Bytes: 1 << 30},
		model.Node{Path: f.p("code/app/android/app/build"), Kind: model.NodeDir, Bytes: 400_000_000, MtimeMax: old.Unix(), Depth: 5},
		model.Node{Path: f.p("code/app/ios/Pods"), Kind: model.NodeDir, Bytes: 300_000_000, MtimeMax: old.Unix(), Depth: 4},
	)
	cat, err := recipes.Load()
	if err != nil {
		t.Fatal(err)
	}
	run := func(status string) map[string]model.Opportunity {
		engine.ProjectCache.Lock()
		engine.ProjectCache.Key = ""
		engine.ProjectCache.Unlock()
		base := "git -C " + f.p("code/app") + " --no-pager --no-optional-locks -c core.fsmonitor=false -c core.untrackedCache=false "
		r := &platform.FakeRunner{Responses: map[string]platform.Result{
			base + "status --porcelain --untracked-files=normal -- .": {Stdout: []byte(status)},
			base + "log -1 --format=%ct -- .":                         {Stdout: []byte(strconv.FormatInt(old.Unix(), 10))},
		}}
		o := f.opts()
		o.Catalog, o.Runner = cat, r
		res, err := engine.Opportunities(context.Background(), o)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]model.Opportunity{}
		for _, op := range res.Opportunities {
			out[op.RecipeID] = op
		}
		return out
	}

	clean := run("")
	if op := clean["rn.native_build_stale"]; op.Risk != model.RiskSafe || op.Target != f.p("code/app/android/app/build") || op.Title != "android/app/build in app" {
		t.Errorf("clean native build: %+v", op)
	}
	if op := clean["rn.ios_pods_stale"]; op.Risk != model.RiskCaution {
		t.Errorf("clean pods: %+v", op)
	}

	dirty := run(" M src/index.js\n")
	if op := dirty["rn.native_build_stale"]; op.Risk != model.RiskCaution || op.RiskReason != "the project has uncommitted changes" {
		t.Errorf("dirty native build: risk %s (%q)", op.Risk, op.RiskReason)
	}
	if op := dirty["rn.ios_pods_stale"]; op.Risk != model.RiskRisky {
		t.Errorf("dirty pods: risk %s", op.Risk)
	}
	if base, raised := engine.RaisedFrom(dirty["rn.native_build_stale"], cat); !raised || base != model.RiskSafe {
		t.Errorf("RaisedFrom = %s %v", base, raised)
	}
}
