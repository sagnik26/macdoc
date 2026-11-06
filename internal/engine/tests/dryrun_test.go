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
)

// Opportunities keeps the brew dry run in the state database, so a second
// command within the TTL does not run brew; Live asks brew again.
func TestOpportunitiesReuseBrewDryRun(t *testing.T) {
	f := newFixture(t, time.Hour)
	prefix := t.TempDir()
	bin := filepath.Join(prefix, "bin", "brew")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	oldBin, oldTool := recipes.BrewBinary, recipes.ToolInstalled
	recipes.BrewBinary = func() (string, error) { return bin, nil }
	recipes.ToolInstalled = func(name string) bool { return name == "brew" }
	t.Cleanup(func() { recipes.BrewBinary, recipes.ToolInstalled = oldBin, oldTool })

	cat, err := recipes.Load()
	if err != nil {
		t.Fatal(err)
	}
	r, ok := cat.Get("brew.cleanup")
	if !ok {
		t.Fatal("brew.cleanup not in the catalog")
	}
	runner := &platform.FakeRunner{Responses: map[string]platform.Result{
		"brew cleanup -n --prune=all": {Stdout: []byte("Would remove: /x (10MB)\n==> This operation would free approximately 10MB of disk space.\n")},
	}}
	o := f.opts(r)
	o.Runner = runner
	for range 2 {
		res, err := engine.Opportunities(context.Background(), o)
		if err != nil || len(res.Opportunities) != 1 {
			t.Fatalf("got %+v, %v", res, err)
		}
	}
	if len(runner.Calls) != 1 {
		t.Fatalf("brew ran %d times for two commands; want 1", len(runner.Calls))
	}
	o.Live = true
	if _, err := engine.Opportunities(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	if len(runner.Calls) != 2 {
		t.Errorf("Live: brew ran %d times in total; want 2", len(runner.Calls))
	}
}
