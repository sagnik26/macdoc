package tests

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/platform"
	"github.com/sagnik26/macdoc/internal/recipes"
)

// appFixture makes an app folder holding one .app per bundle identifier and a
// runner that answers plutil for each. It returns the folder.
func appFixture(t *testing.T, ids ...string) (string, *platform.Fake) {
	t.Helper()
	dir := t.TempDir()
	fake := &platform.Fake{Responses: map[string]platform.Result{}}
	for i, id := range ids {
		bundle := filepath.Join(dir, "App"+string(rune('A'+i))+".app")
		if err := os.MkdirAll(filepath.Join(bundle, "Contents"), 0o755); err != nil {
			t.Fatal(err)
		}
		fake.Responses["plutil -convert json -o - "+filepath.Join(bundle, "Contents", "Info.plist")] =
			platform.Result{Stdout: []byte(`{"CFBundleIdentifier":"` + id + `","CFBundleName":"x"}`)}
	}
	return dir, fake
}

// Positive and look-alikes in one table: only a reverse-DNS folder that no
// installed app or helper owns, old enough and big enough, is a leftover.
func TestAppLeftovers(t *testing.T) {
	apps, fake := appFixture(t, "com.vendor.alive")
	c := w2Home + "/Library/Caches"
	nodes := fakeNodes{
		{Path: c, Bytes: 1 << 30},
		{Path: c + "/com.gone.app", Bytes: 50 << 20, MtimeMax: w2DaysAgo(200)},            // leftover
		{Path: c + "/com.vendor.alive", Bytes: 50 << 20, MtimeMax: w2DaysAgo(200)},        // installed
		{Path: c + "/com.vendor.alive.helper", Bytes: 50 << 20, MtimeMax: w2DaysAgo(200)}, // helper of an installed app
		{Path: c + "/com.vendor", Bytes: 50 << 20, MtimeMax: w2DaysAgo(200)},              // too short to be an app id
		{Path: c + "/com.apple.Safari", Bytes: 50 << 20, MtimeMax: w2DaysAgo(200)},        // Apple's
		{Path: c + "/Slack", Bytes: 50 << 20, MtimeMax: w2DaysAgo(200)},                   // a name, not an id
		{Path: c + "/com.gone.fresh", Bytes: 50 << 20, MtimeMax: w2DaysAgo(5)},            // changed recently
		{Path: c + "/com.gone.tiny", Bytes: 1 << 10, MtimeMax: w2DaysAgo(200)},            // under 1 MB
		{Path: c + "/com.gone.app/deep", Bytes: 50 << 20, MtimeMax: w2DaysAgo(200)},       // not a direct child
		{Path: c + "/com bad.app.x", Bytes: 50 << 20, MtimeMax: w2DaysAgo(200)},           // not an identifier
	}
	fs, err := w2Run(t, "apps.leftovers", recipes.Env{Nodes: nodes, Runner: fake, AppDirs: []string{apps}})
	w2WantTargets(t, fs, err, c+"/com.gone.app")
	if fs[0].Bytes.Source != model.SourceMeasured || fs[0].Title == "" {
		t.Errorf("finding = %+v", fs[0])
	}
}

// Saved window state is named after the identifier plus ".savedState".
func TestAppLeftoversSavedState(t *testing.T) {
	apps, fake := appFixture(t, "com.vendor.alive")
	s := w2Home + "/Library/Saved Application State"
	nodes := fakeNodes{
		{Path: s + "/com.gone.app.savedState", Bytes: 5 << 20, MtimeMax: w2DaysAgo(200)},
		{Path: s + "/com.vendor.alive.savedState", Bytes: 5 << 20, MtimeMax: w2DaysAgo(200)},
	}
	fs, err := w2Run(t, "apps.leftovers", recipes.Env{Nodes: nodes, Runner: fake, AppDirs: []string{apps}})
	w2WantTargets(t, fs, err, s+"/com.gone.app.savedState")
}

// If the installed apps can't be listed, nothing is offered: an unreadable
// machine must never look like one with everything uninstalled.
func TestAppLeftoversNeedTheInstalledList(t *testing.T) {
	c := w2Home + "/Library/Caches"
	nodes := fakeNodes{{Path: c + "/com.gone.app", Bytes: 50 << 20, MtimeMax: w2DaysAgo(200)}}
	empty := t.TempDir()
	fs, err := w2Run(t, "apps.leftovers", recipes.Env{Nodes: nodes, Runner: &platform.Fake{}, AppDirs: []string{empty}})
	if err == nil || len(fs) != 0 {
		t.Errorf("got %+v, %v; want an error and no findings", fs, err)
	}
	apps, fake := appFixture(t, "com.vendor.alive")
	fake.Errors = map[string]error{}
	for k := range fake.Responses {
		fake.Errors[k] = errors.New("boom")
	}
	fs, err = w2Run(t, "apps.leftovers", recipes.Env{Nodes: nodes, Runner: fake, AppDirs: []string{apps}})
	if len(fs) != 0 {
		t.Errorf("unreadable apps still offered %+v (%v)", fs, err)
	}
}

func TestLeftoverID(t *testing.T) {
	for in, want := range map[string]string{
		"com.vendor.app": "com.vendor.app", "com.vendor.app.savedState": "com.vendor.app",
		"org.x-y.App_1": "org.x-y.App_1",
	} {
		if got, ok := recipes.LeftoverID(in); !ok || got != want {
			t.Errorf("LeftoverID(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "Slack", "com.vendor", "com.apple.Safari", "COM.Apple.x", "a..b", "com.v endor.app", "../com.a.b"} {
		if _, ok := recipes.LeftoverID(in); ok {
			t.Errorf("LeftoverID(%q) accepted", in)
		}
	}
}

// Positive: an app in ~/Applications is offered, sized from the scan when the
// scan has it and unknown otherwise. Look-alikes: Apple's apps, a plain folder
// named like an app without a bundle, and a file are not offered.
func TestUserAppBundles(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "Applications")
	fake := &platform.Fake{Responses: map[string]platform.Result{}}
	for name, id := range map[string]string{"Mine.app": "com.vendor.mine", "Sized.app": "com.vendor.sized", "Safari.app": "com.apple.Safari"} {
		if err := os.MkdirAll(filepath.Join(dir, name, "Contents"), 0o755); err != nil {
			t.Fatal(err)
		}
		fake.Responses["plutil -convert json -o - "+filepath.Join(dir, name, "Contents", "Info.plist")] =
			platform.Result{Stdout: []byte(`{"CFBundleIdentifier":"` + id + `","CFBundleName":"` + strings.TrimSuffix(name, ".app") + `"}`)}
	}
	if err := os.MkdirAll(filepath.Join(dir, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "x.app.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	nodes := fakeNodes{{Path: dir + "/Sized.app", Bytes: 300 << 20}}
	fs, err := w2Run(t, "apps.user_bundle", recipes.Env{Home: home, Nodes: nodes, Runner: fake})
	w2WantTargets(t, fs, err, dir+"/Mine.app", dir+"/Sized.app")
	for _, f := range fs {
		switch filepath.Base(f.Target) {
		case "Sized.app":
			if f.Bytes.Source != model.SourceMeasured {
				t.Errorf("a scanned app should be measured: %+v", f.Bytes)
			}
		case "Mine.app":
			if f.Bytes.Source != model.SourceUnknown {
				t.Errorf("an unscanned app must be unknown, not zero: %+v", f.Bytes)
			}
		}
	}
}

// Positive: a background item whose program is gone is found, with its label
// and plist. Look-alikes: a program that still exists, Apple's items, a
// relative program, a plist with no label, and an unreadable plist are not.
func TestBrokenLaunchAgents(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "Library", "LaunchAgents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	alive := filepath.Join(home, "alive-tool")
	if err := os.WriteFile(alive, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	fake := &platform.Fake{Responses: map[string]platform.Result{}}
	add := func(file, json string) {
		if err := os.WriteFile(filepath.Join(dir, file), []byte("plist"), 0o644); err != nil {
			t.Fatal(err)
		}
		fake.Responses["plutil -convert json -o - "+filepath.Join(dir, file)] = platform.Result{Stdout: []byte(json)}
	}
	add("com.gone.helper.plist", `{"Label":"com.gone.helper","ProgramArguments":["/Applications/Gone.app/Contents/MacOS/helper","--x"]}`)
	add("com.alive.plist", `{"Label":"com.alive","Program":"`+alive+`"}`)
	add("com.apple.thing.plist", `{"Label":"com.apple.thing","Program":"/System/missing"}`)
	add("com.rel.plist", `{"Label":"com.rel","Program":"relative-tool"}`)
	add("nolabel.plist", `{"Program":"/Applications/Gone.app/x"}`)
	add("com.bad label.plist", `{"Label":"com.bad label","Program":"/Applications/Gone.app/x"}`)
	if err := os.WriteFile(filepath.Join(dir, "broken.plist"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"apps.launch_agent_unload", "apps.launch_agent_file"} {
		fs, err := w2Run(t, id, recipes.Env{Home: home, Runner: fake})
		w2WantTargets(t, fs, err, filepath.Join(dir, "com.gone.helper.plist"))
		if fs[0].NativeArg != "com.gone.helper" {
			t.Errorf("%s: label = %q", id, fs[0].NativeArg)
		}
	}
}

// The /Applications recipe offers a plain user-owned bundle only: Apple's apps,
// Mac App Store apps (they carry a store receipt), a symlink posing as a
// bundle, and non-bundles are left out.
func TestSystemAppBundles(t *testing.T) {
	apps := t.TempDir()
	fake := &platform.Fake{Responses: map[string]platform.Result{}}
	mk := func(name, id string, mode os.FileMode, extra ...string) {
		for _, d := range append([]string{"Contents"}, extra...) {
			if err := os.MkdirAll(filepath.Join(apps, name, d), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Chmod(filepath.Join(apps, name), mode); err != nil {
			t.Fatal(err)
		}
		fake.Responses["plutil -convert json -o - "+filepath.Join(apps, name, "Contents", "Info.plist")] =
			platform.Result{Stdout: []byte(`{"CFBundleIdentifier":"` + id + `","CFBundleName":"x"}`)}
	}
	mk("Plain.app", "com.vendor.plain", 0o755)
	mk("Apple.app", "com.apple.thing", 0o755)
	mk("Store.app", "com.vendor.store", 0o755, "Contents/_MASReceipt")
	if err := os.Symlink(filepath.Join(apps, "Plain.app"), filepath.Join(apps, "Link.app")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(apps, "Utilities", "Deep.app", "Contents"), 0o755); err != nil {
		t.Fatal(err)
	}
	fs, err := w2Run(t, "apps.system_bundle", recipes.Env{Home: t.TempDir(), Runner: fake, AppsFolder: apps})
	w2WantTargets(t, fs, err, filepath.Join(apps, "Plain.app"))
}

// Application Support: only a bundle-ID-named folder with no owner, unchanged
// for 180 days, is offered; a 100-day-old one, a plain-named one, macdoc's own
// folder and MobileSync are not.
func TestSupportLeftovers(t *testing.T) {
	apps, fake := appFixture(t, "com.vendor.alive")
	s := w2Home + "/Library/Application Support"
	nodes := fakeNodes{
		{Path: s + "/com.gone.app", Bytes: 50 << 20, MtimeMax: w2DaysAgo(400)},
		{Path: s + "/com.gone.recent", Bytes: 50 << 20, MtimeMax: w2DaysAgo(100)},
		{Path: s + "/com.vendor.alive", Bytes: 50 << 20, MtimeMax: w2DaysAgo(400)},
		{Path: s + "/Slack", Bytes: 50 << 20, MtimeMax: w2DaysAgo(400)},
		{Path: s + "/macdoc", Bytes: 50 << 20, MtimeMax: w2DaysAgo(400)},
		{Path: s + "/MobileSync", Bytes: 50 << 20, MtimeMax: w2DaysAgo(400)},
	}
	fs, err := w2Run(t, "apps.support_leftovers", recipes.Env{Nodes: nodes, Runner: fake, AppDirs: []string{apps}})
	w2WantTargets(t, fs, err, s+"/com.gone.app")
}
