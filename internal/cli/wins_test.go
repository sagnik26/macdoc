package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"macdoc/internal/engine"
	"macdoc/internal/model"
	"macdoc/internal/platform"
	"macdoc/internal/recipes"
	"macdoc/internal/store"
)

// w3test_cli_fixed returns the findings in cliFindings for the recipe ID.
var (
	cliMu       sync.Mutex
	cliFindings = map[string][]recipes.Finding{}
)

func init() {
	recipes.Register("w3test_cli_fixed", func(_ context.Context, _ recipes.Env, r recipes.Recipe) ([]recipes.Finding, error) {
		cliMu.Lock()
		defer cliMu.Unlock()
		return cliFindings[r.ID], nil
	})
}

const gb = 1_000_000_000

func measured(n int64) model.Bytes { return model.Bytes{Value: n, Source: model.SourceMeasured} }

func cliRecipe(id, title string, risk model.Risk, method model.Method, kind model.RestoreKind) recipes.Recipe {
	r := recipes.Recipe{ID: id, Title: title, Category: "package-caches", Toolchain: "node", Risk: risk, Method: method,
		Detector: "w3test_cli_fixed", Restore: model.Restore{Kind: kind, Note: "Reinstall with npm i in the project."},
		Explain: "What " + title + " is."}
	switch method {
	case model.MethodNative:
		r.Native = &recipes.Native{Argv: []string{"npm", "cache", "clean", "--force"}, DryRunArgv: []string{"npm", "cache", "ls"}}
	case model.MethodQuarantine:
		r.Allow = []string{"~/code/**"}
	}
	return r
}

// cliFixture installs a fake home with one complete scan, a test catalog and
// a fake runner for the duration of the test.
func cliFixture(t *testing.T) (home string) {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"a", "b", "c", "d", "e"} {
		if err := os.MkdirAll(filepath.Join(home, "code", p, "node_modules"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	st, err := store.Open(store.DefaultPath(home))
	if err != nil {
		t.Fatal(err)
	}
	id, err := st.BeginScan("/", "test", time.Now().Add(-2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddNodes(id, []model.Node{
		{Path: "/", Kind: model.NodeDir, Bytes: 100 * gb},
		{Path: filepath.Join(home, "big.iso"), Kind: model.NodeFile, Bytes: 4 * gb, Depth: 8},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.FinishScan(id, model.ScanComplete, 100*gb, 1, time.Now().Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()

	old := time.Now().Add(-200 * 24 * time.Hour)
	cache := cliRecipe("npm.cache", "npm cache", model.RiskSafe, model.MethodNative, model.RestoreRedownload)
	nm := cliRecipe("node.nm", "Stale node_modules folder", model.RiskCaution, model.MethodQuarantine, model.RestoreRedownload)
	nm.KeepNames = []string{"node_modules"}
	adv := cliRecipe("ios.backup", "iPhone backup", model.RiskAdvise, model.MethodAdvise, model.RestoreIrreplaceable)
	adv.Restore.Note = "Finder › iPhone › Manage Backups"
	adv.Toolchain = "system"
	cat := &recipes.Catalog{Recipes: []recipes.Recipe{adv, nm, cache}}
	findings := map[string][]recipes.Finding{
		"npm.cache":  {{Target: filepath.Join(home, ".npm/_cacache"), Bytes: measured(3 * gb), LastUsed: old}},
		"ios.backup": {{Target: "/Users/Shared/Backup", Bytes: model.Bytes{Source: model.SourceUnknown, Reason: "no permission"}, Title: "iPhone backup (Mar 2025)"}},
	}
	for i, p := range []string{"a", "b", "c", "d", "e"} {
		findings["node.nm"] = append(findings["node.nm"], recipes.Finding{
			Target: filepath.Join(home, "code", p, "node_modules"), Bytes: measured(int64(i+1) * gb), LastUsed: old})
	}
	cliMu.Lock()
	cliFindings = findings
	cliMu.Unlock()

	oldHome, oldCat, oldRunner, oldTool := userHome, loadCatalog, runner, recipes.ToolInstalled
	userHome = func() (string, error) { return home, nil }
	loadCatalog = func() (*recipes.Catalog, error) { return cat, nil }
	runner = &platform.FakeRunner{}
	recipes.ToolInstalled = func(string) bool { return true }
	t.Cleanup(func() {
		userHome, loadCatalog, runner, recipes.ToolInstalled = oldHome, oldCat, oldRunner, oldTool
		cliMu.Lock()
		cliFindings = map[string][]recipes.Finding{}
		cliMu.Unlock()
	})
	return home
}

func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := NewRootCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	// The detail views are sections of `macdoc doctor`.
	if len(args) > 0 {
		switch args[0] {
		case "wins", "dev", "explain", "storage", "space", "scan", "health", "slow", "trend":
			args = append([]string{"doctor"}, args...)
		}
	}
	cmd.SetArgs(args)
	err := cmd.Execute()
	return buf.String(), err
}

func mustContain(t *testing.T, out string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Errorf("output missing %q\n---\n%s", w, out)
		}
	}
}

func TestWinsCommand(t *testing.T) {
	cliFixture(t)
	out, err := run(t, "wins")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("sample wins output:\n%s", out)
	mustContain(t, out,
		"macdoc doctor wins · scan S-", "· 2h ago",
		" Free & safe ─", "3.0 GB\n",
		"npm cache", "re-downloads on demand", "→ npm cache clean --force",
		" Worth considering ─", "15.0 GB",
		"5 × Stale node_modules folder · 15.0 GB", "(macdoc doctor wins --all)",
		" Your call (advice only) ─", "size unknown", "iPhone backup (Mar 2025)", "Finder › iPhone › Manage Backups",
		" macdoc doctor explain <id> · macdoc plan <id…> (a dry run) · nothing has been changed")
	// Sections appear in tier order.
	if strings.Index(out, "Free & safe") > strings.Index(out, "Worth considering") ||
		strings.Index(out, "Worth considering") > strings.Index(out, "Your call") {
		t.Error("sections out of order")
	}
	if strings.Contains(out, "Needs care") {
		t.Error("empty tier shown")
	}
	if strings.Contains(out, "_cacache") || strings.Contains(out, "0 B") {
		t.Error("single item got an instance suffix, or unknown shown as 0")
	}
	if !strings.Contains(out, "macdoc plan <id…> (a dry run)") || strings.Contains(out, "macdoc apply") {
		t.Error("wins should point to plan (Phase 5), never to apply")
	}

	out, err = run(t, "wins", "--all")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, "Stale node_modules folder · e", "Stale node_modules folder · a")
	if strings.Contains(out, "5 ×") {
		t.Error("--all must not group")
	}

	out, err = run(t, "wins", "--tier", "advise")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "Free & safe") || !strings.Contains(out, "Your call") {
		t.Errorf("--tier advise:\n%s", out)
	}
	out, err = run(t, "wins", "--tier", "risky")
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, out, `Nothing in "Needs care"`)

	if _, err := run(t, "wins", "--tier", "bogus"); err == nil || !strings.Contains(err.Error(), "unknown tier") {
		t.Errorf("err = %v", err)
	}
}

func TestWinsJSON(t *testing.T) {
	cliFixture(t)
	out, err := run(t, "wins", "--json", "--tier", "caution")
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		SchemaVersion string                   `json:"schema_version"`
		Command       string                   `json:"command"`
		Data          engine.OpportunityResult `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("bad JSON: %v\n%s", err, out)
	}
	if env.SchemaVersion != model.SchemaVersion || env.Command != "wins" || len(env.Data.Opportunities) != 5 ||
		env.Data.Totals[model.RiskCaution] != 15*gb || len(env.Data.Totals) != 1 {
		t.Errorf("envelope = %+v", env)
	}
	for _, op := range env.Data.Opportunities {
		if !strings.HasPrefix(op.ID, "O-") || op.Explain == "" || op.Restore.Kind == "" {
			t.Errorf("op = %+v", op)
		}
	}
}

func TestEnvelopeWarnings(t *testing.T) {
	home := cliFixture(t)
	// A target outside the recipe's allow patterns becomes a warning.
	cliMu.Lock()
	cliFindings["node.nm"] = append(cliFindings["node.nm"], recipes.Finding{Target: filepath.Join(home, "other", "node_modules"), Bytes: measured(gb)})
	cliMu.Unlock()
	out, err := run(t, "wins", "--all")
	if err != nil {
		t.Fatal(err)
	}
	id := regexpID.FindString(out)
	for _, args := range [][]string{{"wins"}, {"dev"}, {"storage"}, {"explain", id}} {
		out, err := run(t, append(args, "--json")...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		var env struct {
			Command  string   `json:"command"`
			Warnings []string `json:"warnings"`
		}
		if err := json.Unmarshal([]byte(out), &env); err != nil {
			t.Fatalf("%v: bad JSON: %v", args, err)
		}
		if env.Command != args[0] || !strings.Contains(strings.Join(env.Warnings, "\n"), "node.nm: skipped") {
			t.Errorf("%v: top-level warnings = %q", args, env.Warnings)
		}
	}
	// Commands without warnings omit the field; doctor and version still work.
	for _, args := range [][]string{{"version", "--json"}, {"doctor", "--json"}} {
		out, err := run(t, args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if !strings.Contains(out, `"schema_version": "1"`) {
			t.Errorf("%v: %s", args, out)
		}
	}
	out, _ = run(t, "version", "--json")
	if strings.Contains(out, `"warnings"`) {
		t.Errorf("version has warnings: %s", out)
	}
}

var regexpID = regexp.MustCompile(`O-[a-z2-7]{6}`)

func TestWinsNoScanAndWarnings(t *testing.T) {
	home := cliFixture(t)
	// Another home without a scan.
	empty := filepath.Join(home, "empty")
	if err := os.MkdirAll(empty, 0o700); err != nil {
		t.Fatal(err)
	}
	userHome = func() (string, error) { return empty, nil }
	for _, args := range [][]string{{"wins"}, {"dev"}, {"explain", "O-abcdef"}, {"storage"}} {
		if _, err := run(t, args...); !errors.Is(err, engine.ErrNoScan) {
			t.Errorf("%v: err = %v", args, err)
		}
	}
	userHome = func() (string, error) { return "", errors.New("no home") }
	for _, args := range [][]string{{"wins"}, {"storage"}} {
		if _, err := run(t, args...); err == nil {
			t.Errorf("%v: expected home error", args)
		}
	}
	userHome = func() (string, error) { return home, nil }
	loadCatalog = func() (*recipes.Catalog, error) { return nil, errors.New("bad catalog") }
	for _, args := range [][]string{{"wins"}, {"storage"}} {
		if _, err := run(t, args...); err == nil || !strings.Contains(err.Error(), "bad catalog") {
			t.Errorf("%v: err = %v", args, err)
		}
	}
}

func sampleWins() (engine.OpportunityResult, *recipes.Catalog) {
	ops := []model.Opportunity{
		{ID: "O-ab12cd", RecipeID: "xcode.dd", Title: "Xcode DerivedData · MyApp", Bytes: measured(14_200_000_000), Risk: model.RiskSafe,
			Method: model.MethodQuarantine, Restore: model.Restore{Kind: model.RestoreRebuild}},
		{ID: "O-k3m9pq", RecipeID: "docker.bc", Title: "Docker build cache", Bytes: model.Bytes{Value: 11_000_000_000, Source: model.SourceEstimated}, Risk: model.RiskSafe,
			Method: model.MethodNative, Command: []string{"docker", "builder", "prune", "-f"}, Restore: model.Restore{Kind: model.RestoreRebuild}},
		{ID: "O-z4n1ty", RecipeID: "android.img", Title: "Android system image API 29", Bytes: measured(8_400_000_000), Risk: model.RiskRisky,
			Method: model.MethodNative, Command: []string{"sdkmanager", "--uninstall", "system-images;android-29"}, Restore: model.Restore{Kind: model.RestoreRedownload, RedownloadBytes: 1_200_000_000}},
		{ID: "O-c6h5ju", RecipeID: "misc", Title: "Something odd", Bytes: model.Bytes{Source: model.SourceUnknown}, Risk: model.RiskRisky,
			Method: model.MethodQuarantine, Restore: model.Restore{Kind: model.RestoreNone}},
		{ID: "O-qq11ww", RecipeID: "misc2", Title: "Lost data", Bytes: measured(1), Risk: model.RiskAdvise,
			Method: model.MethodAdvise, Restore: model.Restore{Kind: model.RestoreIrreplaceable}},
	}
	res := engine.OpportunityResult{Scan: model.ScanInfo{ID: "S-TEST"}, ScanAge: "2h ago", Opportunities: ops,
		Totals:   map[model.Risk]int64{model.RiskSafe: 25_200_000_000, model.RiskRisky: 8_400_000_000, model.RiskAdvise: 1},
		Warnings: []string{"results are from a scan 9 hours old; run \"macdoc scan\" to refresh"}}
	return res, &recipes.Catalog{}
}

func TestRenderWins(t *testing.T) {
	res, cat := sampleWins()
	var buf bytes.Buffer
	if err := renderWins(&buf, res, cat, false, ""); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	t.Logf("rendered:\n%s", out)
	mustContain(t, out,
		" Free & safe ─", "25.2 GB",
		"  O-ab12cd   14.2 GB  Xcode DerivedData · MyApp", "rebuilds on next build",
		"~11.0 GB", "→ docker builder prune -f",
		" Needs care ─", "8.4 GB + unknown", "re-download ~1.2 GB", "—  ",
		"can't be restored",
		" Notes\n  · results are from a scan 9 hours old",
		" macdoc doctor explain <id> · macdoc plan <id…> (a dry run) · nothing has been changed")

	buf.Reset()
	empty := engine.OpportunityResult{Scan: model.ScanInfo{ID: "S-X"}, ScanAge: "just now"}
	if err := renderWins(&buf, empty, nil, false, ""); err != nil {
		t.Fatal(err)
	}
	mustContain(t, buf.String(), "Nothing to reclaim was found")
}

func TestHelpers(t *testing.T) {
	if got := recipeTitle(nil, "x.y"); got != "x.y" {
		t.Error(got)
	}
	if got := restoreNote(model.Opportunity{Restore: model.Restore{Kind: model.RestoreNone}}); got != "" {
		t.Errorf("restoreNote = %q", got)
	}
	if got := restoreNote(model.Opportunity{Restore: model.Restore{Kind: model.RestoreRebuild, Note: strings.Repeat("x", 60)}, Method: model.MethodAdvise}); len([]rune(got)) != 40 {
		t.Errorf("advise note not truncated: %q", got)
	}
	if h := sectionHeader(strings.Repeat("n", 80), "1 GB"); !strings.Contains(h, "───") {
		t.Errorf("header = %q", h)
	}
	for _, s := range []string{"safe", "CAUTION", "Risky", "advise"} {
		if _, err := parseTier(s); err != nil {
			t.Errorf("%s: %v", s, err)
		}
	}
}

// TestRealWins runs against the real latest scan. Opt in with
// MACDOC_REAL_WINS=1 (read-only; never scans).
func TestRealWins(t *testing.T) {
	if os.Getenv("MACDOC_REAL_WINS") != "1" {
		t.Skip("set MACDOC_REAL_WINS=1 to run against the real latest scan")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	cat, err := recipes.Load()
	if err != nil {
		t.Fatal(err)
	}
	res, err := engine.Opportunities(context.Background(), engine.OpportunityOptions{Home: home, Runner: platform.ExecRunner{}, Catalog: cat})
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := renderWins(&buf, res, cat, false, ""); err != nil {
		t.Fatal(err)
	}
	t.Logf("\n%s", buf.String())
}
