package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/platform"
	"github.com/sagnik26/macdoc/internal/recipes"
)

func sysFile(t *testing.T, base, rel string, age int) string {
	t.Helper()
	p := filepath.Join(base, rel)
	touch(t, p, strings.Repeat("x", 5000))
	old := w2Now.Add(-time.Duration(age) * 24 * time.Hour)
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatal(err)
	}
	return p
}

// The directory detector lists items one level down, with measured sizes and
// the newest change, and skips Apple items and symlinks.
func TestSystemDirDetector(t *testing.T) {
	base := t.TempDir()
	sysFile(t, base, "Library/Caches/Acme/a.bin", 90)
	sysFile(t, base, "Library/Caches/com.apple.thing/a.bin", 90)
	sysFile(t, base, "Library/Caches/Single.cache", 90)
	if err := os.Symlink("/etc", filepath.Join(base, "Library/Caches/Link")); err != nil {
		t.Fatal(err)
	}
	fs, err := w2Run(t, "system.library_caches", recipes.Env{SystemBase: base})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]recipes.Finding{}
	for _, f := range fs {
		got[filepath.Base(f.Target)] = f
	}
	if len(got) != 2 || got["Acme"].Bytes.Source != model.SourceMeasured || got["Acme"].Bytes.Value == 0 || got["Single.cache"].Target == "" {
		t.Fatalf("findings = %+v", fs)
	}
	if got["Acme"].LastUsed.IsZero() || !strings.Contains(got["Acme"].Note, "password") {
		t.Errorf("Acme = %+v", got["Acme"])
	}
	// A folder that isn't there gives nothing.
	fs, err = w2Run(t, "system.library_logs", recipes.Env{SystemBase: t.TempDir()})
	w2WantNone(t, fs, err)
}

// An orphaned background item is one whose program is gone; one whose program
// exists, and Apple's, are left.
func TestSystemLaunchdDetector(t *testing.T) {
	base := t.TempDir()
	live := filepath.Join(base, "bin", "alive")
	touch(t, live, "x")
	plist := func(name, label, prog string) (string, string) {
		p := sysFile(t, base, "Library/LaunchDaemons/"+name, 1)
		j := `{"Label":"` + label + `","ProgramArguments":["` + prog + `"]}`
		return "plutil -convert json -o - " + p, j
	}
	r := map[string]platform.Result{}
	for _, c := range [][3]string{
		{"org.acme.gone.plist", "org.acme.gone", "/opt/acme/missing"},
		{"org.acme.alive.plist", "org.acme.alive", live},
		{"com.apple.x.plist", "com.apple.x", "/opt/acme/missing"},
	} {
		k, j := plist(c[0], c[1], c[2])
		r[k] = platform.Result{Stdout: []byte(j)}
	}
	fs, err := w2Run(t, "system.launch_daemons", recipes.Env{SystemBase: base, Runner: w2Fake(r)})
	if err != nil || len(fs) != 1 || filepath.Base(fs[0].Target) != "org.acme.gone.plist" {
		t.Fatalf("got %+v, %v", fs, err)
	}
}

// A helper tool is offered only when no installed app, helper or background
// item owns its identifier; Apple's and names that aren't identifiers are not.
func TestSystemHelpersDetector(t *testing.T) {
	apps, fake := appFixture(t, "com.vendor.alive")
	base := t.TempDir()
	for _, n := range []string{"com.gone.helper", "com.vendor.alive.helper", "com.apple.helper", "NotAnID", "com.vendor"} {
		sysFile(t, base, "Library/PrivilegedHelperTools/"+n, 1)
	}
	fs, err := w2Run(t, "system.helper_tools", recipes.Env{SystemBase: base, Runner: fake, Home: t.TempDir(), AppDirs: []string{apps}})
	if err != nil || len(fs) != 1 || filepath.Base(fs[0].Target) != "com.gone.helper" {
		t.Fatalf("got %+v, %v", fs, err)
	}
	// If the installed apps can't be listed, nothing is offered.
	fs, err = w2Run(t, "system.helper_tools", recipes.Env{SystemBase: base, Runner: &platform.Fake{}, Home: t.TempDir(), AppDirs: []string{t.TempDir()}})
	if err == nil || len(fs) != 0 {
		t.Errorf("an unreadable app list gave %+v, %v", fs, err)
	}
}

// Interrupted Time Machine backups are listed one by one; complete ones are not.
func TestSystemBackupsDetector(t *testing.T) {
	w2WithTools(t, "tmutil")
	mount := t.TempDir()
	db := filepath.Join(mount, "Backups.backupdb", "My Mac")
	for _, d := range []string{"2026-10-01-120000", "2026-11-01-120000.inProgress", "2026-11-02-090000.inProgress"} {
		if err := os.MkdirAll(filepath.Join(db, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	fs, err := w2Run(t, "system.tm_incomplete", recipes.Env{Runner: w2Fake(map[string]platform.Result{"tmutil destinationinfo": tmInfo(mount)})})
	if err != nil || len(fs) != 2 || fs[0].Bytes.Source != model.SourceUnknown {
		t.Fatalf("got %+v, %v", fs, err)
	}
	for _, f := range fs {
		if !strings.HasSuffix(f.Target, ".inProgress") {
			t.Errorf("%s isn't an interrupted backup", f.Target)
		}
	}
}

// The command is built from the kind and path, and the privileged runner's own
// check accepts only that: a wrong flag, an extra argument, another program or
// a path outside the named layouts is refused.
func TestSystemArgv(t *testing.T) {
	for kind, want := range map[string][]string{
		"caches":      {"rm", "-rf", "--", "/Library/Caches/Foo"},
		"launchd":     {"rm", "-f", "--", "/Library/LaunchDaemons/org.a.b.plist"},
		"timemachine": {"tmutil", "delete", "/Volumes/B/Backups.backupdb/M/x.inProgress"},
	} {
		got := recipes.SystemArgv(kind, want[len(want)-1])
		if strings.Join(got, " ") != strings.Join(want, " ") || !recipes.SystemArgvOK(got) {
			t.Errorf("%s: %v (ok %v)", kind, got, recipes.SystemArgvOK(got))
		}
	}
	for _, argv := range [][]string{
		{"rm", "-rf", "--", "/"}, {"rm", "-rf", "--", "/Library/Caches"}, {"rm", "-rf", "/Library/Caches/Foo"},
		{"rm", "-rf", "--", "/Library/Caches/Foo", "/etc"}, {"rm", "-rf", "--no-preserve-root", "--", "/Library/Caches/Foo"},
		{"rm", "-f", "--", "/Library/Caches/Foo"}, {"sh", "-c", "rm -rf /"}, {"tmutil", "delete", "/Library/Caches/Foo"},
		{"tmutil", "deletelocalsnapshots", "/Volumes/B/Backups.backupdb/M/x.inProgress"}, {"rm"}, nil,
		{"rm", "-rf", "--", "/Library/Caches/com.apple.x"}, {"rm", "-rf", "--", "/Library/Caches/Foo/../../../etc"},
	} {
		if recipes.SystemArgvOK(argv) {
			t.Errorf("%q accepted", argv)
		}
	}
}

// The loader accepts a system recipe only with a known kind, that kind's
// detector and risk, and no allow list, command or privileged flag.
func TestSystemLoaderRefusals(t *testing.T) {
	good := recipes.Recipe{ID: "system.library_caches", Title: "t", Category: "system", Toolchain: "system", Risk: model.RiskCaution,
		Method: model.MethodSystem, Detector: "system_dir", Params: map[string]string{"dir": "/Library/Caches", "kind": "caches"},
		Restore: model.Restore{Kind: model.RestoreNone}, Explain: "x"}
	if err := recipes.Validate(good); err != nil {
		t.Fatalf("the system recipe shape is rejected: %v", err)
	}
	for name, mod := range map[string]func(*recipes.Recipe){
		"no kind":        func(r *recipes.Recipe) { r.Params = map[string]string{"dir": "/Library/Caches"} },
		"unknown kind":   func(r *recipes.Recipe) { r.Params["kind"] = "everything" },
		"wrong detector": func(r *recipes.Recipe) { r.Detector = "children" },
		"SAFE":           func(r *recipes.Recipe) { r.Risk = model.RiskSafe },
		"RISKY caches":   func(r *recipes.Recipe) { r.Risk = model.RiskRisky },
		"allow list":     func(r *recipes.Recipe) { r.Allow = []string{"/Library/Caches/*"} },
		"command":        func(r *recipes.Recipe) { r.Native = &recipes.Native{Argv: []string{"brew", "cleanup"}} },
	} {
		r := good
		r.Params = map[string]string{"dir": "/Library/Caches", "kind": "caches"}
		mod(&r)
		if recipes.Validate(r) == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// Look-alike: a launchd recipe must be RISKY with its own detector.
	l := good
	l.Params = map[string]string{"dir": "/Library/LaunchDaemons", "kind": "launchd"}
	l.Detector, l.Risk = "system_launchd", model.RiskRisky
	if err := recipes.Validate(l); err != nil {
		t.Errorf("launchd recipe rejected: %v", err)
	}
	l.Risk = model.RiskCaution
	if recipes.Validate(l) == nil {
		t.Error("a CAUTION launchd recipe was accepted")
	}
}
