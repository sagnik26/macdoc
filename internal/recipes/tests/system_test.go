package tests

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/sagnik26/macdoc/internal/recipes"

	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/platform"
)

// Shared helpers for the P3-W2 detector tests.

const w2Home = "/Users/me"

var w2Now = time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)

// daysAgo returns a unix mtime d days before w2Now.
func w2DaysAgo(d int) int64 { return w2Now.Add(-time.Duration(d) * 24 * time.Hour).Unix() }

// w2Recipe returns the embedded catalog recipe id.
func w2Recipe(t *testing.T, id string) recipes.Recipe {
	t.Helper()
	c, err := recipes.Load()
	if err != nil {
		t.Fatal(err)
	}
	r, ok := c.Get(id)
	if !ok {
		t.Fatalf("recipe %s not in catalog", id)
	}
	return r
}

// runRecipe runs the registered detector of catalog recipe id.
func w2Run(t *testing.T, id string, env recipes.Env) ([]recipes.Finding, error) {
	t.Helper()
	r := w2Recipe(t, id)
	d, ok := recipes.Lookup(r.Detector)
	if !ok {
		t.Fatalf("detector %s not registered", r.Detector)
	}
	if env.Now.IsZero() {
		env.Now = w2Now
	}
	if env.Home == "" {
		env.Home = w2Home
	}
	return d(context.Background(), env, r)
}

func w2Fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// withTools makes recipes.ToolInstalled report exactly the given tools.
func w2WithTools(t *testing.T, tools ...string) {
	t.Helper()
	old := recipes.ToolInstalled
	recipes.ToolInstalled = func(n string) bool { return slices.Contains(tools, n) }
	t.Cleanup(func() { recipes.ToolInstalled = old })
}

func w2Fake(responses map[string]platform.Result) *platform.Fake {
	return &platform.Fake{Responses: responses}
}

func w2Targets(fs []recipes.Finding) []string {
	var out []string
	for _, f := range fs {
		out = append(out, f.Target)
	}
	slices.Sort(out)
	return out
}

func w2WantTargets(t *testing.T, fs []recipes.Finding, err error, want ...string) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(want)
	if got := w2Targets(fs); !slices.Equal(got, want) {
		t.Fatalf("targets = %q, want %q", got, want)
	}
}

func w2WantNone(t *testing.T, fs []recipes.Finding, err error) {
	t.Helper()
	if err != nil || fs != nil {
		t.Fatalf("want nil, nil; got %+v, %v", fs, err)
	}
}

func TestLoadIncludesW2Recipes(t *testing.T) {
	ids := []string{
		"node.node_modules_stale", "npm.cache", "pnpm.store", "yarn.cache", "nvm.old_versions",
		"brew.cleanup", "brew.autoremove",
		"docker.build_cache", "docker.dangling_images", "docker.unused_images",
		"docker.stopped_containers", "docker.volumes", "docker.raw_disk",
		"pip.cache", "uv.cache", "poetry.cache", "conda.pkgs", "python.venv_stale",
		"ollama.model", "hf.cache_repo", "lmstudio.model",
		"user.app_caches", "user.logs", "user.trash", "user.downloads_large_old",
		"ios.device_backups", "tm.local_snapshots",
	}
	for _, id := range ids {
		r := w2Recipe(t, id)
		if r.Restore.Kind == "" || r.Explain == "" {
			t.Errorf("%s: missing restore or explain", id)
		}
	}
}

// Corepack shims download binaries when queried, so no detector may run
// pnpm, yarn or npm.
func TestNoPackageManagerQueries(t *testing.T) {
	w2WithTools(t, "npm", "pnpm", "yarn")
	runner := w2Fake(nil)
	nodes := fakeNodes{
		{Path: w2Home + "/.npm/_cacache", Bytes: 1 << 30},
		{Path: w2Home + "/Library/pnpm/store", Bytes: 1 << 30},
		{Path: w2Home + "/Library/Caches/Yarn", Bytes: 1 << 30},
	}
	for _, id := range []string{"npm.cache", "pnpm.store", "yarn.cache"} {
		fs, err := w2Run(t, id, recipes.Env{Nodes: nodes, Runner: runner})
		// pnpm store prune frees an unknown part, so its size isn't claimed.
		want := model.SourceMeasured
		if id == "pnpm.store" {
			want = model.SourceUnknown
		}
		if err != nil || len(fs) != 1 || fs[0].Bytes.Source != want {
			t.Errorf("%s = %+v, %v", id, fs, err)
		}
	}
	if len(runner.Calls) != 0 {
		t.Fatalf("ran %v", runner.Calls)
	}
}

func TestPathRecipes(t *testing.T) {
	nodes := fakeNodes{
		{Path: w2Home + "/Library/Caches/pip", Bytes: 300 << 20},
		{Path: w2Home + "/.cache/uv", Bytes: 200 << 20},
		{Path: w2Home + "/Library/Caches/pypoetry", Bytes: 150 << 20},
		{Path: w2Home + "/Library/Containers/com.docker.docker/Data/vms/0/data/Docker.raw", Kind: model.NodeFile, Bytes: 20 << 30},
		{Path: w2Home + "/Library/Caches/pip-tools", Bytes: 500 << 20}, // look-alike
	}
	for id, want := range map[string]string{
		"pip.cache":       "/Library/Caches/pip",
		"uv.cache":        "/.cache/uv",
		"poetry.cache":    "/Library/Caches/pypoetry",
		"docker.raw_disk": "/Library/Containers/com.docker.docker/Data/vms/0/data/Docker.raw",
	} {
		fs, err := w2Run(t, id, recipes.Env{Nodes: nodes})
		w2WantTargets(t, fs, err, w2Home+want)
	}
}

// The Trash lists the items in it, not the folder itself, and nothing that only
// looks like it.
func TestTrashItems(t *testing.T) {
	nodes := fakeNodes{
		{Path: w2Home + "/.Trash", Bytes: 3 << 30},
		{Path: w2Home + "/.Trash/big.dmg", Kind: model.NodeFile, Bytes: 2 << 30},
		{Path: w2Home + "/.Trash/old project", Bytes: 1 << 30},
		{Path: w2Home + "/.Trash-old/other.dmg", Kind: model.NodeFile, Bytes: 1 << 30},
		{Path: w2Home + "/Library/.Trash/x", Bytes: 1 << 30},
	}
	fs, err := w2Run(t, "user.trash", recipes.Env{Nodes: nodes})
	w2WantTargets(t, fs, err, w2Home+"/.Trash/big.dmg", w2Home+"/.Trash/old project")
}

// Only the small state files directly in CrashReporter that have not changed
// for 30 days are listed.
func TestCrashReporterFiles(t *testing.T) {
	d := w2Home + "/Library/Application Support/CrashReporter"
	nodes := fakeNodes{
		{Path: d + "/Intervals_AAAA.plist", Kind: model.NodeFile, Bytes: 4096, MtimeMax: w2DaysAgo(90)},
		{Path: d + "/Preview_AAAA.plist", Kind: model.NodeFile, Bytes: 2048, MtimeMax: w2DaysAgo(45)},
		{Path: d + "/Intervals_NEW.plist", Kind: model.NodeFile, Bytes: 4096, MtimeMax: w2DaysAgo(2)}, // recent
		{Path: d + "/old/deep.plist", Kind: model.NodeFile, Bytes: 4096, MtimeMax: w2DaysAgo(90)},     // not directly in the folder
		{Path: d + "/folder", Bytes: 4096, MtimeMax: w2DaysAgo(90)},                                   // a folder
		{Path: d + "/setup.dmg", Kind: model.NodeFile, Bytes: 4096, MtimeMax: w2DaysAgo(90)},          // installers are another recipe
		{Path: w2Home + "/Library/Application Support/CrashReporter-old/x.plist", Kind: model.NodeFile, Bytes: 4096, MtimeMax: w2DaysAgo(90)},
		{Path: w2Home + "/Library/Application Support/Other/x.plist", Kind: model.NodeFile, Bytes: 4096, MtimeMax: w2DaysAgo(90)},
	}
	fs, err := w2Run(t, "user.crash_reporter", recipes.Env{Nodes: nodes})
	w2WantTargets(t, fs, err, d+"/Intervals_AAAA.plist", d+"/Preview_AAAA.plist")
	checkTargets(t, w2Home, "user.crash_reporter", fs)
}

func TestAppCaches(t *testing.T) {
	c := w2Home + "/Library/Caches"
	nodes := fakeNodes{
		{Path: c, Bytes: 10 << 30},
		{Path: c + "/com.spotify.client", Bytes: 2 << 30, MtimeMax: w2DaysAgo(1)},
		{Path: c + "/Google", Bytes: 600 << 20},
		{Path: c + "/com.apple.Safari", Bytes: 3 << 30},                      // Apple
		{Path: c + "/COM.APPLE.bird", Bytes: 3 << 30},                        // Apple, case-insensitive
		{Path: c + "/com.apple.dt.Xcode", Bytes: 3 << 30},                    // xcode.caches
		{Path: c + "/Yarn", Bytes: 3 << 30},                                  // yarn.cache
		{Path: c + "/pip", Bytes: 3 << 30},                                   // pip.cache
		{Path: c + "/pypoetry", Bytes: 3 << 30},                              // poetry.cache
		{Path: c + "/CocoaPods", Bytes: 3 << 30},                             // cocoapods.cache
		{Path: c + "/Homebrew", Bytes: 3 << 30},                              // brew.cleanup
		{Path: c + "/com.small.app", Bytes: 40 << 20},                        // below 50 MB
		{Path: c + "/com.mid.app", Bytes: 400 << 20, MtimeMax: w2DaysAgo(1)}, // listed since the threshold is 50 MB
		{Path: c + "/big.bin", Kind: model.NodeFile, Bytes: 1 << 30},         // a file, not an app cache
		{Path: c + "/com.spotify.client/Data", Bytes: 2 << 30},               // grandchild
	}
	fs, err := w2Run(t, "user.app_caches", recipes.Env{Nodes: nodes})
	// Google is excluded since P3-W4 D10 (android_studio.old_versions_data
	// owns Caches/Google/AndroidStudio*).
	w2WantTargets(t, fs, err, c+"/com.spotify.client", c+"/com.mid.app")
	for _, f := range fs {
		if f.Title == "" || f.Bytes.Source != model.SourceMeasured {
			t.Errorf("finding %+v", f)
		}
	}
	fs, err = w2Run(t, "user.app_caches", recipes.Env{})
	w2WantNone(t, fs, err)
}

func TestDownloadsLargeOld(t *testing.T) {
	d := w2Home + "/Downloads"
	nodes := fakeNodes{
		{Path: d, Bytes: 10 << 30},
		{Path: d + "/Xcode.xip", Kind: model.NodeFile, Bytes: 7 << 30, MtimeMax: w2DaysAgo(200)},
		{Path: d + "/small-old.dmg", Kind: model.NodeFile, Bytes: 400 << 20, MtimeMax: w2DaysAgo(200)},
		{Path: d + "/Upper.DMG", Kind: model.NodeFile, Bytes: 30 << 20, MtimeMax: w2DaysAgo(40)},      // extension is case-insensitive
		{Path: d + "/tiny.pkg", Kind: model.NodeFile, Bytes: 5 << 20, MtimeMax: w2DaysAgo(200)},       // below 20 MB
		{Path: d + "/recent.dmg", Kind: model.NodeFile, Bytes: 30 << 20, MtimeMax: w2DaysAgo(10)},     // newer than 30 days
		{Path: d + "/notes.dmg.txt", Kind: model.NodeFile, Bytes: 30 << 20, MtimeMax: w2DaysAgo(200)}, // look-alike: not an installer
		{Path: d + "/big-new.zip", Kind: model.NodeFile, Bytes: 2 << 30, MtimeMax: w2DaysAgo(10)},
		{Path: d + "/no-mtime.mov", Kind: model.NodeFile, Bytes: 2 << 30},
		{Path: d + "/Project", Bytes: 3 << 30, MtimeMax: w2DaysAgo(200)}, // a directory
		{Path: d + "/Project/huge.iso", Kind: model.NodeFile, Bytes: 3 << 30, MtimeMax: w2DaysAgo(200)},
	}
	fs, err := w2Run(t, "user.downloads_large_old", recipes.Env{Nodes: nodes})
	w2WantTargets(t, fs, err, d+"/Upper.DMG", d+"/Xcode.xip", d+"/small-old.dmg")
	fs, err = w2Run(t, "user.downloads_large_old", recipes.Env{})
	w2WantNone(t, fs, err)
}

func TestLogsAndBackups(t *testing.T) {
	l := w2Home + "/Library/Logs"
	b := w2Home + "/Library/Application Support/MobileSync/Backup"
	nodes := fakeNodes{
		{Path: l, Bytes: 300}, {Path: l + "/DiagnosticReports", Bytes: 200, MtimeMax: w2DaysAgo(40)},
		{Path: b, Bytes: 900}, {Path: b + "/00008110-ABC", Bytes: 900, MtimeMax: w2DaysAgo(5)},
	}
	fs, err := w2Run(t, "user.logs", recipes.Env{Nodes: nodes})
	w2WantTargets(t, fs, err, l+"/DiagnosticReports")
	fs, err = w2Run(t, "ios.device_backups", recipes.Env{Nodes: nodes})
	w2WantTargets(t, fs, err, b+"/00008110-ABC")
}

func TestLocalSnapshots(t *testing.T) {
	key := "tmutil listlocalsnapshots /"
	w2WithTools(t, "tmutil")
	fs, err := w2Run(t, "tm.local_snapshots", recipes.Env{Runner: w2Fake(map[string]platform.Result{key: {Stdout: w2Fixture(t, "w2_tmutil_snapshots.txt")}})})
	if err != nil || len(fs) != 1 || fs[0].Note != "2 local snapshots" || fs[0].Bytes.Source != model.SourceUnknown || fs[0].Bytes.Reason == "" {
		t.Fatalf("got %+v, %v", fs, err)
	}
	fs, err = w2Run(t, "tm.local_snapshots", recipes.Env{Runner: w2Fake(map[string]platform.Result{key: {Stdout: []byte("Snapshots for disk /:\n")}})})
	w2WantNone(t, fs, err)
	if _, err := w2Run(t, "tm.local_snapshots", recipes.Env{Runner: w2Fake(map[string]platform.Result{key: {ExitCode: 1, Stderr: []byte("boom")}})}); err == nil {
		t.Error("non-zero exit accepted")
	}
	if _, err := w2Run(t, "tm.local_snapshots", recipes.Env{Runner: &platform.Fake{Errors: map[string]error{key: errors.New("timeout")}}}); err == nil {
		t.Error("runner error swallowed")
	}
	fs, err = w2Run(t, "tm.local_snapshots", recipes.Env{})
	w2WantNone(t, fs, err)
	w2WithTools(t)
	fs, err = w2Run(t, "tm.local_snapshots", recipes.Env{Runner: w2Fake(nil)})
	w2WantNone(t, fs, err)
}

func TestParseSize(t *testing.T) {
	for in, want := range map[string]int64{
		"377MB": 377 << 20, "1.5GB (45%)": 1536 << 20, "0B": 0, "12 kB": 12 << 10,
		"1,024KB": 1 << 20, "2TB": 2 << 40, "5": 5, "1GiB": 1 << 30,
	} {
		if got, ok := recipes.ParseSize(in, 1024); !ok || got != want {
			t.Errorf("parseSize(%q) = %d, %v; want %d", in, got, ok, want)
		}
	}
	if got, _ := recipes.ParseSize("2.3GB", 1000); got != 2_300_000_000 {
		t.Errorf("decimal = %d", got)
	}
	for _, in := range []string{"", "GB", "1.2XB", "-1MB", "N/A"} {
		if _, ok := recipes.ParseSize(in, 1000); ok {
			t.Errorf("parseSize(%q) accepted", in)
		}
	}
}

func TestHelpers(t *testing.T) {
	if _, err := recipes.RequireParam(recipes.Recipe{ID: "x.y", Detector: "d"}, "dir"); err == nil {
		t.Error("missing param accepted")
	}
	env := recipes.Env{Now: w2Now}
	if !recipes.Stale(env, 0, 0) || recipes.Stale(env, 0, 30) || recipes.Stale(env, w2DaysAgo(29), 30) || !recipes.Stale(env, w2DaysAgo(30), 30) {
		t.Error("stale")
	}
	if recipes.BelowHome(w2Home, w2Home) != nil || recipes.BelowHome(w2Home, "/Users/meow/x") != nil || len(recipes.BelowHome(w2Home, w2Home+"/a/b")) != 2 {
		t.Error("belowHome")
	}
	if recipes.FirstLine([]byte("\n  \n x \n y")) != "x" || recipes.FirstLine(nil) != "" {
		t.Error("firstLine")
	}
	if recipes.Plural(1, "a", "as") != "1 a" || recipes.Plural(3, "a", "as") != "3 as" {
		t.Error("plural")
	}
	if _, err := recipes.RunDryRun(context.Background(), env, recipes.Recipe{ID: "x.y"}, time.Second); !errors.Is(err, recipes.ErrNoDryRun) {
		t.Errorf("runDryRun without dry-run argv = %v", err)
	}
	// Bad params in the system detectors.
	for _, d := range []recipes.Detector{recipes.DetectAppCaches, recipes.DetectDownloadsLargeOld} {
		if _, err := d(context.Background(), recipes.Env{Nodes: fakeNodes{}}, recipes.Recipe{ID: "x.y"}); err == nil {
			t.Error("missing dir accepted")
		}
		if _, err := d(context.Background(), recipes.Env{Nodes: fakeNodes{}}, recipes.Recipe{ID: "x.y", Params: map[string]string{"dir": "~/a/b", "min_bytes": "x"}}); err == nil {
			t.Error("bad min_bytes accepted")
		}
		if _, err := d(context.Background(), recipes.Env{Nodes: w2ErrNodes{}}, recipes.Recipe{ID: "x.y", Params: map[string]string{"dir": "~/a/b"}}); err == nil {
			t.Error("Nodes error swallowed")
		}
	}
}

// errNodes is a recipes.NodeSource that always fails.
type w2ErrNodes struct{}

func (w2ErrNodes) Nodes(string, int) ([]model.Node, error) { return nil, errors.New("db locked") }
