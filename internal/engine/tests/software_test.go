package tests

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/sagnik26/macdoc/internal/engine"
	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/platform"
	"github.com/sagnik26/macdoc/internal/store"
)

// appBundle writes a bundle folder and registers its plutil answer with the
// fake runner, so the app source reads it as if it were a real Info.plist.
func appBundle(t *testing.T, apps, name, bundleID string, fake *platform.Fake) {
	t.Helper()
	bundle := filepath.Join(apps, name+".app")
	if err := os.MkdirAll(filepath.Join(bundle, "Contents"), 0o755); err != nil {
		t.Fatal(err)
	}
	plist := filepath.Join(bundle, "Contents", "Info.plist")
	if err := os.WriteFile(plist, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	fake.Responses["plutil -convert json -o - "+plist] = platform.Result{
		Stdout: []byte(`{"CFBundleIdentifier":"` + bundleID + `","CFBundleName":"` + name + `","CFBundleShortVersionString":"1.0"}`),
	}
}

func softwareOpts(f fixture, apps string, fake *platform.Fake) engine.SoftwareOptions {
	return engine.SoftwareOptions{
		Home: f.home, StorePath: f.storePath, Runner: fake, Now: f.now,
		AppDirs: []string{apps}, BinDirs: []string{}, JavaDirs: []string{}, HasTool: func(string) bool { return false },
	}
}

// evaluatedTempDir is a fresh home directory with symlinks resolved, the way
// the scan records paths.
func evaluatedTempDir(t *testing.T) string {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return home
}

func dirNode(path string, bytes int64) model.Node {
	return model.Node{Path: path, Kind: model.NodeDir, Bytes: bytes, Files: 1, Depth: 3}
}

func TestSoftwareAttributesACacheFolderAndStoresTheSnapshot(t *testing.T) {
	apps := t.TempDir()
	fake := &platform.Fake{Responses: map[string]platform.Result{}}
	appBundle(t, apps, "Figma", "com.figma.Desktop", fake)

	home := evaluatedTempDir(t)
	cache := filepath.Join(home, "Library", "Caches", "com.figma.Desktop")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	f := newFixtureIn(t, home, time.Hour, dirNode(cache, 500))

	res, err := engine.Software(context.Background(), softwareOpts(f, apps, fake))
	if err != nil {
		t.Fatal(err)
	}
	var figma *engine.SoftwareEntry
	for i := range res.Software {
		if res.Software[i].ID == "app:com.figma.Desktop" {
			figma = &res.Software[i]
		}
	}
	if figma == nil {
		t.Fatalf("figma not listed: %+v", res.Software)
	}
	if figma.Bytes != 500 || len(figma.Folders) != 1 || figma.Folders[0].Path != cache {
		t.Errorf("figma = %+v", figma)
	}
	if figma.Installed == nil {
		t.Errorf("the install time of an app folder should be known")
	}
	if len(res.Unattributed) != 0 {
		t.Errorf("unattributed = %+v", res.Unattributed)
	}
	inv, err := inventoryOf(t, f.storePath, f.scanID)
	if err != nil || len(inv.Software) == 0 || len(inv.Owners) != 1 {
		t.Errorf("snapshot not stored: %+v, %v", inv, err)
	}
}

func TestSoftwareReportsUnexplainedFoldersLargestFirst(t *testing.T) {
	apps := t.TempDir()
	fake := &platform.Fake{Responses: map[string]platform.Result{}}
	home := evaluatedTempDir(t)
	small := filepath.Join(home, "Library", "Caches", "com.unknown.small")
	big := filepath.Join(home, "Library", "Caches", "com.unknown.big")
	f := newFixtureIn(t, home, time.Hour, dirNode(small, 10), dirNode(big, 900))

	res, err := engine.Software(context.Background(), softwareOpts(f, apps, fake))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Unattributed) != 2 || res.Unattributed[0].Path != big || res.Unattributed[0].Bytes != 900 {
		t.Errorf("unattributed = %+v; want the 900-byte folder first", res.Unattributed)
	}
}

func TestSoftwareNewSincePreviousSnapshot(t *testing.T) {
	apps := t.TempDir()
	fake := &platform.Fake{Responses: map[string]platform.Result{}}
	appBundle(t, apps, "Figma", "com.figma.Desktop", fake)

	first := newFixture(t, 2*time.Hour)
	if _, err := engine.Software(context.Background(), softwareOpts(first, apps, fake)); err != nil {
		t.Fatal(err)
	}

	// A newer scan, taken after Slack was installed.
	appBundle(t, apps, "Slack", "com.tinyspeck.slackmacgap", fake)
	second := newFixtureIn(t, first.home, time.Hour)
	res, err := engine.Software(context.Background(), softwareOpts(second, apps, fake))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.New) != 1 || res.New[0] != "app:com.tinyspeck.slackmacgap" {
		t.Errorf("new = %v; want only Slack", res.New)
	}
	if len(res.Warnings) != 0 {
		t.Errorf("warnings = %v", res.Warnings)
	}
}

func TestSoftwareWithoutAnEarlierSnapshotSaysSo(t *testing.T) {
	apps := t.TempDir()
	fake := &platform.Fake{Responses: map[string]platform.Result{}}
	// The earlier scan was never inventoried, as before this feature existed.
	first := newFixture(t, 2*time.Hour)
	second := newFixtureIn(t, first.home, time.Hour)
	res, err := engine.Software(context.Background(), softwareOpts(second, apps, fake))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.New) != 0 {
		t.Errorf("nothing should be called new without a comparison: %v", res.New)
	}
	if len(res.Warnings) != 1 {
		t.Errorf("want one warning explaining the missing comparison, got %v", res.Warnings)
	}
}

func TestSoftwareNeedsACompleteScan(t *testing.T) {
	_, err := engine.Software(context.Background(), engine.SoftwareOptions{
		Home: t.TempDir(), Runner: &platform.Fake{}, HasTool: func(string) bool { return false },
	})
	if !errors.Is(err, engine.ErrNoScan) {
		t.Errorf("err = %v, want ErrNoScan", err)
	}
}

// inventoryOf reads back the snapshot stored for a scan.
func inventoryOf(t *testing.T, path, scanID string) (store.Inventory, error) {
	t.Helper()
	st, err := store.Open(path)
	if err != nil {
		return store.Inventory{}, err
	}
	defer func() { _ = st.Close() }()
	return st.Inventory(scanID)
}

// Anything big under home shows up as unexplained, wherever it is, not only in
// the folders discovery knows about. A folder nested in a listed one is left
// out, and so is a folder inside an owned one, so no byte is counted twice.
func TestSoftwareUnexplainedCoversAllOfHome(t *testing.T) {
	apps := t.TempDir()
	fake := &platform.Fake{Responses: map[string]platform.Result{}}
	appBundle(t, apps, "Figma", "com.figma.Desktop", fake)
	home := evaluatedTempDir(t)
	cache := filepath.Join(home, "Library", "Caches", "com.figma.Desktop")
	downloads := filepath.Join(home, "Downloads", "big")
	f := newFixtureIn(t, home, time.Hour,
		dirNode(cache, 100),
		dirNode(filepath.Join(home, "Downloads"), 900),
		dirNode(downloads, 800),
		dirNode(filepath.Join(downloads, "inner"), 700),
		dirNode(filepath.Join(cache, "sub"), 50),
	)
	res, err := engine.Software(context.Background(), softwareOpts(f, apps, fake))
	if err != nil {
		t.Fatal(err)
	}
	paths := []string{}
	for _, u := range res.Unattributed {
		paths = append(paths, u.Path)
	}
	if !slices.Contains(paths, filepath.Join(home, "Downloads")) {
		t.Errorf("a large folder outside the fixed roots should be listed: %v", paths)
	}
	for _, p := range []string{downloads, filepath.Join(downloads, "inner"), filepath.Join(cache, "sub")} {
		if slices.Contains(paths, p) {
			t.Errorf("%s is inside a folder already counted and must not be listed again: %v", p, paths)
		}
	}
}

// A program on PATH that a package manager already lists is counted once, under
// the package manager. An App Store entry for an app that is installed as a
// bundle is counted once, as the bundle.
func TestSoftwareCountsEachInstallOnce(t *testing.T) {
	apps := t.TempDir()
	fake := &platform.Fake{Responses: map[string]platform.Result{
		"pip3 list --format=json": {Stdout: []byte(`[{"name": "pip", "version": "24.0"}]`)},
		"mas list":                {Stdout: []byte("497799835  Xcode  (16.2)\n")},
	}}
	appBundle(t, apps, "Xcode", "com.apple.dt.Xcode", fake)
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "pip"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	f := newFixture(t, time.Hour)
	opts := softwareOpts(f, apps, fake)
	opts.BinDirs = []string{bin}
	opts.HasTool = func(name string) bool { return name == "pip3" || name == "mas" }
	res, err := engine.Software(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	findEntry(t, res, "pip:pip")
	for _, sw := range res.Software {
		if sw.ID == "tool:pip" {
			t.Error("pip is on PATH and listed by pip3; it must be counted once")
		}
		if sw.ID == "store:497799835" {
			t.Error("Xcode is installed as a bundle; the App Store entry must not count again")
		}
	}
}

// A program is only called new when its source ran OK in the previous scan. A
// source that was absent or failed then can't show what was there, so its
// programs are not reported as new.
func TestSoftwareNewNeedsThePreviousSourceToHaveRun(t *testing.T) {
	apps := t.TempDir()
	fake := &platform.Fake{Responses: map[string]platform.Result{
		"npm ls -g --depth=0 --json": {Stdout: []byte(`{"dependencies":{"yarn":{"version":"1.22.22"}}}`)},
	}}
	first := newFixture(t, 2*time.Hour)
	opts := softwareOpts(first, apps, fake)
	opts.HasTool = func(name string) bool { return false } // npm absent last time
	if _, err := engine.Software(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	second := newFixtureIn(t, first.home, time.Hour)
	opts = softwareOpts(second, apps, fake)
	opts.HasTool = func(name string) bool { return name == "npm" }
	res, err := engine.Software(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(res.New, "npm:yarn") {
		t.Errorf("yarn came from a source that was absent last time and must not be called new: %v", res.New)
	}
}
