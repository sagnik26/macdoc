package recipes

import (
	"errors"
	"testing"

	"macdoc/internal/model"
	"macdoc/internal/platform"
	"macdoc/internal/testutil/fstree"
)

const condaKey = "conda clean --all --dry-run"

func TestVenvStale(t *testing.T) {
	home := t.TempDir()
	fstree.Build(t, home,
		fstree.File("code/api/.venv/pyvenv.cfg", 10),
		fstree.File("code/fresh/.venv/pyvenv.cfg", 10),
		fstree.Dir("code/nocfg/.venv/lib"),                                           // no pyvenv.cfg
		fstree.Symlink("code/linked/.venv/pyvenv.cfg", "../../api/.venv/pyvenv.cfg"), // marker is a symlink
		fstree.File("code/plain/venv/pyvenv.cfg", 10),                                // generic name
		fstree.File("code/api/.venv/lib/x/.venv/pyvenv.cfg", 10),                     // nested
	)
	old, recent := w2DaysAgo(200), w2DaysAgo(5)
	nodes := fakeNodes{
		{Path: home + "/code/api/.venv", Bytes: 300 << 20, MtimeMax: old},
		{Path: home + "/code/api/.venv/lib/x/.venv", Bytes: 100 << 20, MtimeMax: old},
		{Path: home + "/code/fresh/.venv", Bytes: 300 << 20, MtimeMax: recent},
		{Path: home + "/code/nocfg/.venv", Bytes: 300 << 20, MtimeMax: old},
		{Path: home + "/code/linked/.venv", Bytes: 300 << 20, MtimeMax: old},
		{Path: home + "/code/plain/venv", Bytes: 300 << 20, MtimeMax: old},
		{Path: home + "/code/gone/.venv", Bytes: 300 << 20, MtimeMax: old}, // deleted since the scan
		{Path: home + "/code/zero/.venv", MtimeMax: old},
	}
	fs, err := w2Run(t, "python.venv_stale", Env{Home: home, Nodes: nodes})
	w2WantTargets(t, fs, err, home+"/code/api/.venv")
	if fs[0].Title != ".venv in api" {
		t.Errorf("title %q", fs[0].Title)
	}
	fs, err = w2Run(t, "python.venv_stale", Env{})
	w2WantNone(t, fs, err)
	if _, err := w2Run(t, "python.venv_stale", Env{Nodes: w2ErrNodes{}}); err == nil {
		t.Error("Nodes error swallowed")
	}
}

func TestCondaPkgs(t *testing.T) {
	w2WithTools(t, "conda")
	run := func(res platform.Result) ([]Finding, error) {
		return w2Run(t, "conda.pkgs", Env{Runner: w2Fake(map[string]platform.Result{condaKey: res})})
	}
	fs, err := run(platform.Result{Stdout: w2Fixture(t, "w2_conda_clean_dry_run.txt")})
	if err != nil || len(fs) != 1 {
		t.Fatalf("got %+v, %v", fs, err)
	}
	tarballs, _ := parseSize("345.6 MB", 1024)
	pkgs, _ := parseSize("1.2 GB", 1024)
	want := tarballs + pkgs
	if fs[0].Bytes.Source != model.SourceTool || fs[0].Bytes.Value != want || fs[0].Note != "Would remove 12 tarball(s), 30 package(s)" {
		t.Errorf("finding %+v, want %d", fs[0], want)
	}
	fs, err = run(platform.Result{Stdout: []byte("There are no unused tarball(s) to remove.\nWill remove 1 index cache(s).\n")})
	w2WantNone(t, fs, err)
	if _, err := run(platform.Result{ExitCode: 1}); err == nil {
		t.Error("non-zero exit accepted")
	}
	if _, err := run(platform.Result{Stdout: []byte("Will remove 3 (1.2.3 MB) package(s).\n")}); err == nil {
		t.Error("bad size accepted")
	}
	if _, err := w2Run(t, "conda.pkgs", Env{Runner: &platform.FakeRunner{Errors: map[string]error{condaKey: errors.New("timeout")}}}); err == nil {
		t.Error("runner error swallowed")
	}
	if _, err := detectCondaPkgs(t.Context(), Env{Runner: w2Fake(nil)}, Recipe{ID: "x.y"}); err == nil {
		t.Error("recipe without dry-run accepted")
	}
}

func TestCondaMissing(t *testing.T) {
	w2WithTools(t)
	runner := w2Fake(nil)
	fs, err := w2Run(t, "conda.pkgs", Env{Runner: runner})
	w2WantNone(t, fs, err)
	if len(runner.Calls) != 0 {
		t.Errorf("ran %v without conda", runner.Calls)
	}
}
