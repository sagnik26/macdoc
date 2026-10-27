package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"macdoc/internal/model"
	"macdoc/internal/recipes"
	"macdoc/internal/store"
)

// Test-only detectors. w3test_fixed returns the findings registered for the
// recipe ID in fixedFindings.
var (
	fixedMu       sync.Mutex
	fixedFindings = map[string][]recipes.Finding{}
	blockDetector = make(chan struct{})
)

func init() {
	recipes.Register("w3test_fixed", func(_ context.Context, _ recipes.Env, r recipes.Recipe) ([]recipes.Finding, error) {
		fixedMu.Lock()
		defer fixedMu.Unlock()
		return fixedFindings[r.ID], nil
	})
	recipes.Register("w3test_err", func(context.Context, recipes.Env, recipes.Recipe) ([]recipes.Finding, error) {
		return nil, errors.New("tool exploded")
	})
	recipes.Register("w3test_panic", func(context.Context, recipes.Env, recipes.Recipe) ([]recipes.Finding, error) {
		panic("boom")
	})
	recipes.Register("w3test_block", func(context.Context, recipes.Env, recipes.Recipe) ([]recipes.Finding, error) {
		<-blockDetector // ignores its context on purpose
		return nil, nil
	})
	recipes.Register("w3test_nodes", func(_ context.Context, env recipes.Env, r recipes.Recipe) ([]recipes.Finding, error) {
		ns, err := env.Nodes.Nodes(filepath.Join(env.Home, "Library/Caches"), 1)
		if err != nil {
			return nil, err
		}
		var out []recipes.Finding
		for _, n := range ns {
			if filepath.Base(n.Path) != "Caches" {
				out = append(out, recipes.Finding{Target: n.Path, Bytes: model.Bytes{Value: n.Bytes, Source: model.SourceMeasured}})
			}
		}
		return out, nil
	})
}

func setFindings(t *testing.T, m map[string][]recipes.Finding) {
	t.Helper()
	fixedMu.Lock()
	fixedFindings = m
	fixedMu.Unlock()
	t.Cleanup(func() {
		fixedMu.Lock()
		fixedFindings = map[string][]recipes.Finding{}
		fixedMu.Unlock()
	})
}

func stubTools(t *testing.T, missing ...string) {
	t.Helper()
	old := recipes.ToolInstalled
	recipes.ToolInstalled = func(name string) bool {
		for _, m := range missing {
			if m == name {
				return false
			}
		}
		return true
	}
	t.Cleanup(func() { recipes.ToolInstalled = old })
}

// fixture is a fake home with a store holding one complete scan.
type fixture struct {
	home, storePath, scanID string
	now                     time.Time
}

func newFixture(t *testing.T, age time.Duration, nodes ...model.Node) fixture {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"Library/Caches", "Documents", "code/app/node_modules"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	f := fixture{home: home, storePath: filepath.Join(home, "state.db"), now: now}
	st, err := store.Open(f.storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	f.scanID, err = st.BeginScan("/", "test", now.Add(-age-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) > 0 {
		if err := st.AddNodes(f.scanID, nodes); err != nil {
			t.Fatal(err)
		}
	}
	var total int64
	for _, n := range nodes {
		if n.Depth == 0 {
			total = n.Bytes
		}
	}
	if err := st.FinishScan(f.scanID, model.ScanComplete, total, 1, now.Add(-age)); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f fixture) p(rel string) string { return filepath.Join(f.home, rel) }

func (f fixture) run(t *testing.T, rs ...recipes.Recipe) OpportunityResult {
	t.Helper()
	res, err := Opportunities(context.Background(), f.opts(rs...))
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func (f fixture) opts(rs ...recipes.Recipe) OpportunityOptions {
	return OpportunityOptions{Home: f.home, StorePath: f.storePath, Now: f.now, Catalog: &recipes.Catalog{Recipes: rs}}
}

func rec(id string, risk model.Risk, method model.Method) recipes.Recipe {
	r := recipes.Recipe{ID: id, Title: "Title " + id, Category: "package-caches", Toolchain: "node",
		Risk: risk, Method: method, Detector: "w3test_fixed",
		Restore: model.Restore{Kind: model.RestoreRedownload, Note: "Downloads again."}, Explain: "Explain " + id}
	switch method {
	case model.MethodNative:
		r.Native = &recipes.Native{Argv: []string{"npm", "cache", "clean", "--force"}}
	case model.MethodQuarantine:
		r.Allow = []string{"~/Library/Caches/*", "~/*/**/node_modules"}
	}
	return r
}

func measured(n int64) model.Bytes { return model.Bytes{Value: n, Source: model.SourceMeasured} }

func ids(ops []model.Opportunity) []string {
	var s []string
	for _, o := range ops {
		s = append(s, o.RecipeID+":"+o.Title)
	}
	return s
}

func TestNoScan(t *testing.T) {
	home, _ := filepath.EvalSymlinks(t.TempDir())
	_, err := Opportunities(context.Background(), OpportunityOptions{Home: home, Catalog: &recipes.Catalog{}})
	if !errors.Is(err, ErrNoScan) || !strings.Contains(err.Error(), `Run "macdoc scan" first`) {
		t.Fatalf("err = %v", err)
	}
	if _, err := Opportunities(context.Background(), OpportunityOptions{}); err == nil {
		t.Error("expected error without home")
	}
	if _, err := Opportunities(context.Background(), OpportunityOptions{Home: "relative", Catalog: &recipes.Catalog{}}); err == nil {
		t.Error("expected policy error for relative home")
	}
}

func TestIncompleteScanIsIgnored(t *testing.T) {
	home, _ := filepath.EvalSymlinks(t.TempDir())
	sp := filepath.Join(home, "state.db")
	st, err := store.Open(sp)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := st.BeginScan("/", "t", time.Now())
	_ = st.FinishScan(id, model.ScanIncomplete, 1, 1, time.Now())
	_ = st.Close()
	_, err = Opportunities(context.Background(), OpportunityOptions{Home: home, StorePath: sp, Catalog: &recipes.Catalog{}})
	if !errors.Is(err, ErrNoScan) {
		t.Fatalf("err = %v", err)
	}
}

func TestScanAgeWarning(t *testing.T) {
	f := newFixture(t, 2*time.Hour)
	res := f.run(t)
	if res.ScanAge != "2h ago" || len(res.Warnings) != 0 {
		t.Fatalf("age %q warnings %v", res.ScanAge, res.Warnings)
	}
	if res.Opportunities == nil {
		t.Error("opportunities must be an empty list, not null")
	}
	f = newFixture(t, 9*time.Hour)
	res = f.run(t)
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "scan 9 hours old") {
		t.Fatalf("warnings = %v", res.Warnings)
	}
}

func TestDefaultsUseRealCatalogAndClock(t *testing.T) {
	f := newFixture(t, time.Hour)
	stubTools(t, recipes.NativeTools...)
	res, err := Opportunities(context.Background(), OpportunityOptions{Home: f.home, StorePath: f.storePath})
	if err != nil {
		t.Fatal(err)
	}
	// The fake home has no caches, so the real catalog finds nothing to act on.
	for _, op := range res.Opportunities {
		if op.Method == model.MethodNative {
			t.Errorf("native item despite missing tools: %+v", op)
		}
	}
}

func TestNativeDropRules(t *testing.T) {
	f := newFixture(t, time.Hour)
	stubTools(t, "brew")
	plain := rec("t.native", model.RiskSafe, model.MethodNative)
	missing := rec("t.missing", model.RiskSafe, model.MethodNative)
	missing.Native = &recipes.Native{Argv: []string{"brew", "cleanup"}}
	withID := rec("t.runtime", model.RiskCaution, model.MethodNative)
	withID.Native = &recipes.Native{Argv: []string{"xcrun", "simctl", "runtime", "delete", "{id}"}, DryRunArgv: []string{"xcrun", "simctl", "runtime", "list", "{id}"}}
	setFindings(t, map[string][]recipes.Finding{
		"t.native":  {{Bytes: measured(5)}},
		"t.missing": {{Bytes: measured(5)}},
		"t.runtime": {
			{NativeArg: "ABC-123", Bytes: measured(9), Title: "iOS 17"},
			{NativeArg: "", Bytes: measured(1)},
			{NativeArg: "a b", Bytes: measured(1)},
			{NativeArg: "../x", Bytes: measured(1)},
			{NativeArg: "-rf", Bytes: measured(1)},
			{NativeArg: "x;y", Bytes: measured(1)},
			{NativeArg: "x\x01", Bytes: measured(1)},
			{NativeArg: "x$y", Bytes: measured(1)},
			{NativeArg: "x*", Bytes: measured(1)},
		},
	})
	res := f.run(t, plain, missing, withID)
	if len(res.Opportunities) != 2 {
		t.Fatalf("got %v", ids(res.Opportunities))
	}
	var rt model.Opportunity
	for _, o := range res.Opportunities {
		if o.RecipeID == "t.missing" {
			t.Error("missing tool not dropped")
		}
		if o.RecipeID == "t.runtime" {
			rt = o
		}
	}
	if strings.Join(rt.Command, " ") != "xcrun simctl runtime delete ABC-123" || strings.Join(rt.DryRun, " ") != "xcrun simctl runtime list ABC-123" {
		t.Errorf("command = %v dry = %v", rt.Command, rt.DryRun)
	}
	// 8 bad identifiers: each dropped with a warning; missing tool is silent.
	if len(res.Warnings) != 8 {
		t.Fatalf("warnings = %q", res.Warnings)
	}
	for _, w := range res.Warnings {
		if !strings.HasPrefix(w, "t.runtime: ") {
			t.Errorf("warning %q lacks recipe prefix", w)
		}
	}
}

func TestQuarantineCheckTarget(t *testing.T) {
	f := newFixture(t, time.Hour)
	q := rec("t.q", model.RiskSafe, model.MethodQuarantine)
	adv := rec("t.adv", model.RiskAdvise, model.MethodAdvise)
	setFindings(t, map[string][]recipes.Finding{
		"t.q": {
			{Target: f.p("Library/Caches/com.example"), Bytes: measured(10)},
			{Target: f.p("Documents/thesis"), Bytes: measured(10)},       // protected
			{Target: f.p("Library/Other/x"), Bytes: measured(10)},        // not allowed
			{Target: "/Library/Caches/com.example", Bytes: measured(10)}, // outside home
		},
		"t.adv": {{Target: "/Users/Shared/backup", Bytes: measured(3)}}, // advise: informational
	})
	res := f.run(t, q, adv)
	if len(res.Opportunities) != 2 {
		t.Fatalf("got %v", ids(res.Opportunities))
	}
	if len(res.Warnings) != 3 {
		t.Fatalf("warnings = %q", res.Warnings)
	}
	if !strings.Contains(strings.Join(res.Warnings, "\n"), "Documents") {
		t.Errorf("protected target not mentioned: %q", res.Warnings)
	}
}

func TestStalenessRiskAndConfidence(t *testing.T) {
	f := newFixture(t, time.Hour)
	stale := rec("t.stale", model.RiskCaution, model.MethodQuarantine)
	stale.StaleDays = 90
	stale.KeepNames = []string{"node_modules"}
	safe := rec("t.safe", model.RiskSafe, model.MethodNative)
	safe.Detector = "w3test_fixed"
	risky := rec("t.risky", model.RiskRisky, model.MethodNative)
	day := 24 * time.Hour
	setFindings(t, map[string][]recipes.Finding{
		"t.stale": {
			{Target: f.p("code/app/node_modules"), Bytes: measured(100), LastUsed: f.now.Add(-120 * day)},
			{Target: f.p("code/new/node_modules"), Bytes: measured(100), LastUsed: f.now.Add(-30 * day)}, // too fresh: dropped
			{Target: f.p("code/unk/node_modules"), Bytes: measured(100)},                                 // unknown idle: kept, ×0.5
		},
		"t.safe": {
			{Target: "/x/1", Bytes: model.Bytes{Value: 50, Source: model.SourceTool}, LastUsed: f.now.Add(-2 * day), Title: "fresh"},
			{Target: "/x/2", Bytes: model.Bytes{Value: 50, Source: model.SourceEstimated}, LastUsed: f.now.Add(-30 * day), Title: "old"},
			{Target: "/x/3", Bytes: model.Bytes{Source: model.SourceUnknown}, Title: "unknown"},
			{Target: "/x/4", Bytes: model.Bytes{Value: 7}, Title: "nosource"},
		},
		"t.risky": {{Bytes: measured(1), LastUsed: f.now.Add(-1 * day)}}, // never lowered, never raised
	})
	res := f.run(t, stale, safe, risky)
	got := map[string]model.Opportunity{}
	for _, o := range res.Opportunities {
		got[o.Title] = o
	}
	if len(res.Opportunities) != 7 {
		t.Fatalf("got %v", ids(res.Opportunities))
	}
	check := func(title string, risk model.Risk, conf float64, idle int) {
		t.Helper()
		o, ok := got[title]
		if !ok {
			t.Fatalf("missing %q in %v", title, ids(res.Opportunities))
		}
		if o.Risk != risk || o.Confidence != conf || o.IdleDays != idle {
			t.Errorf("%s: risk %s conf %v idle %d", title, o.Risk, o.Confidence, o.IdleDays)
		}
	}
	check("Title t.stale · app", model.RiskCaution, 1.0, 120)
	check("Title t.stale · unk", model.RiskCaution, 0.5, -1)
	check("fresh", model.RiskCaution, 0.9, 2) // SAFE raised
	check("old", model.RiskSafe, 0.6, 30)
	check("unknown", model.RiskSafe, 0.3, -1)
	check("nosource", model.RiskSafe, 0.3, -1)
	check("Title t.risky", model.RiskRisky, 1.0, 1)

	if got["nosource"].Bytes.Source != model.SourceUnknown {
		t.Error("missing source must become unknown")
	}
	cat := &recipes.Catalog{Recipes: []recipes.Recipe{stale, safe, risky}}
	if base, ok := RaisedFrom(got["fresh"], cat); !ok || base != model.RiskSafe {
		t.Errorf("RaisedFrom = %v %v", base, ok)
	}
	if _, ok := RaisedFrom(got["old"], cat); ok {
		t.Error("not raised")
	}
	if _, ok := RaisedFrom(got["old"], nil); ok {
		t.Error("nil catalog")
	}
	// Totals: known bytes only, by final risk.
	if res.Totals[model.RiskCaution] != 250 || res.Totals[model.RiskSafe] != 50 || res.Totals[model.RiskRisky] != 1 {
		t.Errorf("totals = %v", res.Totals)
	}
}

func TestOverlapMerge(t *testing.T) {
	f := newFixture(t, time.Hour)
	nvm := rec("t.nvm", model.RiskCaution, model.MethodQuarantine)
	nvm.Allow = []string{"~/code/**"}
	nm := rec("t.nm", model.RiskCaution, model.MethodQuarantine)
	nm.Allow = []string{"~/code/**"}
	adv := rec("t.adv", model.RiskAdvise, model.MethodAdvise)
	setFindings(t, map[string][]recipes.Finding{
		"t.nvm": {{Target: f.p("code/app"), Bytes: measured(100)}},
		"t.nm": {
			{Target: f.p("code/app/node_modules"), Bytes: measured(60)}, // under t.nvm: merged
			{Target: f.p("code/app/node_modules"), Bytes: measured(60)}, // duplicate
			{Target: f.p("code/application"), Bytes: measured(5)},       // sibling prefix, not a descendant
		},
		"t.adv": {
			{Target: f.p("code"), Bytes: measured(500)},                  // advise never absorbs
			{Target: f.p("code/app/node_modules/x"), Bytes: measured(1)}, // and is never absorbed
		},
	})
	res := f.run(t, nvm, nm, adv)
	if len(res.Opportunities) != 4 {
		t.Fatalf("got %v", ids(res.Opportunities))
	}
	for _, o := range res.Opportunities {
		if o.RecipeID == "t.nm" && strings.HasSuffix(o.Target, "node_modules") {
			t.Errorf("descendant not merged: %+v", o)
		}
	}
	if len(res.Warnings) != 1 || !strings.HasPrefix(res.Warnings[0], "1 item(s)") {
		t.Errorf("warnings = %q", res.Warnings)
	}
}

func TestDetectorErrorsPanicsAndTimeout(t *testing.T) {
	f := newFixture(t, time.Hour)
	old := detectorTimeout
	detectorTimeout = 50 * time.Millisecond
	t.Cleanup(func() { detectorTimeout = old })
	t.Cleanup(func() { close(blockDetector) })

	mk := func(id, det string) recipes.Recipe {
		r := rec(id, model.RiskSafe, model.MethodNative)
		r.Detector = det
		return r
	}
	ok := mk("t.ok", "w3test_fixed")
	unknown := mk("t.unknown", "w3test_does_not_exist")
	setFindings(t, map[string][]recipes.Finding{"t.ok": {{Bytes: measured(1)}}})
	start := time.Now()
	res := f.run(t, mk("t.err", "w3test_err"), mk("t.panic", "w3test_panic"), mk("t.block", "w3test_block"), ok, unknown)
	if time.Since(start) > 5*time.Second {
		t.Fatal("timeout not applied")
	}
	if len(res.Opportunities) != 1 {
		t.Fatalf("other detectors must still run: %v", ids(res.Opportunities))
	}
	w := strings.Join(res.Warnings, "\n")
	for _, want := range []string{"t.err: tool exploded", "t.panic: detector panicked: boom", "t.block: detector stopped after 50ms", "t.unknown: unknown detector"} {
		if !strings.Contains(w, want) {
			t.Errorf("missing %q in %q", want, w)
		}
	}
}

func TestDetectorSeesScanNodes(t *testing.T) {
	f := newFixture(t, time.Hour)
	f = withNodes(t, f,
		model.Node{Path: f.p("Library/Caches"), Kind: model.NodeDir, Bytes: 30, Depth: 2},
		model.Node{Path: f.p("Library/Caches/a"), Kind: model.NodeDir, Bytes: 20, Depth: 3},
		model.Node{Path: f.p("Library/Caches/b"), Kind: model.NodeDir, Bytes: 10, Depth: 3},
	)
	r := rec("t.children", model.RiskSafe, model.MethodQuarantine)
	r.Detector = "w3test_nodes"
	res := f.run(t, r)
	if len(res.Opportunities) != 2 || res.Opportunities[0].Title != "Title t.children · a" {
		t.Fatalf("got %v", ids(res.Opportunities))
	}
}

// withNodes replaces the fixture's store with a new complete scan holding nodes.
func withNodes(t *testing.T, f fixture, nodes ...model.Node) fixture {
	t.Helper()
	st, err := store.Open(f.storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	id, err := st.BeginScan("/", "test", f.now.Add(-30*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddNodes(id, nodes); err != nil {
		t.Fatal(err)
	}
	var total int64
	for _, n := range nodes {
		total = max(total, n.Bytes)
	}
	if err := st.FinishScan(id, model.ScanComplete, total, 1, f.now.Add(-20*time.Minute)); err != nil {
		t.Fatal(err)
	}
	f.scanID = id
	return f
}

func TestTitles(t *testing.T) {
	f := newFixture(t, time.Hour)
	single := rec("t.single", model.RiskSafe, model.MethodQuarantine)
	single.Detector = "w3test_fixed"
	nm := rec("t.nm", model.RiskCaution, model.MethodQuarantine)
	nm.KeepNames = []string{"node_modules"}
	setFindings(t, map[string][]recipes.Finding{
		"t.single": {{Target: f.p("Library/Caches/one"), Bytes: measured(3), Note: "a note"}},
		"t.nm": {
			{Target: f.p("code/app/node_modules"), Bytes: measured(2)},
			{Target: f.p("code/web/node_modules"), Bytes: measured(1)},
		},
	})
	res := f.run(t, single, nm)
	titles := map[string]model.Opportunity{}
	for _, o := range res.Opportunities {
		titles[o.Title] = o
	}
	// A single finding keeps the recipe title. Several get the instance name;
	// a fixed folder name (node_modules) is replaced by its project.
	for _, want := range []string{"Title t.single", "Title t.nm · app", "Title t.nm · web"} {
		if _, ok := titles[want]; !ok {
			t.Errorf("missing %q in %v", want, ids(res.Opportunities))
		}
	}
	o := titles["Title t.single"]
	if o.Note != "a note" || o.Explain != "Explain t.single" || o.Category != "package-caches" || o.Toolchain != "node" || o.Restore.Kind != model.RestoreRedownload {
		t.Errorf("fields = %+v", o)
	}
	r := rec("t.r", model.RiskSafe, model.MethodNative)
	r.Detector = "path"
	if multiInstance(r, 1) || !multiInstance(r, 2) {
		t.Error("multiInstance for path detector")
	}
	r.Detector = "children"
	if !multiInstance(r, 1) {
		t.Error("children detector is always multi-instance")
	}
}

func TestIDsDeterministic(t *testing.T) {
	f := newFixture(t, time.Hour)
	q := rec("t.q", model.RiskSafe, model.MethodQuarantine)
	setFindings(t, map[string][]recipes.Finding{"t.q": {
		{Target: f.p("Library/Caches/a"), Bytes: measured(1)},
		{Target: f.p("Library/Caches/b"), Bytes: measured(1)},
	}})
	a, b := f.run(t, q), f.run(t, q)
	for i := range a.Opportunities {
		id := a.Opportunities[i].ID
		if id != b.Opportunities[i].ID || len(id) != 8 || !strings.HasPrefix(id, "O-") || strings.ToLower(id[2:]) != id[2:] {
			t.Errorf("id %q vs %q", id, b.Opportunities[i].ID)
		}
		want := "O-" + opportunityID(f.scanID, "t.q", a.Opportunities[i].Target, "")[:6]
		if id != want {
			t.Errorf("id %q, want %q", id, want)
		}
		if a.Opportunities[i].ScanID != f.scanID {
			t.Error("scan id not set")
		}
	}
	if a.Opportunities[0].ID == a.Opportunities[1].ID {
		t.Error("distinct targets share an ID")
	}
	// A different scan gives different IDs.
	g := withNodes(t, f, model.Node{Path: "/", Kind: model.NodeDir, Bytes: 1})
	c := g.run(t, q)
	if c.Opportunities[0].ID == a.Opportunities[0].ID && c.Opportunities[1].ID == a.Opportunities[1].ID {
		t.Error("IDs did not change with the scan")
	}
	if o, ok := FindOpportunity(a, strings.ToUpper(a.Opportunities[1].ID[2:])); !ok || o.ID != a.Opportunities[1].ID {
		t.Error("FindOpportunity by bare upper-case ID")
	}
	if _, ok := FindOpportunity(a, "o-"+a.Opportunities[0].ID[2:]); !ok {
		t.Error("FindOpportunity with o- prefix")
	}
	if _, ok := FindOpportunity(a, "O-zzzzzz"); ok {
		t.Error("found a missing ID")
	}
}

func TestIDCollisionExtension(t *testing.T) {
	// Brute-force two targets whose 6-character IDs collide.
	seen := map[string]string{}
	var t1, t2 string
	for i := 0; t1 == ""; i++ {
		target := "/h/" + strconv.Itoa(i)
		id := opportunityID("S", "r", target, "")
		if prev, ok := seen[id[:6]]; ok {
			t1, t2 = prev, target
			break
		}
		seen[id[:6]] = target
	}
	ops := []model.Opportunity{
		{ID: opportunityID("S", "r", t1, "")},
		{ID: opportunityID("S", "r", t2, "")},
		{ID: opportunityID("S", "r", "/h/other", "")},
		{ID: opportunityID("S", "r", "/h/other", "")}, // identical: can't be separated, must not loop
	}
	assignIDs(ops)
	if len(ops[0].ID) < 10 || len(ops[1].ID) < 10 || ops[0].ID == ops[1].ID {
		t.Errorf("collision not extended: %q %q", ops[0].ID, ops[1].ID)
	}
	if len(ops[2].ID) != 8 || ops[2].ID != ops[3].ID {
		t.Errorf("identical IDs: %q %q", ops[2].ID, ops[3].ID)
	}
}

func TestRanking(t *testing.T) {
	f := newFixture(t, time.Hour)
	day := 24 * time.Hour
	old := f.now.Add(-100 * day)
	safe := rec("t.safe", model.RiskSafe, model.MethodNative)
	caution := rec("t.caution", model.RiskCaution, model.MethodNative)
	risky := rec("t.risky", model.RiskRisky, model.MethodNative)
	adv := rec("t.adv", model.RiskAdvise, model.MethodAdvise)
	setFindings(t, map[string][]recipes.Finding{
		"t.adv":     {{Title: "adv", Bytes: measured(1000)}},
		"t.risky":   {{Title: "risky", Bytes: measured(900), LastUsed: old}},
		"t.caution": {{Title: "caution", Bytes: measured(800), LastUsed: old}},
		"t.safe": {
			{Title: "safe-small", Target: "/x/c", Bytes: measured(10), LastUsed: old},
			{Title: "safe-big-estimated", Target: "/x/d", Bytes: model.Bytes{Value: 100, Source: model.SourceEstimated}, LastUsed: old}, // 60
			{Title: "safe-mid", Target: "/x/e", Bytes: measured(50), LastUsed: old},
			{Title: "safe-tie-a", Target: "/x/a", Bytes: measured(5), LastUsed: old},
			{Title: "safe-tie-b", Target: "/x/b", Bytes: measured(5), LastUsed: old},
		},
	})
	res := f.run(t, adv, risky, caution, safe)
	var got []string
	for _, o := range res.Opportunities {
		got = append(got, o.Title)
	}
	want := "safe-big-estimated safe-mid safe-small"
	if s := strings.Join(got, " "); !strings.HasPrefix(s, want) || !strings.HasSuffix(s, "caution risky adv") {
		t.Fatalf("order = %s", s)
	}
	// The two ties are ordered by ID.
	a, b := res.Opportunities[3], res.Opportunities[4]
	if a.ID > b.ID {
		t.Errorf("tie not ordered by ID: %s %s", a.ID, b.ID)
	}
	if res.Totals[model.RiskAdvise] != 1000 || res.Totals[model.RiskSafe] != 170 {
		t.Errorf("totals = %v", res.Totals)
	}
}

func TestByToolchainAndFormatAge(t *testing.T) {
	ops := []model.Opportunity{
		{Toolchain: "node", Bytes: measured(5)},
		{Toolchain: "xcode", Bytes: measured(10)},
		{Toolchain: "node", Bytes: measured(7)},
		{Toolchain: "ai", Bytes: model.Bytes{Value: 99, Source: model.SourceUnknown}},
		{Toolchain: "docker", Bytes: measured(12)},
	}
	g := ByToolchain(ops)
	var s []string
	for _, x := range g {
		s = append(s, x.Toolchain)
	}
	// Equal totals are ordered by name.
	if strings.Join(s, ",") != "docker,node,xcode,ai" || g[1].Bytes != 12 || len(g[1].Opportunities) != 2 || g[3].Bytes != 0 {
		t.Errorf("groups = %+v", g)
	}
	for d, want := range map[time.Duration]string{
		10 * time.Second: "just now", 5 * time.Minute: "5m ago", 3 * time.Hour: "3h ago", 72 * time.Hour: "3d ago",
	} {
		if got := FormatAge(d); got != want {
			t.Errorf("FormatAge(%v) = %q", d, got)
		}
	}
}

func TestCheckNativeArgAndSubstitute(t *testing.T) {
	for _, ok := range []string{"com.apple.CoreSimulator.SimRuntime.iOS-17-2", "llama3:8b", "ABC_1.2"} {
		if err := checkNativeArg(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "-x", "a b", "a\tb", "a/b", "a\\b", "a'b", `a"b`, "a(b)", "a{b}", "a?b", "a[b]", "a<b", "a>b", "a|b", "a&b", "a`b", "a\nb", "a\x7fb"} {
		if err := checkNativeArg(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	got, err := substitute([]string{"ollama", "rm", "{id}"}, "llama3:8b")
	if err != nil || strings.Join(got, " ") != "ollama rm llama3:8b" {
		t.Errorf("got %v %v", got, err)
	}
	if got, _ := substitute(nil, "x"); got != nil {
		t.Error("empty argv")
	}
	if got, err := substitute([]string{"brew", "cleanup"}, ""); err != nil || len(got) != 2 {
		t.Error("argv without placeholder ignores the identifier")
	}
}

func TestHasAncestor(t *testing.T) {
	set := map[string]bool{"/a/b": true, "/": true}
	if hasAncestor(set, "/") {
		t.Error("root has no ancestor")
	}
	if !hasAncestor(map[string]bool{"/a/b": true}, "/a/b/c/d") || hasAncestor(map[string]bool{"/a/b": true}, "/a/bc") {
		t.Error("component-wise ancestor")
	}
}
