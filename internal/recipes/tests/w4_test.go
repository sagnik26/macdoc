package tests

// P3-W4 tests for the tech-lead decisions on the P3-R1 findings
// (docs/tasks/P3-W4.md, D1–D16).

import (
	"context"
	"errors"
	"github.com/sagnik26/macdoc/internal/recipes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/platform"
)

// D1: every allow-listed argv validates, nothing else does, and recipes.NativeTools
// is exactly the set of allow-listed programs.
func TestW4NativeArgvAllowList(t *testing.T) {
	var tools []string
	for _, argv := range recipes.NativeArgvAllowed {
		if err := recipes.Validate(r1Native(argv...)); err != nil {
			t.Errorf("%q rejected: %v", argv, err)
		}
		if !slices.Contains(tools, argv[0]) {
			tools = append(tools, argv[0])
		}
	}
	slices.Sort(tools)
	got := slices.Clone(recipes.NativeTools)
	slices.Sort(got)
	if !slices.Equal(got, tools) {
		t.Errorf("NativeTools = %q, allow-listed tools = %q", got, tools)
	}
	for _, argv := range [][]string{
		{"brew", "cleanup"},
		{"brew", "cleanup", "--prune=all", "-v"},
		{"docker", "image", "prune", "-f", "-a"},
		{"docker", "volume", "prune", "-f"},
		{"xcrun", "simctl", "delete", "all"},
		{"ollama", "rm"},
	} {
		err := recipes.Validate(r1Native(argv...))
		if err == nil || !strings.Contains(err.Error(), "native argv not in the allow-list") {
			t.Errorf("%q: %v", argv, err)
		}
	}
	// Every catalog native recipe uses an allow-listed argv.
	c, err := recipes.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range c.Recipes {
		if r.Native != nil && !recipes.ArgvIn(r.Native.Argv, recipes.NativeArgvAllowed) {
			t.Errorf("%s: argv %q", r.ID, r.Native.Argv)
		}
	}
}

// D2: dry_run_argv must be one of three exact read-only argvs, and
// recipes.RunDryRun refuses anything else.
func TestW4DryRunAllowList(t *testing.T) {
	for _, d := range recipes.DryRunArgvAllowed {
		r := r1Native("brew", "cleanup", "--prune=all")
		r.Native.DryRunArgv = d
		if err := recipes.Validate(r); err != nil {
			t.Errorf("%q rejected: %v", d, err)
		}
	}
	for _, d := range [][]string{
		{"brew", "cleanup", "-n", "--prune=all", "{id}"},
		{"brew", "autoremove"},
		{"conda", "clean", "--all", "-y"},
		{"ollama", "rm", "{id}"},
	} {
		r := r1Native("brew", "cleanup", "--prune=all")
		r.Native.DryRunArgv = d
		if err := recipes.Validate(r); err == nil || !strings.Contains(err.Error(), "dry-run argv not in the allow-list") {
			t.Errorf("%q: %v", d, err)
		}
	}
	runner := &platform.FakeRunner{}
	r := recipes.Recipe{ID: "x.y", Native: &recipes.Native{DryRunArgv: []string{"brew", "autoremove"}}}
	if _, err := recipes.RunDryRun(context.Background(), recipes.Env{Runner: runner}, r, time.Second); err == nil || len(runner.Calls) != 0 {
		t.Errorf("runDryRun ran %v, err %v", runner.Calls, err)
	}
}

// D3: the protected-path pattern lint.
func TestW4PatternLint(t *testing.T) {
	for _, p := range []string{
		"~/Library/Containers/com.*",
		"~/Library/Containers/[c]om.apple.x",
		"~/Library/Containers/com.apple.Notes/Data",
		"~/Library/Containers/*",
		"~/library/*/x/y",
		"~/.Docker/*",
		"~/.docker/config.json",
		"~/code/**/*",
		"~/code/x/*.db",
		"~/code/x/.env*",
		"~/Library/Application Support/MobileSync/**",
		"~/Library/**/x",
		"~/x*/[a-z]*/y",
	} {
		r := validRecipe()
		r.Allow = []string{p}
		if recipes.Validate(r) == nil {
			t.Errorf("lint accepts %q", p)
		}
	}
	for _, p := range []string{
		"~/Library/Containers/com.google.x/*",
		"~/code/x/*",
		"~/code/**/node_modules",
		"~/Library/Caches/*",
		"~/Library/Application Support/Google/AndroidStudio*",
		"~/*/**/node_modules", // projectTreePatterns
		"~/*/**/.venv",
	} {
		r := validRecipe()
		r.Allow = []string{p}
		if err := recipes.Validate(r); err != nil {
			t.Errorf("lint rejects %q: %v", p, err)
		}
	}
	// The project-tree exemption skips only rules (a) and (b).
	if err := recipes.LintAllowProtected("~/*/**/node_modules"); err != nil {
		t.Error(err)
	}
	// keep_children_of is a scan hint, not a target: a protected folder is
	// fine there (ios.device_backups lists MobileSync/Backup).
	r := validRecipe()
	r.KeepChildrenOf = []string{"~/Library/Application Support/MobileSync/Backup"}
	if err := recipes.Validate(r); err != nil {
		t.Error(err)
	}
}

// D4 and D9.
func TestW4ArgCharsets(t *testing.T) {
	r := validRecipe()
	r.Preconditions = []string{"app_not_running:Android Studio", "app_not_running:Xcode-beta.2"}
	if err := recipes.Validate(r); err != nil {
		t.Error(err)
	}
	for _, s := range []string{"all", "ALL", "Unavailable", "booted", "help", "Version"} {
		if recipes.ValidNativeArg(s) {
			t.Errorf("reserved %q accepted", s)
		}
	}
	if !recipes.ValidNativeArg("allure") || !recipes.ValidNativeArg("Pixel_7") {
		t.Error("ordinary names rejected")
	}
}

// D6: recipes.ReadSmallFile.
func TestW4ReadSmallFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f")
	writeFile(t, p, "hello")
	if b, err := recipes.ReadSmallFile(p, 5); err != nil || string(b) != "hello" {
		t.Errorf("%q, %v", b, err)
	}
	if _, err := recipes.ReadSmallFile(p, 4); !errors.Is(err, recipes.ErrTooLarge) {
		t.Errorf("too large: %v", err)
	}
	if err := os.Symlink(p, filepath.Join(dir, "l")); err != nil {
		t.Fatal(err)
	}
	if _, err := recipes.ReadSmallFile(filepath.Join(dir, "l"), 64); !errors.Is(err, recipes.ErrNotRegular) {
		t.Errorf("symlink: %v", err)
	}
	if _, err := recipes.ReadSmallFile(dir, 64); !errors.Is(err, recipes.ErrNotRegular) {
		t.Errorf("directory: %v", err)
	}
	if _, err := recipes.ReadSmallFile(filepath.Join(dir, "missing"), 64); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing: %v", err)
	}
}

// D5: AVD directories must be real *.avd directories strictly inside the
// AVD home, with a regular config.ini, and not protected.
func TestW4AVDDirRules(t *testing.T) {
	home := androidHome(t)
	avdDir := filepath.Join(home, ".android/avd")
	add := func(name, dir string, cfg bool) {
		writeFile(t, filepath.Join(avdDir, name+".ini"), "path="+dir+"\n")
		mkdir(t, dir)
		if cfg {
			writeFile(t, filepath.Join(dir, "config.ini"), "avd.ini.displayname="+name+"\n")
		}
	}
	add("Good", avdDir+"/Good.avd", true)
	add("NoCfg", avdDir+"/NoCfg.avd", false)
	add("NotAvd", avdDir+"/NotAvd.dir", true)
	add("Nested", avdDir+"/sub/Nested.avd", true)
	// A symlinked .avd directory inside the AVD home pointing elsewhere.
	add("Real", home+"/elsewhere/Real.avd", true)
	if err := os.Symlink(home+"/elsewhere/Real.avd", avdDir+"/Link.avd"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(avdDir, "Link.ini"), "path="+avdDir+"/Link.avd\n")
	// The AVD home itself.
	writeFile(t, filepath.Join(avdDir, "Self.ini"), "path="+avdDir+"\n")
	writeFile(t, filepath.Join(avdDir, "config.ini"), "x=y\n")

	var nodes fakeNodes
	for _, d := range []string{"Good.avd", "NoCfg.avd", "NotAvd.dir", "sub/Nested.avd", "Link.avd", ""} {
		nodes = append(nodes, model.Node{Path: filepath.Join(avdDir, d), Bytes: 1 << 30, MtimeMax: w2DaysAgo(400)})
	}
	nodes = append(nodes, model.Node{Path: home + "/elsewhere/Real.avd", Bytes: 1 << 30, MtimeMax: w2DaysAgo(400)})
	withTools(t, "avdmanager")
	fs, err := r1Run(t, "android.avd", recipes.Env{Home: home, Nodes: nodes})
	if err != nil {
		t.Fatal(err)
	}
	var args []string
	for _, f := range fs {
		args = append(args, f.NativeArg)
	}
	slices.Sort(args)
	if !slices.Equal(args, []string{"Good", "Nested"}) {
		t.Errorf("AVDs = %q, want Good and Nested", args)
	}
	// A home the safety policy rejects is an error, not "no AVDs".
	t.Setenv("ANDROID_AVD_HOME", avdDir)
	if _, err := recipes.ListAVDs(recipes.Env{Home: "relative"}); err == nil {
		t.Error("relative home accepted")
	}
}

// D7: checked sums.
func TestW4SizeChecks(t *testing.T) {
	if v, ok := recipes.AddSize(1, 2); !ok || v != 3 {
		t.Error("addSize")
	}
	for _, c := range [][2]int64{{0, -1}, {-1, 0}, {recipes.MaxPlausibleBytes, 1}, {1, recipes.MaxPlausibleBytes}} {
		if _, ok := recipes.AddSize(c[0], c[1]); ok {
			t.Errorf("addSize%v accepted", c)
		}
	}
	if b, ok := recipes.ParseSize("1000TB", 1000); !ok || b != recipes.MaxPlausibleBytes {
		t.Errorf("1 PB = %d, %v", b, ok)
	}
	if _, ok := recipes.ParseSize("1001TB", 1000); ok {
		t.Error("over 1 PB accepted")
	}
	r1AllTools(t)
	out := "Will remove 1 (900 TB) tarball(s).\nWill remove 2 (900 TB) package(s).\n"
	runner := w2Fake(map[string]platform.Result{"conda clean --all --dry-run": {Stdout: []byte(out)}})
	fs, err := r1Run(t, "conda.pkgs", recipes.Env{Home: w2Home, Runner: runner})
	if err != nil || len(fs) != 1 || fs[0].Bytes.Source != model.SourceUnknown || fs[0].Bytes.Reason == "" {
		t.Errorf("conda sum overflow: %+v, %v", fs, err)
	}
	js := `{"R":{"identifier":"R","version":"17.0","runtimeIdentifier":"com.apple.CoreSimulator.SimRuntime.iOS-17-0","deletable":true,"sizeBytes":9000000000000000000}}`
	runner = w2Fake(map[string]platform.Result{"xcrun simctl runtime list -j": {Stdout: []byte(js)}})
	fs, err = r1Run(t, "xcode.sim_runtimes", recipes.Env{Home: w2Home, Runner: runner})
	if err != nil || len(fs) != 1 || fs[0].Bytes.Source != model.SourceUnknown {
		t.Errorf("absurd runtime size: %+v, %v", fs, err)
	}
	// Brew's absurd size keeps a reason naming it.
	runner = w2Fake(map[string]platform.Result{"brew cleanup -n --prune=all": {Stdout: []byte("Would remove: /x\n==> This operation would free approximately 99999999999TB of disk space.\n")}})
	fs, err = r1Run(t, "brew.cleanup", recipes.Env{Home: w2Home, Runner: runner})
	if err != nil || len(fs) != 1 || !strings.Contains(fs[0].Bytes.Reason, "99999999999TB") {
		t.Errorf("brew absurd: %+v, %v", fs, err)
	}
}

// D10: the children detector's exclude list; user.logs skips Google and
// JetBrains.
func TestW4ChildrenExclude(t *testing.T) {
	l := w2Home + "/Library/Logs"
	nodes := fakeNodes{
		{Path: l, Bytes: 10},
		{Path: l + "/google", Bytes: 1},
		{Path: l + "/JetBrains", Bytes: 1},
		{Path: l + "/DiagnosticReports", Bytes: 1},
	}
	fs, err := w2Run(t, "user.logs", recipes.Env{Home: w2Home, Nodes: nodes})
	w2WantTargets(t, fs, err, l+"/DiagnosticReports")
}

// D12: installers are quarantinable; other large old downloads are
// advise-only.
func TestW4Downloads(t *testing.T) {
	d := w2Home + "/Downloads"
	nodes := fakeNodes{
		{Path: d, Bytes: 10 << 30},
		{Path: d + "/Xcode.XIP", Kind: model.NodeFile, Bytes: 9 << 30, MtimeMax: w2DaysAgo(200)},
		{Path: d + "/data.tar.gz", Kind: model.NodeFile, Bytes: 1 << 30, MtimeMax: w2DaysAgo(200)},
		{Path: d + "/holiday.mov", Kind: model.NodeFile, Bytes: 2 << 30, MtimeMax: w2DaysAgo(200)},
		{Path: d + "/dmg", Kind: model.NodeFile, Bytes: 2 << 30, MtimeMax: w2DaysAgo(200)}, // look-alike: no extension
		{Path: d + "/new.dmg", Kind: model.NodeFile, Bytes: 2 << 30, MtimeMax: w2DaysAgo(10)},
		{Path: d + "/old.mov", Kind: model.NodeFile, Bytes: 2 << 30, MtimeMax: w2DaysAgo(10)},
	}
	fs, err := w2Run(t, "user.downloads_large_old", recipes.Env{Home: w2Home, Nodes: nodes})
	w2WantTargets(t, fs, err, d+"/Xcode.XIP", d+"/data.tar.gz")
	fs, err = w2Run(t, "user.downloads_large_other", recipes.Env{Home: w2Home, Nodes: nodes})
	w2WantTargets(t, fs, err, d+"/holiday.mov", d+"/dmg")

	other := w2Recipe(t, "user.downloads_large_other")
	if other.Method != model.MethodAdvise || other.Risk != model.RiskAdvise || other.Category != "downloads" || other.StaleDays != 90 ||
		!strings.Contains(other.Restore.Note, "may be the only copy") {
		t.Errorf("downloads_large_other = %+v", other)
	}
	inst := w2Recipe(t, "user.downloads_large_old")
	if !strings.Contains(inst.Restore.Note, "Re-download from the original source") || !strings.Contains(inst.Restore.Note, "may be the only copy") {
		t.Errorf("downloads_large_old note = %q", inst.Restore.Note)
	}
	for _, files := range []string{"", "all"} {
		r := w2Recipe(t, "user.downloads_large_old")
		r.Params = map[string]string{"dir": "~/Downloads", "files": files}
		if _, err := recipes.DetectDownloadsLargeOld(context.Background(), recipes.Env{Home: w2Home, Nodes: nodes}, r); err == nil {
			t.Errorf("files=%q accepted", files)
		}
	}
}

// D13, D14: catalog values.
func TestW4CatalogValues(t *testing.T) {
	if r := w2Recipe(t, "user.app_caches"); r.StaleDays != 14 || !strings.Contains(r.Params["exclude"], "Google") || !strings.Contains(r.Params["exclude"], "JetBrains") {
		t.Errorf("user.app_caches = %+v", r)
	}
	if r := w2Recipe(t, "python.venv_stale"); r.Risk != model.RiskRisky {
		t.Errorf("python.venv_stale risk = %s", r.Risk)
	}
}

// D15: ollama's relative MODIFIED forms.
func TestW4OllamaRelative(t *testing.T) {
	day := 24 * time.Hour
	for s, want := range map[string]time.Duration{
		"Less than a second ago": 0,
		"About a minute ago":     time.Minute,
		"About an hour ago":      time.Hour,
		"Yesterday":              day,
		"1 second ago":           time.Second,
		"5 minutes ago":          5 * time.Minute,
		"2 hours ago":            2 * time.Hour,
		"1 day ago":              day,
		"3 weeks ago":            21 * day,
		"2 months ago":           60 * day,
		"1 year ago":             365 * day,
	} {
		if d, ok := recipes.OllamaRelative(s); !ok || d != want {
			t.Errorf("%q = %v, %v", s, d, ok)
		}
	}
	for _, s := range []string{"", "soon", "3 fortnights ago", "-1 days ago", "99999 years ago", "a while ago"} {
		if _, ok := recipes.OllamaRelative(s); ok {
			t.Errorf("%q parsed", s)
		}
	}
	r1AllTools(t)
	out := "NAME ID SIZE MODIFIED\nm:latest a1 1 GB 2 days ago\nn:latest a2 1 GB some time\n"
	fs, err := r1Run(t, "ollama.model", recipes.Env{Home: w2Home, Runner: w2Fake(map[string]platform.Result{"ollama list": {Stdout: []byte(out)}})})
	if err != nil || len(fs) != 2 || !fs[0].LastUsed.Equal(w2Now.Add(-2*day)) || !fs[1].LastUsed.IsZero() || fs[1].Note == "" {
		t.Errorf("ollama = %+v, %v", fs, err)
	}
}

// D11: recipes.ProjectDirs skips protected folders but keeps ordinary ones.
func TestW4ProjectDirsProtected(t *testing.T) {
	home := r1Home(t)
	nodes := fakeNodes{
		{Path: home + "/Documents/a/node_modules", Bytes: 1},
		{Path: home + "/code/a/node_modules", Bytes: 1},
		{Path: home + "/code/.git/x/node_modules", Bytes: 1},
	}
	got, err := recipes.ProjectDirs(recipes.Env{Home: home, Nodes: nodes}, "node_modules")
	if err != nil || len(got) != 1 || got[0].Path != home+"/code/a/node_modules" {
		t.Errorf("projectDirs = %+v, %v", got, err)
	}
	if _, err := recipes.ProjectDirs(recipes.Env{Home: "relative", Nodes: nodes}, "node_modules"); err == nil {
		t.Error("bad home accepted")
	}
}
