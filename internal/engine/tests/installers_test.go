package tests

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sagnik26/macdoc/internal/engine"
	"github.com/sagnik26/macdoc/internal/platform"
	"github.com/sagnik26/macdoc/internal/recipes"
	"github.com/sagnik26/macdoc/internal/testutil/fstree"
)

// A real scan keeps small installers in Downloads (the scanner's own threshold
// would drop them), and the installer recipe suggests the old ones: positive
// for a 25 MB installer idle for 60 days, and nothing for a tiny installer,
// a recent one, or a look-alike name.
func TestScanThenInstallerSuggestion(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-60 * 24 * time.Hour)
	fstree.Build(t, home,
		fstree.File("Downloads/old.dmg", 25<<20).WithModTime(old),
		fstree.File("Downloads/tiny.dmg", 1<<20).WithModTime(old),     // below the 20 MB minimum
		fstree.File("Downloads/recent.pkg", 25<<20),                   // changed just now
		fstree.File("Downloads/old.dmg.txt", 25<<20).WithModTime(old), // look-alike, not an installer
	)

	if _, err := engine.Scan(context.Background(), engine.ScanOptions{Home: home, Root: home}); err != nil {
		t.Fatal(err)
	}
	cat, err := recipes.Load()
	if err != nil {
		t.Fatal(err)
	}
	r, ok := cat.Get("user.downloads_large_old")
	if !ok {
		t.Fatal("recipe user.downloads_large_old is missing")
	}
	stubTools(t)
	res, err := engine.Opportunities(context.Background(), engine.OpportunityOptions{
		Home: home, Catalog: &recipes.Catalog{Recipes: []recipes.Recipe{r}}, MinBytes: -1,
	})
	if err != nil {
		t.Fatal(err)
	}
	var targets []string
	for _, o := range res.Opportunities {
		targets = append(targets, o.Target)
	}
	if len(targets) != 1 || targets[0] != home+"/Downloads/old.dmg" {
		t.Errorf("targets = %v; want only %s/Downloads/old.dmg (warnings %v)", targets, home, res.Warnings)
	}
}

// A real scan keeps small leftover folders (the scanner's threshold would drop
// them), and the leftover recipe suggests only the one no installed app owns.
// Mutation check: with the installed-app check removed, the installed app's
// folder is suggested too and this test fails.
func TestScanThenLeftoverSuggestion(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-90 * 24 * time.Hour)
	fstree.Build(t, home,
		fstree.File("Library/Caches/com.gone.app/data.bin", 3<<20).WithModTime(old),
		fstree.Dir("Library/Caches/com.gone.app").WithModTime(old),
		fstree.File("Library/Caches/com.vendor.alive/data.bin", 3<<20).WithModTime(old),
		fstree.Dir("Library/Caches/com.vendor.alive").WithModTime(old),
		fstree.File("Library/Caches/com.gone.fresh/data.bin", 3<<20),
	)
	apps := t.TempDir()
	bundle := filepath.Join(apps, "Alive.app")
	if err := os.MkdirAll(filepath.Join(bundle, "Contents"), 0o755); err != nil {
		t.Fatal(err)
	}
	runner := &platform.Fake{Responses: map[string]platform.Result{
		"plutil -convert json -o - " + filepath.Join(bundle, "Contents", "Info.plist"): {Stdout: []byte(`{"CFBundleIdentifier":"com.vendor.alive"}`)},
	}}

	if _, err := engine.Scan(context.Background(), engine.ScanOptions{Home: home, Root: home}); err != nil {
		t.Fatal(err)
	}
	cat, err := recipes.Load()
	if err != nil {
		t.Fatal(err)
	}
	r, ok := cat.Get("apps.leftovers")
	if !ok {
		t.Fatal("recipe apps.leftovers is missing")
	}
	res, err := engine.Opportunities(context.Background(), engine.OpportunityOptions{
		Home: home, Runner: runner, AppDirs: []string{apps}, Catalog: &recipes.Catalog{Recipes: []recipes.Recipe{r}}, MinBytes: -1,
	})
	if err != nil {
		t.Fatal(err)
	}
	var targets []string
	for _, o := range res.Opportunities {
		targets = append(targets, o.Target)
	}
	if len(targets) != 1 || targets[0] != home+"/Library/Caches/com.gone.app" {
		t.Errorf("targets = %v; want only com.gone.app (warnings %v)", targets, res.Warnings)
	}
}
