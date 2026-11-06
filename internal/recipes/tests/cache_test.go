package tests

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sagnik26/macdoc/internal/platform"
	"github.com/sagnik26/macdoc/internal/recipes"
)

// memCache is an in-memory recipes.DryRunCache that can be made to fail.
type memCache struct {
	runs     map[string]recipes.Cached
	loads    int
	saves    int
	saveErr  error
	loadMiss bool
}

func (m *memCache) Load(key string) (recipes.Cached, bool) {
	m.loads++
	if m.loadMiss {
		return recipes.Cached{}, false
	}
	r, ok := m.runs[key]
	return r, ok
}

func (m *memCache) Save(key string, run recipes.Cached) error {
	m.saves++
	if m.saveErr != nil {
		return m.saveErr
	}
	if m.runs == nil {
		m.runs = map[string]recipes.Cached{}
	}
	m.runs[key] = run
	return nil
}

// brewFixture is a Homebrew prefix and a home laid out like the real ones.
type brewFixture struct {
	prefix, home, bin string
}

func newBrewFixture(t *testing.T, formulae int) brewFixture {
	t.Helper()
	root := t.TempDir()
	f := brewFixture{prefix: filepath.Join(root, "opt", "homebrew"), home: filepath.Join(root, "home")}
	f.bin = filepath.Join(f.prefix, "bin", "brew")
	for _, d := range []string{filepath.Join(f.prefix, "bin"), filepath.Join(f.home, "Library", "Caches", "Homebrew", "downloads")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(f.bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for i := range formulae {
		f.mkdir(t, "Cellar", fmt.Sprintf("formula%03d", i), "1.0", "bin")
	}
	old := recipes.BrewBinary
	recipes.BrewBinary = func() (string, error) { return f.bin, nil }
	t.Cleanup(func() { recipes.BrewBinary = old })
	return f
}

func (f brewFixture) mkdir(t *testing.T, parts ...string) string {
	t.Helper()
	p := filepath.Join(append([]string{f.prefix}, parts...)...)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func (f brewFixture) env(c recipes.Cache, r platform.Runner, now time.Time) recipes.Env {
	return recipes.Env{Home: f.home, Runner: r, Now: now, Cache: c}
}

const cleanupOut = "Would remove: /x/y (10MB)\n==> This operation would free approximately 10MB of disk space.\n"

func cleanupRunner() *platform.Fake {
	return w2Fake(map[string]platform.Result{
		brewCleanupKey:    {Stdout: []byte(cleanupOut)},
		brewAutoremoveKey: {Stdout: []byte("==> Would autoremove 1 unneeded formula:\nlibfoo\n")},
	})
}

func detectCleanup(t *testing.T, env recipes.Env) {
	t.Helper()
	fs, err := w2Run(t, "brew.cleanup", env)
	if err != nil || len(fs) != 1 || fs[0].Bytes.Value != 10<<20 {
		t.Fatalf("brew.cleanup = %+v, %v", fs, err)
	}
}

func TestCacheReusesWithinTTL(t *testing.T) {
	w2WithTools(t, "brew")
	f := newBrewFixture(t, 5)
	run, c := cleanupRunner(), &memCache{}
	env := f.env(c, run, w2Now)
	detectCleanup(t, env)
	detectCleanup(t, env)
	detectCleanup(t, f.env(c, run, w2Now.Add(recipes.DryRunTTL-time.Second)))
	if len(run.Calls) != 1 {
		t.Errorf("brew ran %d times; want 1 (later calls reuse the stored run)", len(run.Calls))
	}
}

func TestCacheExpires(t *testing.T) {
	w2WithTools(t, "brew")
	f := newBrewFixture(t, 5)
	run, c := cleanupRunner(), &memCache{}
	detectCleanup(t, f.env(c, run, w2Now))
	detectCleanup(t, f.env(c, run, w2Now.Add(recipes.DryRunTTL))) // exactly the TTL: stale
	if len(run.Calls) != 2 {
		t.Fatalf("after the TTL brew ran %d times; want 2", len(run.Calls))
	}
	// An entry that claims to be from the future (clock set back) is not trusted.
	detectCleanup(t, f.env(c, run, w2Now.Add(recipes.DryRunTTL-time.Hour)))
	if len(run.Calls) != 3 {
		t.Errorf("an entry from the future was reused (%d runs)", len(run.Calls))
	}
}

func TestCacheInvalidatedByHomebrewState(t *testing.T) {
	later := w2Now.Add(time.Hour)
	changes := map[string]func(t *testing.T, f brewFixture){
		"a Cellar entry changes mtime": func(t *testing.T, f brewFixture) {
			if err := os.Chtimes(filepath.Join(f.prefix, "Cellar", "formula002"), later, later); err != nil {
				t.Fatal(err)
			}
		},
		"a version folder is added": func(t *testing.T, f brewFixture) {
			f.mkdir(t, "Cellar", "formula001", "2.0")
		},
		"a version folder changes mtime": func(t *testing.T, f brewFixture) {
			if err := os.Chtimes(filepath.Join(f.prefix, "Cellar", "formula001", "1.0"), later, later); err != nil {
				t.Fatal(err)
			}
		},
		"a version folder is removed": func(t *testing.T, f brewFixture) {
			if err := os.Remove(filepath.Join(f.prefix, "Cellar", "formula001", "1.0", "bin")); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(filepath.Join(f.prefix, "Cellar", "formula001", "1.0")); err != nil {
				t.Fatal(err)
			}
		},
		"a formula is added": func(t *testing.T, f brewFixture) { f.mkdir(t, "Cellar", "newformula", "1") },
		"a cask appears":     func(t *testing.T, f brewFixture) { f.mkdir(t, "Caskroom", "somecask") },
		"a download appears": func(t *testing.T, f brewFixture) {
			p := filepath.Join(f.home, "Library", "Caches", "Homebrew", "downloads", "abc--x.tar.gz")
			if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"a cache entry appears": func(t *testing.T, f brewFixture) {
			if err := os.WriteFile(filepath.Join(f.home, "Library", "Caches", "Homebrew", "api"), []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"brew itself is updated": func(t *testing.T, f brewFixture) {
			if err := os.Chtimes(f.bin, later, later); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			w2WithTools(t, "brew")
			f := newBrewFixture(t, 5)
			run, c := cleanupRunner(), &memCache{}
			env := f.env(c, run, w2Now)
			detectCleanup(t, env)
			change(t, f)
			detectCleanup(t, env)
			if len(run.Calls) != 2 {
				t.Errorf("brew ran %d times; want 2 (the change must invalidate)", len(run.Calls))
			}
		})
	}
}

func TestCacheNeverStoresFailures(t *testing.T) {
	w2WithTools(t, "brew")
	f := newBrewFixture(t, 3)
	c := &memCache{}
	bad := w2Fake(map[string]platform.Result{brewCleanupKey: {ExitCode: 1, Stderr: []byte("Error: boom")}})
	for range 2 {
		if _, err := w2Run(t, "brew.cleanup", f.env(c, bad, w2Now)); err == nil {
			t.Fatal("a non-zero exit was accepted")
		}
	}
	if len(bad.Calls) != 2 || c.saves != 0 {
		t.Errorf("non-zero exit: %d runs, %d saves; want 2 runs, nothing stored", len(bad.Calls), c.saves)
	}
	broken := &platform.Fake{Errors: map[string]error{brewCleanupKey: errors.New("timeout")}}
	for range 2 {
		if _, err := w2Run(t, "brew.cleanup", f.env(c, broken, w2Now)); err == nil {
			t.Fatal("a runner error was accepted")
		}
	}
	if len(broken.Calls) != 2 || c.saves != 0 {
		t.Errorf("runner error: %d runs, %d saves; want 2 runs, nothing stored", len(broken.Calls), c.saves)
	}
}

func TestCacheProblemsFallBackToTheRealCommand(t *testing.T) {
	cases := []struct {
		name  string
		setup func(f brewFixture, c *memCache)
		runs  int // brew runs over two detections
	}{
		{"save fails", func(_ brewFixture, c *memCache) { c.saveErr = errors.New("database is locked") }, 2},
		{"load misses", func(_ brewFixture, c *memCache) { c.loadMiss = true }, 2},
		{"stored fingerprint is garbage", func(_ brewFixture, c *memCache) {
			c.runs = map[string]recipes.Cached{brewCleanupKey: {Fingerprint: "garbage", At: w2Now, Result: platform.Result{Stdout: []byte("junk")}}}
		}, 1}, // not used, replaced by a good entry, then reused
		{"brew path unknown", func(_ brewFixture, _ *memCache) {
			recipes.BrewBinary = func() (string, error) { return "", errors.New("not found") }
		}, 2},
		{"brew binary gone", func(f brewFixture, _ *memCache) { _ = os.Remove(f.bin) }, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w2WithTools(t, "brew")
			f := newBrewFixture(t, 3)
			run, c := cleanupRunner(), &memCache{}
			tc.setup(f, c)
			env := f.env(c, run, w2Now)
			detectCleanup(t, env) // a cache problem never fails the command
			detectCleanup(t, env)
			if len(run.Calls) != tc.runs {
				t.Errorf("brew ran %d times; want %d", len(run.Calls), tc.runs)
			}
		})
	}
}

func TestCacheKeepsCommandsApart(t *testing.T) {
	w2WithTools(t, "brew")
	f := newBrewFixture(t, 3)
	run, c := cleanupRunner(), &memCache{}
	env := f.env(c, run, w2Now)
	for range 2 {
		detectCleanup(t, env)
		fs, err := w2Run(t, "brew.autoremove", env)
		if err != nil || len(fs) != 1 || fs[0].Note != "Would uninstall 1 formula: libfoo" {
			t.Fatalf("autoremove = %+v, %v", fs, err)
		}
	}
	if len(run.Calls) != 2 || len(c.runs) != 2 {
		t.Errorf("%d runs and %d entries; want one of each per command", len(run.Calls), len(c.runs))
	}
}

func TestNoCacheMeansEveryCallRuns(t *testing.T) {
	w2WithTools(t, "brew")
	f := newBrewFixture(t, 3)
	run := cleanupRunner()
	env := f.env(nil, run, w2Now) // what the executor's re-check uses
	detectCleanup(t, env)
	detectCleanup(t, env)
	if len(run.Calls) != 2 {
		t.Errorf("without a cache brew ran %d times; want 2", len(run.Calls))
	}
}
