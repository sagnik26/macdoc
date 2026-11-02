package executor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"macdoc/internal/audit"
	"macdoc/internal/model"
	"macdoc/internal/planner"
	"macdoc/internal/platform"
	"macdoc/internal/quarantine"
	"macdoc/internal/recipes"
	"macdoc/internal/sandbox"
	"macdoc/internal/store"
)

// Every executor test runs inside MACDOC_SANDBOX (docs/DEVELOPMENT_PLAN.md
// Phase 6): TestMain creates one if it isn't set, and refuses to run with an
// invalid one. Fixture homes are folders inside it.
func TestMain(m *testing.M) {
	if os.Getenv(crashEnv) != "" {
		crashChild()
		return
	}
	made := ""
	if _, set := os.LookupEnv(sandbox.EnvVar); !set {
		d, err := os.MkdirTemp("", "macdoc-executor-sandbox-")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		made = d
		_ = os.Setenv(sandbox.EnvVar, d)
	}
	if _, active, err := sandbox.Root(); !active || err != nil {
		fmt.Fprintln(os.Stderr, "executor tests need a valid MACDOC_SANDBOX:", err)
		os.Exit(2)
	}
	code := m.Run()
	if made != "" {
		_ = os.RemoveAll(made)
	}
	os.Exit(code)
}

var t0 = time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)

// env is one fixture: a home inside the sandbox, its store and a config.
type env struct {
	home, storePath string
	st              *store.Store
	cfg             Config
	runner          *platform.FakeRunner
	exec            *fakeExec
}

type fakeExec struct {
	mu    sync.Mutex
	calls [][]string
	code  int
	out   string
}

func (f *fakeExec) run(_ context.Context, argv []string, _ time.Duration) ([]byte, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, argv)
	return []byte(f.out), f.code, nil
}

func newEnv(t *testing.T) *env {
	t.Helper()
	root, _, err := sandbox.Root()
	if err != nil {
		t.Fatal(err)
	}
	home, err := os.MkdirTemp(root, "home-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	sp := store.DefaultPath(home)
	st, err := store.Open(sp)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	cat, err := recipes.Load()
	if err != nil {
		t.Fatal(err)
	}
	e := &env{home: home, storePath: sp, st: st, exec: &fakeExec{},
		runner: &platform.FakeRunner{Responses: map[string]platform.Result{"ps -axww -o command=": {Stdout: []byte("/sbin/launchd\n")}}}}
	e.cfg = Config{Store: st, StorePath: sp, Home: home, Catalog: cat, Runner: e.runner, Exec: e.exec.run,
		Now: func() time.Time { return t0.Add(time.Minute) }}
	return e
}

// mkdir creates home-relative folders with a file in each, dated long ago.
func (e *env) folder(t *testing.T, rel string, files ...string) string {
	t.Helper()
	p := filepath.Join(e.home, rel)
	if len(files) == 0 {
		files = []string{"a.log"}
	}
	for _, f := range files {
		fp := filepath.Join(p, f)
		if err := os.MkdirAll(filepath.Dir(fp), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fp, []byte(strings.Repeat("x", 5000)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	old := t0.Add(-60 * 24 * time.Hour)
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatal(err)
	}
	return p
}

// item is a quarantine plan item for target, fingerprinted now.
func (e *env) item(t *testing.T, id, recipe string, risk model.Risk, target string) model.PlanItem {
	t.Helper()
	fi, err := os.Lstat(target)
	if err != nil {
		t.Fatal(err)
	}
	r, ok := e.cfg.Catalog.Get(recipe)
	if !ok {
		t.Fatalf("no recipe %s", recipe)
	}
	return model.PlanItem{OpportunityID: id, RecipeID: recipe, Title: r.Title, Target: target,
		ExpectedBytes: 8192, BytesSource: model.SourceMeasured, Risk: risk, Method: model.MethodQuarantine,
		Preconditions: r.Preconditions, Restore: r.Restore, Fingerprint: planner.Fingerprint(fi)}
}

func (e *env) native(t *testing.T, id, recipe string, risk model.Risk, argv ...string) model.PlanItem {
	t.Helper()
	r, ok := e.cfg.Catalog.Get(recipe)
	if !ok {
		t.Fatalf("no recipe %s", recipe)
	}
	return model.PlanItem{OpportunityID: id, RecipeID: recipe, Title: r.Title, ExpectedBytes: 1 << 20,
		BytesSource: model.SourceTool, Risk: risk, Method: model.MethodNative, Argv: argv,
		Preconditions: r.Preconditions, Restore: r.Restore}
}

// plan seals and saves a plan of items.
func (e *env) plan(t *testing.T, items ...model.PlanItem) model.Plan {
	t.Helper()
	p := model.Plan{SchemaVersion: model.SchemaVersion, CreatedAt: t0, ExpiresAt: t0.Add(planner.DefaultTTL),
		Origin: model.OriginCLI, ScanID: "S-1", Request: "test", Items: items}
	for _, it := range items {
		p.TotalBytes += it.ExpectedBytes
	}
	if err := planner.Save(e.st, planner.Dir(e.storePath), &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func all(p model.Plan) map[string]bool {
	m := map[string]bool{}
	for _, it := range p.Items {
		m[it.OpportunityID] = true
	}
	return m
}

func (e *env) events(t *testing.T) []audit.Record {
	t.Helper()
	recs, bad, err := audit.Read(audit.Path(e.storePath), 0)
	if err != nil || bad != 0 {
		t.Fatalf("audit log: %d bad lines, %v", bad, err)
	}
	return recs
}

func eventNames(recs []audit.Record) []string {
	out := make([]string, len(recs))
	for i, r := range recs {
		out[i] = r.Event
	}
	return out
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

func mustApply(t *testing.T, e *env, p model.Plan) Report {
	t.Helper()
	rep, err := Apply(context.Background(), e.cfg, p, all(p))
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func TestApplyQuarantineAndRestore(t *testing.T) {
	e := newEnv(t)
	target := e.folder(t, "Library/Logs/OldApp", "a.log", "sub/b.log")
	p := e.plan(t, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, target))
	rep := mustApply(t, e, p)

	if len(rep.Items) != 1 || rep.Items[0].Outcome != OutcomeDone || rep.Items[0].QuarantineID == "" {
		t.Fatalf("report = %+v", rep.Items)
	}
	if exists(target) {
		t.Fatal("target still there after apply")
	}
	qid := rep.Items[0].QuarantineID
	root := quarantine.Root(e.storePath)
	if !exists(filepath.Join(root, qid, "payload", "OldApp", "sub", "b.log")) {
		t.Fatal("payload missing")
	}
	if !exists(filepath.Join(root, ".metadata_never_index")) {
		t.Error("quarantine folder not excluded from Spotlight")
	}
	if fi, _ := os.Stat(root); fi.Mode().Perm() != 0o700 {
		t.Errorf("quarantine folder mode %v", fi.Mode().Perm())
	}
	if got := eventNames(e.events(t)); !slices.Equal(got, []string{audit.ApplyStarted, audit.ItemDone, audit.ApplyFinished}) {
		t.Errorf("audit events = %v", got)
	}
	if rec, _ := e.st.Plan(p.ID); rec.Status != store.PlanApplied {
		t.Errorf("plan status %s", rec.Status)
	}
	l, err := quarantine.List(e.st, root, e.cfg.Now())
	if err != nil || len(l.Entries) != 1 || l.Entries[0].State != quarantine.StateHeld || len(l.Unknown) != 0 {
		t.Fatalf("listing = %+v, %v", l, err)
	}
	if len(e.exec.calls) != 0 {
		t.Errorf("commands ran in sandbox mode: %v", e.exec.calls)
	}

	r, err := Restore(context.Background(), e.cfg, qid)
	if err != nil || r.Outcome != OutcomeDone {
		t.Fatalf("Restore = %+v, %v", r, err)
	}
	if !exists(filepath.Join(target, "sub", "b.log")) || exists(filepath.Join(root, qid)) {
		t.Fatal("restore didn't put it back and tidy up")
	}
	if rec, _ := e.st.Quarantine(qid); rec.Status != store.QuarantineRestored {
		t.Errorf("entry status %s", rec.Status)
	}
	if ev := e.events(t); ev[len(ev)-1].Event != audit.QuarantineRestored {
		t.Errorf("last event %s", ev[len(ev)-1].Event)
	}
}

func TestPlanAppliesOnce(t *testing.T) {
	e := newEnv(t)
	p := e.plan(t, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, e.folder(t, "Library/Logs/A")))
	mustApply(t, e, p)
	if _, err := Apply(context.Background(), e.cfg, p, all(p)); !errors.Is(err, planner.ErrApplied) {
		t.Fatalf("second apply = %v, want ErrApplied", err)
	}
}

// The lock holds against two applies racing.
func TestConcurrentApplyLocks(t *testing.T) {
	e := newEnv(t)
	p := e.plan(t, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, e.folder(t, "Library/Logs/A")))
	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = Apply(context.Background(), e.cfg, p, all(p))
		}()
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case !errors.Is(err, planner.ErrApplied):
			t.Errorf("unexpected error %v", err)
		}
	}
	if ok != 1 {
		t.Fatalf("%d applies ran, want exactly 1", ok)
	}
}

func TestRefusedPlansChangeNothing(t *testing.T) {
	cases := map[string]func(e *env, p *model.Plan){
		"edited target": func(e *env, p *model.Plan) { p.Items[0].Target = filepath.Join(e.home, "Library/Logs/Other") },
		"expired":       func(e *env, _ *model.Plan) { e.cfg.Now = func() time.Time { return t0.Add(time.Hour) } },
		"edited risk":   func(_ *env, p *model.Plan) { p.Items[0].Risk = model.RiskCaution },
		"unknown plan":  func(_ *env, p *model.Plan) { p.ID = "P-ZZZZ" },
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			target := e.folder(t, "Library/Logs/A")
			p := e.plan(t, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, target))
			id := p.ID
			edit(e, &p)
			if _, err := Apply(context.Background(), e.cfg, p, all(p)); err == nil {
				t.Fatal("apply succeeded")
			}
			if !exists(target) {
				t.Fatal("target moved")
			}
			if rec, _ := e.st.Plan(id); rec.Status != store.PlanPending {
				t.Errorf("plan status %s, want pending", rec.Status)
			}
			if len(e.events(t)) != 0 {
				t.Errorf("audit written for a refused plan: %v", eventNames(e.events(t)))
			}
		})
	}
}

func TestNoApprovalNoChange(t *testing.T) {
	e := newEnv(t)
	target := e.folder(t, "Library/Logs/A")
	p := e.plan(t, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, target))
	if _, err := Apply(context.Background(), e.cfg, p, nil); !errors.Is(err, ErrNoApproval) {
		t.Fatalf("err = %v", err)
	}
	rep, err := Apply(context.Background(), e.cfg, p, map[string]bool{"O-other": true})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Items[0].Outcome != OutcomeSkipped || rep.Items[0].Reason != "not approved" || !exists(target) {
		t.Fatalf("unapproved item = %+v", rep.Items[0])
	}
}

// A symlink swapped in between plan and apply is detected and skipped, and
// what it points to is untouched (Phase 6 done-when).
func TestSwapsBetweenPlanAndApply(t *testing.T) {
	cases := map[string]func(t *testing.T, e *env, target string){
		"target becomes a symlink": func(t *testing.T, e *env, target string) {
			victim := e.folder(t, "Documents/Thesis", "chapter1.md")
			mustRename(t, target, target+".moved")
			mustSymlink(t, victim, target)
		},
		"parent becomes a symlink": func(t *testing.T, e *env, target string) {
			e.folder(t, "Documents/Logs/A", "keep.md")
			logs := filepath.Dir(target)
			mustRename(t, logs, logs+".moved")
			mustSymlink(t, filepath.Join(e.home, "Documents/Logs"), logs)
		},
		"parent becomes a symlink to an allowed folder": func(t *testing.T, e *env, target string) {
			e.folder(t, "Library/Logs/Elsewhere/A", "other.log")
			logs := filepath.Dir(target)
			mustRename(t, logs, logs+".moved")
			mustSymlink(t, logs+".moved/Elsewhere", logs)
		},
		"replaced by another folder": func(t *testing.T, e *env, target string) {
			mustRename(t, target, target+".moved")
			e.folder(t, "Library/Logs/A", "new.log")
		},
		"deleted": func(t *testing.T, _ *env, target string) {
			mustRename(t, target, target+".moved")
		},
	}
	for name, swap := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			target := e.folder(t, "Library/Logs/A")
			p := e.plan(t, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, target))
			swap(t, e, target)
			rep, err := Apply(context.Background(), e.cfg, p, all(p))
			if err != nil {
				// A parent turned into a symlink into a protected folder
				// fails the fresh policy in Verify: the whole plan is
				// refused before anything changes.
				if !errors.Is(err, planner.ErrInvalid) || len(e.events(t)) != 0 {
					t.Fatalf("apply = %v", err)
				}
				return
			}
			if it := rep.Items[0]; it.Outcome != OutcomeSkipped || it.QuarantineID != "" {
				t.Fatalf("item = %+v, want skipped", it)
			}
			if exists(filepath.Join(e.home, "Documents")) {
				if !exists(filepath.Join(e.home, "Documents/Thesis/chapter1.md")) && !exists(filepath.Join(e.home, "Documents/Logs/A/keep.md")) {
					t.Fatal("the symlink's destination was touched")
				}
			}
			ev := e.events(t)
			if ev[1].Event != audit.ItemSkipped || ev[1].Reason == "" {
				t.Errorf("skip not audited with a reason: %+v", ev[1])
			}
			t.Log(ev[1].Reason)
		})
	}
}

func mustRename(t *testing.T, a, b string) {
	t.Helper()
	if err := os.Rename(a, b); err != nil {
		t.Fatal(err)
	}
}

func mustSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

// A swap that lands after the checks, between opening the parent folder
// and renaming, is caught by the identity check on the opened folder.
func TestSwapDuringMove(t *testing.T) {
	e := newEnv(t)
	target := e.folder(t, "Library/Logs/A")
	p := e.plan(t, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, target))
	victim := e.folder(t, "Documents/Thesis", "chapter1.md")
	testHook = func(stage string) {
		if stage == "pending" {
			mustRename(t, target, target+".moved")
			mustSymlink(t, victim, target)
		}
	}
	t.Cleanup(func() { testHook = nil })
	rep := mustApply(t, e, p)
	if rep.Items[0].Outcome != OutcomeSkipped {
		t.Fatalf("item = %+v", rep.Items[0])
	}
	if !exists(filepath.Join(victim, "chapter1.md")) {
		t.Fatal("victim moved")
	}
	l, _ := quarantine.List(e.st, quarantine.Root(e.storePath), e.cfg.Now())
	if len(l.Entries) != 0 || len(l.Unknown) != 0 {
		t.Errorf("canceled entry left behind: %+v", l)
	}
}

func TestContentChecks(t *testing.T) {
	cases := []struct {
		name  string
		files []string
		skip  string
	}{
		{"git repo", []string{"proj/.git/HEAD"}, "git repository"},
		{"git file", []string{"proj/.git"}, "git repository"},
		{"bare repo", []string{"backup.git/HEAD", "backup.git/objects/x", "backup.git/refs/heads/main"}, "git repository"},
		{"env file", []string{"app/.env.local"}, "environment file"},
		{"envrc", []string{".envrc"}, "environment file"},
		{"vendored git is fine", []string{"node_modules/pkg/.git/HEAD", "node_modules/pkg/index.js"}, ""},
		{"vendored env is fine", []string{"lib/site-packages/x/.env"}, ""},
		{"look-alikes are fine", []string{".gitignore", "my.git.repo/a", ".github/x", "HEAD"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			target := e.folder(t, "Library/Logs/A", c.files...)
			p := e.plan(t, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, target))
			it := mustApply(t, e, p).Items[0]
			if c.skip == "" {
				if it.Outcome != OutcomeDone {
					t.Fatalf("item = %+v, want done", it)
				}
				return
			}
			if it.Outcome != OutcomeSkipped || !strings.Contains(it.Reason, c.skip) || !exists(target) {
				t.Fatalf("item = %+v, want skipped for %q", it, c.skip)
			}
		})
	}
}

func TestChangedSincePlan(t *testing.T) {
	e := newEnv(t)
	safe := e.folder(t, "Library/Logs/A")
	caution := e.folder(t, "Library/Caches/com.example.app")
	p := e.plan(t,
		e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, safe),
		e.item(t, "O-bbbbbb", "user.app_caches", model.RiskCaution, caution))
	for _, d := range []string{safe, caution} {
		if err := os.WriteFile(filepath.Join(d, "new.bin"), make([]byte, 40000), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	rep := mustApply(t, e, p)
	byID := map[string]ItemResult{}
	for _, it := range rep.Items {
		byID[it.Item.OpportunityID] = it
	}
	if a := byID["O-aaaaaa"]; a.Outcome != OutcomeDone || a.MovedBytes < 40000 {
		t.Errorf("SAFE item = %+v, want moved and re-sized", a)
	}
	if b := byID["O-bbbbbb"]; b.Outcome != OutcomeSkipped || !strings.Contains(b.Reason, "changed") || !exists(caution) {
		t.Errorf("CAUTION item = %+v, want skipped", b)
	}
}

func TestPreconditionSkips(t *testing.T) {
	e := newEnv(t)
	e.runner.Responses["ps -axww -o command="] = platform.Result{Stdout: []byte("/Applications/Xcode.app/Contents/MacOS/Xcode -psn\n")}
	target := e.folder(t, "Library/Developer/Xcode/DerivedData/App-abc")
	p := e.plan(t, e.item(t, "O-aaaaaa", "xcode.derived_data", model.RiskSafe, target))
	it := mustApply(t, e, p).Items[0]
	if it.Outcome != OutcomeSkipped || !strings.Contains(it.Reason, "Xcode is open") || !exists(target) {
		t.Fatalf("item = %+v", it)
	}
}

func TestPreconditionUncheckableSkips(t *testing.T) {
	e := newEnv(t)
	delete(e.runner.Responses, "ps -axww -o command=")
	target := e.folder(t, "Library/Developer/Xcode/DerivedData/App-abc")
	p := e.plan(t, e.item(t, "O-aaaaaa", "xcode.derived_data", model.RiskSafe, target))
	if it := mustApply(t, e, p).Items[0]; it.Outcome != OutcomeSkipped || !exists(target) {
		t.Fatalf("item = %+v", it)
	}
}

func withTools(t *testing.T, names ...string) {
	t.Helper()
	old := recipes.ToolInstalled
	recipes.ToolInstalled = func(n string) bool { return slices.Contains(names, n) }
	t.Cleanup(func() { recipes.ToolInstalled = old })
}

func TestNativeCommands(t *testing.T) {
	withTools(t, "brew")
	e := newEnv(t)
	e.exec.out = "Removing: /opt/homebrew/Cellar/foo/1.0... (12 files, 3MB)\n"
	p := e.plan(t, e.native(t, "O-aaaaaa", "brew.cleanup", model.RiskSafe, "brew", "cleanup", "--prune=all"))
	it := mustApply(t, e, p).Items[0]
	if it.Outcome != OutcomeDone || it.ExitCode == nil || *it.ExitCode != 0 {
		t.Fatalf("item = %+v", it)
	}
	if len(e.exec.calls) != 1 || !slices.Equal(e.exec.calls[0], []string{"brew", "cleanup", "--prune=all"}) {
		t.Fatalf("ran %v", e.exec.calls)
	}
	ev := e.events(t)[1]
	if ev.Event != audit.ItemDone || !strings.Contains(ev.Output, "Removing") || len(ev.Argv) != 3 {
		t.Errorf("audit = %+v", ev)
	}
}

func TestNativeFailure(t *testing.T) {
	withTools(t, "brew")
	e := newEnv(t)
	e.exec.code, e.exec.out = 1, "Error: permission denied"
	p := e.plan(t, e.native(t, "O-aaaaaa", "brew.cleanup", model.RiskSafe, "brew", "cleanup", "--prune=all"))
	rep := mustApply(t, e, p)
	if it := rep.Items[0]; it.Outcome != OutcomeFailed || *it.ExitCode != 1 {
		t.Fatalf("item = %+v", it)
	}
	if ev := e.events(t); ev[1].Event != audit.ItemFailed || ev[2].Result != "nothing" {
		t.Errorf("audit = %v / %s", eventNames(ev), ev[2].Result)
	}
}

func TestNativeRefusedInSandboxWithRealExec(t *testing.T) {
	withTools(t, "brew")
	e := newEnv(t)
	e.cfg.Exec = nil // the real one
	p := e.plan(t, e.native(t, "O-aaaaaa", "brew.cleanup", model.RiskSafe, "brew", "cleanup", "--prune=all"))
	if it := mustApply(t, e, p).Items[0]; it.Outcome != OutcomeSkipped || !strings.Contains(it.Reason, "sandbox") {
		t.Fatalf("item = %+v", it)
	}
}

func TestNativeToolMissing(t *testing.T) {
	withTools(t)
	e := newEnv(t)
	p := e.plan(t, e.native(t, "O-aaaaaa", "brew.cleanup", model.RiskSafe, "brew", "cleanup", "--prune=all"))
	// Verify needs nothing installed; the item check does.
	if it := mustApply(t, e, p).Items[0]; it.Outcome != OutcomeSkipped || len(e.exec.calls) != 0 {
		t.Fatalf("item = %+v", it)
	}
}

// A dynamic identifier must still be in the tool's own listing, and the
// precondition (model not loaded) must hold.
func TestNativeIdentifierRelisted(t *testing.T) {
	withTools(t, "ollama")
	list := "NAME              ID            SIZE      MODIFIED\nllama3.2:latest   a80c4f17acd5  2.0 GB    3 weeks ago\n"
	idle := "NAME    ID    SIZE    PROCESSOR    UNTIL\n"
	cases := []struct {
		name, model, ps string
		want            Outcome
	}{
		{"listed and idle", "llama3.2:latest", idle, OutcomeDone},
		{"no longer listed", "qwen2.5:7b", idle, OutcomeSkipped},
		{"loaded", "llama3.2:latest", idle + "llama3.2:latest  a80c  3 GB  100% GPU  4 minutes from now\n", OutcomeSkipped},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t)
			e.runner.Responses["ollama list"] = platform.Result{Stdout: []byte(list)}
			e.runner.Responses["ollama ps"] = platform.Result{Stdout: []byte(c.ps)}
			p := e.plan(t, e.native(t, "O-aaaaaa", "ollama.model", model.RiskCaution, "ollama", "rm", c.model))
			it := mustApply(t, e, p).Items[0]
			if it.Outcome != c.want {
				t.Fatalf("item = %+v, want %s", it, c.want)
			}
			if (c.want == OutcomeDone) != (len(e.exec.calls) == 1) {
				t.Errorf("ran %v", e.exec.calls)
			}
		})
	}
}

// A failed or skipped item doesn't stop the others.
func TestItemsAreIndependent(t *testing.T) {
	withTools(t, "brew")
	e := newEnv(t)
	gone := e.folder(t, "Library/Logs/Gone")
	ok := e.folder(t, "Library/Logs/Ok")
	p := e.plan(t,
		e.native(t, "O-cccccc", "brew.cleanup", model.RiskSafe, "brew", "cleanup", "--prune=all"),
		e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, gone),
		e.item(t, "O-bbbbbb", "user.logs", model.RiskSafe, ok))
	mustRename(t, gone, gone+".x")
	e.exec.code = 2
	rep := mustApply(t, e, p)
	got := []Outcome{rep.Items[0].Outcome, rep.Items[1].Outcome, rep.Items[2].Outcome}
	if !slices.Equal(got, []Outcome{OutcomeFailed, OutcomeSkipped, OutcomeDone}) {
		t.Fatalf("outcomes = %v", got)
	}
	if exists(ok) {
		t.Error("third item not moved")
	}
}

func TestSandboxBoundary(t *testing.T) {
	e := newEnv(t)
	target := e.folder(t, "Library/Logs/A")
	p := e.plan(t, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, target))

	t.Setenv(sandbox.EnvVar, "")
	if _, err := Apply(context.Background(), e.cfg, p, all(p)); err == nil {
		t.Fatal("apply ran with an empty MACDOC_SANDBOX")
	}
	other := t.TempDir()
	t.Setenv(sandbox.EnvVar, other)
	// The state folder is outside the sandbox, so apply refuses before any
	// write (P6-R1 F6): an error, or a skipped item, and the target untouched.
	rep, err := Apply(context.Background(), e.cfg, p, all(p))
	if err != nil {
		if !strings.Contains(err.Error(), "sandbox") {
			t.Fatalf("refusal does not name the sandbox: %v", err)
		}
	} else if it := rep.Items[0]; it.Outcome != OutcomeSkipped || !strings.Contains(it.Reason, "sandbox") {
		t.Fatalf("item outside the sandbox = %+v", it)
	}
	if !exists(target) {
		t.Fatal("target moved outside the sandbox")
	}
}

func TestRestoreNeverOverwrites(t *testing.T) {
	e := newEnv(t)
	target := e.folder(t, "Library/Logs/A")
	p := e.plan(t, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, target))
	qid := mustApply(t, e, p).Items[0].QuarantineID
	e.folder(t, "Library/Logs/A", "new.log")
	if _, err := Restore(context.Background(), e.cfg, qid); !errors.Is(err, ErrNotRestorable) {
		t.Fatalf("restore over an existing folder = %v", err)
	}
	if !exists(filepath.Join(target, "new.log")) || !exists(filepath.Join(quarantine.Root(e.storePath), qid, "payload", "A", "a.log")) {
		t.Fatal("something was overwritten")
	}
	if _, err := Restore(context.Background(), e.cfg, "../../x"); err == nil {
		t.Error("restore accepted a path")
	}
}

func TestPurge(t *testing.T) {
	e := newEnv(t)
	p := e.plan(t,
		e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, e.folder(t, "Library/Logs/A")),
		e.item(t, "O-bbbbbb", "user.logs", model.RiskSafe, e.folder(t, "Library/Logs/B")))
	rep := mustApply(t, e, p)
	a, b := rep.Items[0].QuarantineID, rep.Items[1].QuarantineID
	root := quarantine.Root(e.storePath)

	// A read-only folder inside the payload is still removed.
	ro := filepath.Join(root, a, "payload", "A", "ro")
	if err := os.MkdirAll(filepath.Join(ro, "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(ro, 0o555); err != nil { //nolint:gosec // G302: a read-only fixture folder.
		t.Fatal(err)
	}
	// A folder macdoc didn't create is never touched, even by ID.
	stray := filepath.Join(root, "Q-ZZZZ", "payload", "x")
	if err := os.MkdirAll(stray, 0o755); err != nil {
		t.Fatal(err)
	}

	res, err := Purge(context.Background(), e.cfg, []string{a, "Q-ZZZZ", "../etc"})
	if err != nil {
		t.Fatal(err)
	}
	if res[0].Outcome != OutcomeDone || exists(filepath.Join(root, a)) {
		t.Fatalf("purge %s = %+v", a, res[0])
	}
	if res[1].Outcome != OutcomeSkipped || !exists(stray) || res[2].Outcome != OutcomeSkipped {
		t.Fatalf("purge of unknown entries = %+v", res[1:])
	}
	if !exists(filepath.Join(root, b)) {
		t.Fatal("an entry that wasn't asked for was purged")
	}
	if rec, _ := e.st.Quarantine(a); rec.Status != store.QuarantinePurged {
		t.Errorf("status %s", rec.Status)
	}
	if _, err := Restore(context.Background(), e.cfg, a); err == nil {
		t.Error("restored a purged entry")
	}
	l, _ := quarantine.List(e.st, root, e.cfg.Now())
	if len(l.Entries) != 1 || !slices.Equal(l.Unknown, []string{"Q-ZZZZ"}) {
		t.Errorf("listing = %+v", l)
	}
	ev := e.events(t)
	if ev[len(ev)-3].Event != audit.QuarantinePurged || ev[len(ev)-3].ActualBytes == nil {
		t.Errorf("purge not audited: %+v", ev[len(ev)-3])
	}
}

// A manifest that doesn't match macdoc's record, or a payload replaced by a
// symlink, makes the entry damaged: never restored, never purged.
func TestDamagedEntriesUntouched(t *testing.T) {
	cases := map[string]func(t *testing.T, e *env, entry string){
		"manifest names another original": func(t *testing.T, e *env, entry string) {
			mp := filepath.Join(entry, quarantine.ManifestName)
			b, _ := os.ReadFile(mp)
			b = []byte(strings.Replace(string(b), "Library/Logs/A", "Documents/A", 1))
			if err := os.WriteFile(mp, b, 0o600); err != nil { //nolint:gosec // G703: a test fixture path.
				t.Fatal(err)
			}
		},
		"payload is a symlink": func(t *testing.T, e *env, entry string) {
			victim := e.folder(t, "Documents/A", "keep.md")
			pl := filepath.Join(entry, "payload", "A")
			if err := os.RemoveAll(pl); err != nil {
				t.Fatal(err)
			}
			mustSymlink(t, victim, pl)
		},
	}
	for name, damage := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			p := e.plan(t, e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, e.folder(t, "Library/Logs/A")))
			qid := mustApply(t, e, p).Items[0].QuarantineID
			entry := filepath.Join(quarantine.Root(e.storePath), qid)
			damage(t, e, entry)
			if _, err := Restore(context.Background(), e.cfg, qid); err == nil {
				t.Error("restored a damaged entry")
			}
			res, _ := Purge(context.Background(), e.cfg, []string{qid})
			if res[0].Outcome == OutcomeDone || !exists(entry) {
				t.Fatalf("purged a damaged entry: %+v", res[0])
			}
			if exists(filepath.Join(e.home, "Documents/A")) && !exists(filepath.Join(e.home, "Documents/A/keep.md")) {
				t.Fatal("the symlink's destination was touched")
			}
		})
	}
}

func TestDeleteNow(t *testing.T) {
	e := newEnv(t)
	e.cfg.DeleteNow = true
	a := e.folder(t, "Library/Logs/A", "a.log", "sub/b.log")
	b := e.folder(t, "Library/Logs/B", "b.log")
	p := e.plan(t,
		e.item(t, "O-aaaaaa", "user.logs", model.RiskSafe, a),
		e.item(t, "O-bbbbbb", "user.logs", model.RiskSafe, b))
	// The second one fails its re-check, so nothing of it is touched.
	if err := os.WriteFile(filepath.Join(b, ".env"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	rep := mustApply(t, e, p)

	it := rep.Items[0]
	if it.Outcome != OutcomeDone || !it.Deleted || it.QuarantineID == "" {
		t.Fatalf("item = %+v", it)
	}
	root := quarantine.Root(e.storePath)
	if exists(a) || exists(filepath.Join(root, it.QuarantineID)) {
		t.Fatal("not deleted")
	}
	if rec, _ := e.st.Quarantine(it.QuarantineID); rec.Status != store.QuarantinePurged {
		t.Errorf("entry status %s", rec.Status)
	}
	if rep.Items[1].Outcome != OutcomeSkipped || rep.Items[1].Deleted || !exists(filepath.Join(b, "b.log")) {
		t.Fatalf("skipped item = %+v", rep.Items[1])
	}
	if rep.DeletedBytes() != it.MovedBytes || rep.MovedBytes() != 0 {
		t.Errorf("deleted %d, held %d", rep.DeletedBytes(), rep.MovedBytes())
	}
	want := []string{audit.ApplyStarted, audit.ItemDone, audit.QuarantinePurged, audit.ItemSkipped, audit.ApplyFinished}
	if got := eventNames(e.events(t)); !slices.Equal(got, want) {
		t.Errorf("audit events = %v", got)
	}
	if _, err := Restore(context.Background(), e.cfg, it.QuarantineID); err == nil {
		t.Error("restored a deleted item")
	}
}
