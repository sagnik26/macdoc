package tests

import (
	"errors"
	"strings"
	"testing"

	"github.com/sagnik26/macdoc/internal/recipes"

	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/platform"
)

const (
	brewCleanupKey    = "brew cleanup -n --prune=all"
	brewAutoremoveKey = "brew autoremove -n"
)

func TestBrewCleanupRealFixture(t *testing.T) {
	w2WithTools(t, "brew")
	raw := w2Fixture(t, "brew_cleanup_n.txt")
	// brew prints warnings on stderr and the plan on stdout; the fixture
	// interleaves both, so feed it as stdout.
	runner := w2Fake(map[string]platform.Result{brewCleanupKey: {Stdout: raw}})
	fs, err := w2Run(t, "brew.cleanup", recipes.Env{Runner: runner})
	if err != nil || len(fs) != 1 {
		t.Fatalf("got %+v, %v", fs, err)
	}
	f := fs[0]
	if f.Bytes.Source != model.SourceTool || f.Bytes.Value != 377<<20 {
		t.Errorf("bytes = %+v, want ≈ 377 MB from the tool", f.Bytes)
	}
	want := strings.Count(string(raw), "Would remove:")
	if want == 0 || !strings.HasPrefix(f.Note, recipes.Plural(want, "item", "items")) {
		t.Errorf("note = %q, want %d items", f.Note, want)
	}
	if len(runner.Calls) != 1 || runner.Calls[0].Timeout != recipes.BrewTimeout {
		t.Errorf("calls = %+v", runner.Calls)
	}
}

func TestBrewCleanupCases(t *testing.T) {
	w2WithTools(t, "brew")
	run := func(res platform.Result) ([]recipes.Finding, error) {
		return w2Run(t, "brew.cleanup", recipes.Env{Runner: w2Fake(map[string]platform.Result{brewCleanupKey: res})})
	}
	// Nothing to clean.
	fs, err := run(platform.Result{Stderr: []byte("Warning: Skipping x: most recent version 1 not installed\n")})
	w2WantNone(t, fs, err)
	// Items but no summary line: unknown, never 0.
	fs, err = run(platform.Result{Stdout: []byte("Would remove: /Users/me/Library/Caches/Homebrew/x (1KB)\n")})
	if err != nil || len(fs) != 1 || fs[0].Bytes.Source != model.SourceUnknown || fs[0].Bytes.Reason == "" || fs[0].Note != "1 item (old versions, downloads and logs)" {
		t.Fatalf("got %+v, %v", fs, err)
	}
	// GB summary on stderr.
	fs, err = run(platform.Result{Stdout: []byte("Would remove: x (1GB)\n"), Stderr: []byte("==> This operation would free approximately 1.5GB of disk space.\n")})
	if err != nil || len(fs) != 1 || fs[0].Bytes.Value != 1536<<20 {
		t.Fatalf("got %+v, %v", fs, err)
	}
	if _, err := run(platform.Result{ExitCode: 1, Stderr: []byte("Error: boom")}); err == nil {
		t.Error("non-zero exit accepted")
	}
	if _, err := w2Run(t, "brew.cleanup", recipes.Env{Runner: &platform.Fake{Errors: map[string]error{brewCleanupKey: errors.New("timeout")}}}); err == nil {
		t.Error("runner error swallowed")
	}
	if _, err := recipes.DetectBrewCleanup(t.Context(), recipes.Env{Runner: w2Fake(nil)}, recipes.Recipe{ID: "x.y"}); err == nil {
		t.Error("recipe without dry-run accepted")
	}
}

func TestBrewMissing(t *testing.T) {
	w2WithTools(t)
	for _, id := range []string{"brew.cleanup", "brew.autoremove"} {
		runner := w2Fake(nil)
		fs, err := w2Run(t, id, recipes.Env{Runner: runner})
		w2WantNone(t, fs, err)
		if len(runner.Calls) != 0 {
			t.Errorf("%s ran %v without brew", id, runner.Calls)
		}
		fs, err = w2Run(t, id, recipes.Env{})
		w2WantNone(t, fs, err)
	}
}

func TestBrewAutoremove(t *testing.T) {
	w2WithTools(t, "brew")
	run := func(res platform.Result) ([]recipes.Finding, error) {
		return w2Run(t, "brew.autoremove", recipes.Env{Runner: w2Fake(map[string]platform.Result{brewAutoremoveKey: res})})
	}
	fs, err := run(platform.Result{Stdout: w2Fixture(t, "w2_brew_autoremove_n.txt")})
	if err != nil || len(fs) != 1 {
		t.Fatalf("got %+v, %v", fs, err)
	}
	if fs[0].Note != "Would uninstall 3 formulae: libfoo, libbar, python@3.11" ||
		fs[0].Bytes.Source != model.SourceUnknown || fs[0].Bytes.Reason == "" {
		t.Errorf("finding %+v", fs[0])
	}
	// Nothing to remove: brew prints nothing.
	fs, err = run(platform.Result{})
	w2WantNone(t, fs, err)
	// Lines outside the "Would autoremove" section are ignored.
	fs, err = run(platform.Result{Stdout: []byte("==> Something else\nnot-a-formula\n")})
	w2WantNone(t, fs, err)
	if _, err := run(platform.Result{ExitCode: 1}); err == nil {
		t.Error("non-zero exit accepted")
	}
	if _, err := recipes.DetectBrewAutoremove(t.Context(), recipes.Env{Runner: w2Fake(nil)}, recipes.Recipe{ID: "x.y"}); err == nil {
		t.Error("recipe without dry-run accepted")
	}
}
