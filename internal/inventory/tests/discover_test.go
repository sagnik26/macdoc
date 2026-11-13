package tests

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/sagnik26/macdoc/internal/inventory"
	"github.com/sagnik26/macdoc/internal/platform"
)

// makeApp writes a bundle folder with an Info.plist placeholder. The plist
// content is irrelevant here: the fake runner answers plutil.
func makeApp(t *testing.T, dir, name string) string {
	t.Helper()
	bundle := filepath.Join(dir, name+".app")
	if err := os.MkdirAll(filepath.Join(bundle, "Contents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundle, "Contents", "Info.plist"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return bundle
}

func plutilKey(bundle string) string {
	return "plutil -convert json -o - " + filepath.Join(bundle, "Contents", "Info.plist")
}

func statusOf(res inventory.Result, source string) inventory.Status {
	for _, s := range res.Statuses {
		if s.Source == source {
			return s
		}
	}
	return inventory.Status{}
}

func find(res inventory.Result, id string) (inventory.Software, bool) {
	for _, s := range res.Software {
		if s.ID == id {
			return s, true
		}
	}
	return inventory.Software{}, false
}

func TestAppsReadBundleIdentity(t *testing.T) {
	apps := t.TempDir()
	bundle := makeApp(t, apps, "Figma")
	fake := &platform.Fake{Responses: map[string]platform.Result{
		plutilKey(bundle): {Stdout: []byte(`{"CFBundleIdentifier":"com.figma.Desktop","CFBundleName":"Figma","CFBundleShortVersionString":"124.3"}`)},
	}}
	res := inventory.Discover(context.Background(), inventory.Env{
		Home: t.TempDir(), AppDirs: []string{apps}, Runner: fake,
		HasTool: func(string) bool { return false }, // brew and npm absent: apps only
	})
	sw, ok := find(res, "app:com.figma.Desktop")
	if !ok {
		t.Fatalf("app not found: %+v", res.Software)
	}
	if sw.Name != "Figma" || sw.Version != "124.3" || sw.Kind != "app" || sw.Installed.IsZero() {
		t.Errorf("app = %+v", sw)
	}
}

func TestAppWithoutReadablePlistKeepsItsFolderName(t *testing.T) {
	apps := t.TempDir()
	makeApp(t, apps, "Mystery")
	res := inventory.Discover(context.Background(), inventory.Env{
		AppDirs: []string{apps}, Runner: &platform.Fake{}, HasTool: func(string) bool { return false },
	})
	sw, ok := find(res, "app-name:Mystery")
	if !ok {
		t.Fatalf("app with unreadable plist should still be listed by name: %+v", res.Software)
	}
	if sw.BundleID != "" {
		t.Errorf("bundle id should be unknown, got %q", sw.BundleID)
	}
}

func TestMissingAppFolderIsNotAnError(t *testing.T) {
	res := inventory.Discover(context.Background(), inventory.Env{
		AppDirs: []string{filepath.Join(t.TempDir(), "nope")}, Runner: &platform.Fake{},
		HasTool: func(string) bool { return false },
	})
	if st := statusOf(res, "apps"); st.State != "ok" {
		t.Errorf("apps status = %+v, want ok", st)
	}
}

func TestBrewAbsentIsNotUnknown(t *testing.T) {
	res := inventory.Discover(context.Background(), inventory.Env{
		Runner: &platform.Fake{}, HasTool: func(name string) bool { return name != "brew" },
	})
	if st := statusOf(res, "brew"); st.State != "absent" {
		t.Errorf("brew status = %+v, want absent", st)
	}
}

func TestBrewListsFormulaeAndCasks(t *testing.T) {
	fake := &platform.Fake{Responses: map[string]platform.Result{
		"brew list --formula": {Stdout: []byte("node\nxcodes\n")},
		"brew list --cask":    {Stdout: []byte("docker\n")},
	}}
	res := inventory.Discover(context.Background(), inventory.Env{
		Runner: fake, HasTool: func(name string) bool { return name == "brew" },
	})
	if _, ok := find(res, "brew:node"); !ok {
		t.Errorf("formula node missing: %+v", res.Software)
	}
	if sw, ok := find(res, "brew:docker"); !ok || sw.Kind != "cask" {
		t.Errorf("cask docker = %+v, %v", sw, ok)
	}
	if st := statusOf(res, "brew"); st.State != "ok" {
		t.Errorf("brew status = %+v", st)
	}
}

func TestFailingBrewKeepsWhatItListedBeforeIt(t *testing.T) {
	fake := &platform.Fake{
		Responses: map[string]platform.Result{"brew list --formula": {Stdout: []byte("node\n")}},
		Errors:    map[string]error{"brew list --cask": os.ErrDeadlineExceeded},
	}
	res := inventory.Discover(context.Background(), inventory.Env{
		Runner: fake, HasTool: func(name string) bool { return name == "brew" },
	})
	if _, ok := find(res, "brew:node"); !ok {
		t.Errorf("formula listed before the failure should be kept: %+v", res.Software)
	}
	if st := statusOf(res, "brew"); st.State != "unknown" || st.Reason == "" {
		t.Errorf("brew status = %+v, want unknown with a reason", st)
	}
}

func TestNpmGlobalPackages(t *testing.T) {
	fake := &platform.Fake{Responses: map[string]platform.Result{
		"npm ls -g --depth=0 --json": {Stdout: []byte(`{"dependencies":{"typescript":{"version":"5.4.0"}}}`), ExitCode: 1},
	}}
	res := inventory.Discover(context.Background(), inventory.Env{
		Runner: fake, HasTool: func(name string) bool { return name == "npm" },
	})
	sw, ok := find(res, "npm:typescript")
	if !ok || sw.Version != "5.4.0" {
		t.Errorf("npm package = %+v, %v", sw, ok)
	}
}

func TestBadNpmOutputIsUnknownNotAPanic(t *testing.T) {
	fake := &platform.Fake{Responses: map[string]platform.Result{
		"npm ls -g --depth=0 --json": {Stdout: []byte("not json")},
	}}
	res := inventory.Discover(context.Background(), inventory.Env{
		Runner: fake, HasTool: func(name string) bool { return name == "npm" },
	})
	if st := statusOf(res, "npm"); st.State != "unknown" {
		t.Errorf("npm status = %+v, want unknown", st)
	}
}

func slimEnv(t *testing.T, plist string, resp map[string]platform.Result, errs map[string]error) (inventory.Software, *platform.Fake) {
	t.Helper()
	apps := t.TempDir()
	bundle := makeApp(t, apps, "Fat")
	resp[plutilKey(bundle)] = platform.Result{Stdout: []byte(plist)}
	fake := &platform.Fake{Responses: resp, Errors: errs}
	res := inventory.Discover(context.Background(), inventory.Env{
		Home: t.TempDir(), AppDirs: []string{apps}, Runner: fake,
		HasTool: func(string) bool { return false },
	})
	sw, ok := find(res, "app:com.x.fat")
	if !ok {
		t.Fatal("app not found")
	}
	return sw, fake
}

const slimPlist = `{"CFBundleIdentifier":"com.x.fat","CFBundleExecutable":"Fat"}`

func TestSlimUniversal(t *testing.T) {
	apps := t.TempDir()
	bundle := makeApp(t, apps, "Fat")
	exe := filepath.Join(bundle, "Contents", "MacOS", "Fat")
	fake := &platform.Fake{Responses: map[string]platform.Result{
		plutilKey(bundle):            {Stdout: []byte(slimPlist)},
		"lipo -archs " + exe:         {Stdout: []byte("x86_64 arm64\n")},
		"lipo -detailed_info " + exe: {Stdout: []byte("Fat header in: x\nnfat_arch 2\narchitecture x86_64\n    offset 16384\n    size 48128\n    align 2^14\narchitecture arm64\n    size 99\n")},
	}}
	res := inventory.Discover(context.Background(), inventory.Env{Home: t.TempDir(), AppDirs: []string{apps}, Runner: fake,
		HasTool: func(string) bool { return false }})
	sw, _ := find(res, "app:com.x.fat")
	if sw.Slim == nil || !sw.Slim.Universal || sw.Slim.IntelBytes == nil || *sw.Slim.IntelBytes != 48128 {
		t.Fatalf("slim = %+v", sw.Slim)
	}
}

func TestSlimSingleArchIsNotUniversal(t *testing.T) {
	for _, archs := range []string{"arm64", "x86_64"} {
		apps := t.TempDir()
		bundle := makeApp(t, apps, "Fat")
		exe := filepath.Join(bundle, "Contents", "MacOS", "Fat")
		fake := &platform.Fake{Responses: map[string]platform.Result{
			plutilKey(bundle):    {Stdout: []byte(slimPlist)},
			"lipo -archs " + exe: {Stdout: []byte(archs + "\n")},
		}}
		res := inventory.Discover(context.Background(), inventory.Env{Home: t.TempDir(), AppDirs: []string{apps}, Runner: fake,
			HasTool: func(string) bool { return false }})
		sw, _ := find(res, "app:com.x.fat")
		if sw.Slim == nil || sw.Slim.Universal || sw.Slim.IntelBytes != nil {
			t.Fatalf("%s: slim = %+v", archs, sw.Slim)
		}
	}
}

func TestSlimUnknownWhenLipoFails(t *testing.T) {
	sw, _ := slimEnv(t, slimPlist, map[string]platform.Result{}, nil) // fake has no lipo answer: error
	if sw.Slim != nil {
		t.Fatalf("want unknown (nil), got %+v", sw.Slim)
	}
}

func TestSlimIntelSizeUnknownWhenDetailFails(t *testing.T) {
	apps := t.TempDir()
	bundle := makeApp(t, apps, "Fat")
	exe := filepath.Join(bundle, "Contents", "MacOS", "Fat")
	fake := &platform.Fake{Responses: map[string]platform.Result{
		plutilKey(bundle):    {Stdout: []byte(slimPlist)},
		"lipo -archs " + exe: {Stdout: []byte("arm64 x86_64")},
	}}
	res := inventory.Discover(context.Background(), inventory.Env{Home: t.TempDir(), AppDirs: []string{apps}, Runner: fake,
		HasTool: func(string) bool { return false }})
	sw, _ := find(res, "app:com.x.fat")
	if sw.Slim == nil || !sw.Slim.Universal || sw.Slim.IntelBytes != nil {
		t.Fatalf("slim = %+v", sw.Slim)
	}
}
