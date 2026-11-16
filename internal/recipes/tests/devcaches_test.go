package tests

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/platform"
	"github.com/sagnik26/macdoc/internal/precond"
	"github.com/sagnik26/macdoc/internal/recipes"
	"github.com/sagnik26/macdoc/internal/safety"
)

// cacheCase is one new path-based cache recipe: the folders it owns, the
// look-alike folders it must never list, and the app whose precondition it
// carries (empty if none).
type cacheCase struct {
	id, app    string
	own, other []string
}

const (
	supportDir = "/Library/Application Support"
	cachesDir  = "/Library/Caches"
)

func newCacheCases() []cacheCase {
	sup := w2Home + supportDir
	cs := []cacheCase{
		{"browser.safari", "Safari", []string{w2Home + cachesDir + "/com.apple.Safari"}, []string{w2Home + cachesDir + "/com.apple.Safari.SafeBrowsing", w2Home + supportDir + "/com.apple.Safari"}},
		{"browser.chrome", "Google Chrome", []string{w2Home + cachesDir + "/Google/Chrome"}, []string{w2Home + cachesDir + "/Google/Chrome-headless", w2Home + cachesDir + "/Google/AndroidStudio2024", sup + "/Google/Chrome"}},
		{"browser.edge", "Microsoft Edge", []string{w2Home + cachesDir + "/Microsoft Edge"}, []string{w2Home + cachesDir + "/Microsoft Edge Beta", sup + "/Microsoft Edge"}},
		{"browser.brave", "Brave Browser", []string{w2Home + cachesDir + "/BraveSoftware"}, []string{w2Home + cachesDir + "/BraveSoftware-Nightly", sup + "/BraveSoftware"}},
		{"browser.arc", "Arc", []string{w2Home + cachesDir + "/company.thebrowser.Browser"}, []string{w2Home + cachesDir + "/company.thebrowser.dia", sup + "/Arc"}},
		{"browser.firefox", "Firefox", []string{w2Home + cachesDir + "/Firefox"}, []string{w2Home + cachesDir + "/Firefox-nightly", sup + "/Firefox"}},
		{"go.build_cache", "", []string{w2Home + cachesDir + "/go-build"}, []string{w2Home + cachesDir + "/goimports"}},
		{"cargo.registry_cache", "", []string{w2Home + "/.cargo/registry/cache"}, []string{w2Home + "/.cargo/registry/src", w2Home + "/.cargo/bin"}},
		{"bun.cache", "", []string{w2Home + "/.bun/install/cache"}, []string{w2Home + "/.bun/install/global", w2Home + "/.bun/bin"}},
		{"deno.cache", "", []string{w2Home + cachesDir + "/deno"}, []string{w2Home + cachesDir + "/denoland"}},
		{"composer.cache", "", []string{w2Home + cachesDir + "/composer"}, []string{w2Home + cachesDir + "/composer-old"}},
		{"swiftpm.cache", "", []string{w2Home + cachesDir + "/org.swift.swiftpm"}, []string{w2Home + cachesDir + "/org.swift.swiftpm.other"}},
		{"nodegyp.cache", "", []string{w2Home + cachesDir + "/node-gyp"}, []string{w2Home + cachesDir + "/node-gyp-extra"}},
		{"precommit.cache", "", []string{w2Home + "/.cache/pre-commit"}, []string{w2Home + "/.cache/pre-commit-old", w2Home + "/.cache/pip"}},
	}
	for _, e := range []struct{ id, app, dir string }{{"vscode.cache", "Visual Studio Code", "Code"}, {"cursor.cache", "Cursor", "Cursor"}} {
		root := sup + "/" + e.dir
		cs = append(cs, cacheCase{e.id, e.app,
			[]string{root + "/Cache", root + "/CachedData", root + "/Code Cache", root + "/GPUCache"},
			[]string{root + "/User", root + "/Backups", root + "/User/workspaceStorage", root + "/Cookies", root + "/Local Storage", root + "/CachedExtensionVSIXs"}})
	}
	return cs
}

func nodesOf(paths ...[]string) fakeNodes {
	var ns fakeNodes
	for _, ps := range paths {
		for _, p := range ps {
			ns = append(ns, model.Node{Path: p, Bytes: 1 << 20, MtimeMax: w2DaysAgo(1)})
		}
	}
	return ns
}

func TestNewCacheRecipesPositive(t *testing.T) {
	for _, c := range newCacheCases() {
		fs, err := w2Run(t, c.id, recipes.Env{Nodes: nodesOf(c.own)})
		w2WantTargets(t, fs, err, c.own...)
	}
}

func TestNewCacheRecipesLookAlikeNegative(t *testing.T) {
	for _, c := range newCacheCases() {
		fs, err := w2Run(t, c.id, recipes.Env{Nodes: nodesOf(c.other)})
		if err != nil || len(fs) != 0 {
			t.Errorf("%s listed look-alikes: %v, %v", c.id, w2Targets(fs), err)
		}
		// Mixed: only the owned folders come back.
		fs, err = w2Run(t, c.id, recipes.Env{Nodes: nodesOf(c.own, c.other)})
		w2WantTargets(t, fs, err, c.own...)
		// Scan kept nothing under that name: nothing listed.
		fs, err = w2Run(t, c.id, recipes.Env{Nodes: fakeNodes{}})
		w2WantNone(t, fs, err)
	}
}

// TestNewCacheRecipesAllowOnlyOwn checks, with CheckTarget on a real fixture
// tree, that the allow patterns accept the cache and refuse every look-alike.
func TestNewCacheRecipesAllowOnlyOwn(t *testing.T) {
	home := t.TempDir()
	pol, err := safety.NewPolicy(home, nil)
	if err != nil {
		t.Fatal(err)
	}
	re := func(p string) string { return filepath.Join(home, strings.TrimPrefix(p, w2Home)) }
	for _, c := range newCacheCases() {
		r := w2Recipe(t, c.id)
		for _, p := range append(slices.Clone(c.own), c.other...) {
			if err := os.MkdirAll(re(p), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		for _, p := range c.own {
			if err := pol.CheckTarget(re(p), r.Allow); err != nil {
				t.Errorf("%s: own path %s refused: %v", c.id, p, err)
			}
		}
		for _, p := range c.other {
			if err := pol.CheckTarget(re(p), r.Allow); err == nil {
				t.Errorf("%s: look-alike %s accepted", c.id, p)
			}
		}
	}
}

func TestNewCacheRecipesShape(t *testing.T) {
	for _, c := range newCacheCases() {
		r := w2Recipe(t, c.id)
		if r.Risk != model.RiskSafe || r.Method != model.MethodQuarantine || r.Toolchain != "system" {
			t.Errorf("%s: risk/method/toolchain = %s/%s/%s", c.id, r.Risk, r.Method, r.Toolchain)
		}
		if r.Restore.Kind == model.RestoreNone || r.Restore.Kind == model.RestoreIrreplaceable || r.Explain == "" {
			t.Errorf("%s: needs a rebuild or redownload restore and an explain", c.id)
		}
		var want []string
		if c.app != "" {
			want = []string{"app_not_running:" + c.app}
		}
		if !slices.Equal(r.Preconditions, want) {
			t.Errorf("%s: preconditions = %v, want %v", c.id, r.Preconditions, want)
		}
		if len(r.Allow) != len(c.own) {
			t.Errorf("%s: %d allow patterns for %d folders", c.id, len(r.Allow), len(c.own))
		}
		for i, a := range r.Allow {
			if strings.ContainsAny(a, "*?[") || !slices.Contains(c.own, recipes.ExpandHome(w2Home, a)) {
				t.Errorf("%s: allow %q [%d] is not exactly an owned folder", c.id, a, i)
			}
		}
	}
}

// TestNewCacheRecipesPreconditionFails runs the real checker against a fake
// process list: the app open fails the item, the app closed passes it.
func TestNewCacheRecipesPreconditionFails(t *testing.T) {
	const psKey = "ps -axww -o command="
	for _, c := range newCacheCases() {
		if c.app == "" {
			continue
		}
		r := w2Recipe(t, c.id)
		item := model.PlanItem{Preconditions: r.Preconditions}
		run := func(ps string) error {
			f := &platform.Fake{Responses: map[string]platform.Result{psKey: {Stdout: []byte(ps)}}}
			return (&precond.Checker{Runner: f, Home: t.TempDir()}).Check(context.Background(), item)
		}
		if run("/Applications/"+c.app+".app/Contents/MacOS/"+c.app+"\n") == nil {
			t.Errorf("%s: passed while %s is running", c.id, c.app)
		}
		if err := run("/usr/bin/vim notes.txt\n/Applications/Other.app/Contents/MacOS/Other\n"); err != nil {
			t.Errorf("%s: failed with %s closed: %v", c.id, c.app, err)
		}
	}
}

// TestNewCacheRecipesNeverMatchUserData checks that no new recipe's allow
// pattern can match profiles, history, cookies, logins or settings.
func TestNewCacheRecipesNeverMatchUserData(t *testing.T) {
	home := t.TempDir()
	pol, err := safety.NewPolicy(home, nil)
	if err != nil {
		t.Fatal(err)
	}
	sup := filepath.Join(home, "Library/Application Support")
	var secrets []string
	for _, app := range []string{"Google/Chrome", "Microsoft Edge", "BraveSoftware/Brave-Browser", "Arc", "Firefox", "Code", "Cursor", "Safari"} {
		for _, leaf := range []string{"Default", "Profiles", "Cookies", "History", "Login Data", "Bookmarks", "Local Storage", "User", "Preferences"} {
			secrets = append(secrets, filepath.Join(sup, app, leaf), filepath.Join(sup, app, "Default", leaf))
		}
		secrets = append(secrets, filepath.Join(sup, app))
	}
	secrets = append(secrets, filepath.Join(home, "Library/Safari/History.db"), filepath.Join(home, "Library/Safari/Bookmarks.plist"),
		filepath.Join(home, "Library/Caches/JetBrains"), filepath.Join(home, "Library/Caches/JetBrains/IntelliJIdea2024.1/LocalHistory"))
	for _, p := range secrets {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range newCacheCases() {
		r := w2Recipe(t, c.id)
		for _, p := range secrets {
			if err := pol.CheckTarget(p, r.Allow); err == nil {
				t.Errorf("%s: allow patterns %v accept %s", c.id, r.Allow, p)
			}
		}
	}
	// The same must hold for the JetBrains logs recipe.
	r := w2Recipe(t, "jetbrains.logs")
	for _, p := range secrets {
		if err := pol.CheckTarget(p, r.Allow); err == nil {
			t.Errorf("jetbrains.logs accepts %s", p)
		}
	}
}

func TestJetBrainsLogs(t *testing.T) {
	logs := w2Home + "/Library/Logs/JetBrains"
	nodes := fakeNodes{
		{Path: logs, Bytes: 300},
		{Path: logs + "/IntelliJIdea2024.1", Bytes: 100, MtimeMax: w2DaysAgo(90)},
		{Path: logs + "/GoLand2024.2", Bytes: 100, MtimeMax: w2DaysAgo(2)},
		{Path: w2Home + "/Library/Caches/JetBrains/IntelliJIdea2024.1/LocalHistory", Bytes: 1 << 30},
		{Path: w2Home + "/Library/Caches/JetBrains", Bytes: 1 << 30},
		{Path: w2Home + "/Library/Logs/Other/x", Bytes: 100},
	}
	fs, err := w2Run(t, "jetbrains.logs", recipes.Env{Nodes: nodes})
	if err != nil {
		t.Fatal(err)
	}
	// The detector lists direct children; staleness (30 days) is applied by
	// the engine from stale_days.
	w2WantTargets(t, fs, nil, logs+"/IntelliJIdea2024.1", logs+"/GoLand2024.2")
	r := w2Recipe(t, "jetbrains.logs")
	if r.StaleDays != 30 || r.Risk != model.RiskSafe {
		t.Errorf("stale_days=%d risk=%s", r.StaleDays, r.Risk)
	}
}

// TestGenericCachesDoNotListOwnedFolders checks user.app_caches skips every
// folder a new recipe owns, so nothing is listed twice.
func TestGenericCachesDoNotListOwnedFolders(t *testing.T) {
	var ns fakeNodes
	skipped := []string{"go-build", "deno", "composer", "org.swift.swiftpm", "node-gyp", "Microsoft Edge", "BraveSoftware", "company.thebrowser.Browser", "Firefox", "Google", "JetBrains"}
	for _, n := range skipped {
		ns = append(ns, model.Node{Path: w2Home + cachesDir + "/" + n, Bytes: 1 << 30, MtimeMax: w2DaysAgo(60)})
	}
	keep := w2Home + cachesDir + "/SomeOtherApp"
	ns = append(ns, model.Node{Path: keep, Bytes: 1 << 30, MtimeMax: w2DaysAgo(60)})
	fs, err := w2Run(t, "user.app_caches", recipes.Env{Nodes: ns})
	w2WantTargets(t, fs, err, keep)
}

// Folders a tool runs from are not caches: Playwright, Cypress, Puppeteer and
// Camoufox keep their downloaded browsers under ~/Library/Caches, and the
// generic app-cache recipe must leave them alone.
func TestAppCachesLeaveToolRuntimesAlone(t *testing.T) {
	c := w2Home + "/Library/Caches"
	nodes := fakeNodes{
		{Path: c, Bytes: 5 << 30},
		{Path: c + "/ms-playwright", Bytes: 600 << 20, MtimeMax: w2DaysAgo(60)},
		{Path: c + "/ms-playwright-go", Bytes: 600 << 20, MtimeMax: w2DaysAgo(60)},
		{Path: c + "/Cypress", Bytes: 600 << 20, MtimeMax: w2DaysAgo(60)},
		{Path: c + "/puppeteer", Bytes: 600 << 20, MtimeMax: w2DaysAgo(60)},
		{Path: c + "/camoufox", Bytes: 600 << 20, MtimeMax: w2DaysAgo(60)},
		{Path: c + "/com.example.chat", Bytes: 600 << 20, MtimeMax: w2DaysAgo(60)}, // an ordinary app cache
	}
	fs, err := w2Run(t, "user.app_caches", recipes.Env{Nodes: nodes})
	w2WantTargets(t, fs, err, c+"/com.example.chat")
}

// regenerable is only for SAFE recipes, and the shipped catalog uses it only
// on caches: every flagged recipe is SAFE, and every flagged browser or editor
// recipe still needs its app to be closed.
func TestRegenerableFlagIsLimited(t *testing.T) {
	bad := validRecipe()
	bad.Risk, bad.Regenerable = model.RiskCaution, true
	if err := recipes.Validate(bad); err == nil || !strings.Contains(err.Error(), "regenerable") {
		t.Errorf("regenerable on a CAUTION recipe was accepted: %v", err)
	}
	c, err := recipes.Load()
	if err != nil {
		t.Fatal(err)
	}
	flagged := 0
	for _, r := range c.Recipes {
		if !r.Regenerable {
			continue
		}
		flagged++
		if r.Risk != model.RiskSafe {
			t.Errorf("%s is regenerable but %s", r.ID, r.Risk)
		}
		if (strings.HasPrefix(r.ID, "browser.") || r.ID == "vscode.cache" || r.ID == "cursor.cache") && len(r.Preconditions) == 0 {
			t.Errorf("%s is regenerable but doesn't require its app to be closed", r.ID)
		}
		if r.ID == "jetbrains.logs" {
			t.Errorf("%s is logs, not a cache", r.ID)
		}
	}
	if flagged < 15 {
		t.Errorf("only %d regenerable recipes; the cache recipes lost their flag", flagged)
	}
}
