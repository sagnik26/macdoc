package tests

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sagnik26/macdoc/internal/recipes"
)

func fingerprint(t *testing.T, f brewFixture) string {
	t.Helper()
	fp, err := recipes.BrewFingerprint(f.home, f.bin)
	if err != nil {
		t.Fatal(err)
	}
	return fp
}

func TestFingerprintStable(t *testing.T) {
	f := newBrewFixture(t, 10)
	if a, b := fingerprint(t, f), fingerprint(t, f); a != b {
		t.Fatal("the fingerprint changed with nothing changed")
	}
	// The prefix comes from the brew path (two levels up), not a fixed
	// /opt/homebrew: Cellar changes under this temporary prefix are seen
	// (TestCacheInvalidatedByHomebrewState), and a different home differs.
	other := brewFixture{prefix: f.prefix, home: t.TempDir(), bin: f.bin}
	if fingerprint(t, f) == fingerprint(t, other) {
		t.Error("a home without Homebrew's cache folder has the same fingerprint")
	}
}

// Missing Caskroom, cache and downloads folders are normal and stable.
func TestFingerprintMissingFoldersAreMarkers(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin", "brew")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "nohome")
	a, err := recipes.BrewFingerprint(home, bin)
	if err != nil {
		t.Fatalf("missing folders failed the fingerprint: %v", err)
	}
	if b, _ := recipes.BrewFingerprint(home, bin); a != b {
		t.Error("the marker for a missing folder is not stable")
	}
	if err := os.MkdirAll(filepath.Join(root, "Cellar", "wget", "1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if b, _ := recipes.BrewFingerprint(home, bin); a == b {
		t.Error("creating Cellar did not change the fingerprint")
	}
}

func TestFingerprintErrorsAreReported(t *testing.T) {
	if _, err := recipes.BrewFingerprint("", "/x/bin/brew"); err == nil {
		t.Error("empty home accepted")
	}
	if _, err := recipes.BrewFingerprint(t.TempDir(), ""); err == nil {
		t.Error("empty brew path accepted")
	}
	if _, err := recipes.BrewFingerprint(t.TempDir(), filepath.Join(t.TempDir(), "bin", "brew")); err == nil {
		t.Error("a brew binary that does not exist was accepted")
	}
}

// A link inside Cellar is hashed as the link; what it points at is not read.
func TestFingerprintDoesNotFollowLinks(t *testing.T) {
	f := newBrewFixture(t, 3)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(f.prefix, "Cellar", "linked")); err != nil {
		t.Fatal(err)
	}
	before := fingerprint(t, f)
	if err := os.WriteFile(filepath.Join(outside, "new"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(outside, later, later); err != nil {
		t.Fatal(err)
	}
	if fingerprint(t, f) != before {
		t.Error("a change behind a link changed the fingerprint, so the link was followed")
	}
}

// 300 formulae with three versions each: cheap, and no brew command involved.
func TestFingerprintCostWith300Formulae(t *testing.T) {
	w2WithTools(t, "brew")
	f := newBrewFixture(t, 0)
	for i := range 300 {
		for _, v := range []string{"1.0", "1.1", "2.0"} {
			f.mkdir(t, "Cellar", fmt.Sprintf("formula%03d", i), v)
		}
	}
	best := time.Hour
	for range 5 {
		start := time.Now()
		fingerprint(t, f)
		best = min(best, time.Since(start))
	}
	if best > 50*time.Millisecond {
		t.Errorf("fingerprint of 300 formulae took %v; want under 50ms", best)
	}
	t.Logf("fingerprint of 300 formulae x 3 versions: %v", best)

	// With a warm cache the fingerprint is the only work: brew is not run.
	run, c := cleanupRunner(), &memCache{}
	env := f.env(c, run, w2Now)
	detectCleanup(t, env)
	for range 5 {
		if _, err := recipes.RunCachedDryRun(context.Background(), env, w2Recipe(t, "brew.cleanup"), recipes.BrewTimeout); err != nil {
			t.Fatal(err)
		}
	}
	if len(run.Calls) != 1 {
		t.Errorf("brew ran %d times; the warm calls must not run it", len(run.Calls))
	}
}
