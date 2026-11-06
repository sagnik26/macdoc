package tests

import (
	"errors"
	"github.com/sagnik26/macdoc/internal/recipes"
	"strconv"
	"testing"

	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/platform"
	"github.com/sagnik26/macdoc/internal/testutil/fstree"
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
	// Every parent is a project, so only the rule under test rejects it.
	for _, d := range []string{"api", "fresh", "nocfg", "linked", "plain", "zero", "api/.venv/lib/x"} {
		fstree.Build(t, home, fstree.File("code/"+d+"/requirements.txt", 10))
	}
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
	fs, err := w2Run(t, "python.venv_stale", recipes.Env{Home: home, Nodes: nodes})
	w2WantTargets(t, fs, err, home+"/code/api/.venv")
	if fs[0].Title != ".venv in api" {
		t.Errorf("title %q", fs[0].Title)
	}
}

func TestVenvProjectRules(t *testing.T) {
	home := t.TempDir()
	markers := []string{"pyproject.toml", "requirements.txt", "requirements-dev.txt", "setup.py", "setup.cfg", "Pipfile", "poetry.lock", "uv.lock"}
	var entries []fstree.Entry
	var nodes fakeNodes
	var want []string
	add := func(dir string, extra ...fstree.Entry) {
		entries = append(entries, fstree.File(dir+"/.venv/pyvenv.cfg", 10))
		entries = append(entries, extra...)
		nodes = append(nodes, model.Node{Path: home + "/" + dir + "/.venv", Bytes: 300 << 20, MtimeMax: w2DaysAgo(200)})
	}
	for i, m := range markers { // positives: one marker each
		dir := "code/p" + strconv.Itoa(i)
		add(dir, fstree.File(dir+"/"+m, 10))
		want = append(want, home+"/"+dir+"/.venv")
	}
	// Look-alikes: tool and app trees, even with a project file.
	for _, dir := range []string{".local/share/pipx/venvs/black", ".cursor/extensions/ms-python-1.0", "Library/Application Support/SomeApp/python"} {
		add(dir, fstree.File(dir+"/pyproject.toml", 10))
	}
	add("code/nomarker", fstree.File("code/nomarker/README.md", 10))
	add("code/reqdir", fstree.Dir("code/reqdir/requirements.txt"))                             // marker is a directory
	add("code/reqlink", fstree.Symlink("code/reqlink/pyproject.toml", "../p0/pyproject.toml")) // marker is a symlink
	add("code/reqlook", fstree.File("code/reqlook/requirements.md", 10), fstree.File("code/reqlook/my-requirements.txt", 10))
	fstree.Build(t, home, entries...)

	fs, err := w2Run(t, "python.venv_stale", recipes.Env{Home: home, Nodes: nodes})
	w2WantTargets(t, fs, err, want...)
	if recipes.IsPythonProject(home + "/missing") {
		t.Error("missing dir is a project")
	}
	fs, err = w2Run(t, "python.venv_stale", recipes.Env{})
	w2WantNone(t, fs, err)
	if _, err := w2Run(t, "python.venv_stale", recipes.Env{Nodes: w2ErrNodes{}}); err == nil {
		t.Error("Nodes error swallowed")
	}
}

func TestCondaPkgs(t *testing.T) {
	w2WithTools(t, "conda")
	run := func(res platform.Result) ([]recipes.Finding, error) {
		return w2Run(t, "conda.pkgs", recipes.Env{Runner: w2Fake(map[string]platform.Result{condaKey: res})})
	}
	fs, err := run(platform.Result{Stdout: w2Fixture(t, "w2_conda_clean_dry_run.txt")})
	if err != nil || len(fs) != 1 {
		t.Fatalf("got %+v, %v", fs, err)
	}
	tarballs, _ := recipes.ParseSize("345.6 MB", 1024)
	pkgs, _ := recipes.ParseSize("1.2 GB", 1024)
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
	if _, err := w2Run(t, "conda.pkgs", recipes.Env{Runner: &platform.FakeRunner{Errors: map[string]error{condaKey: errors.New("timeout")}}}); err == nil {
		t.Error("runner error swallowed")
	}
	if _, err := recipes.DetectCondaPkgs(t.Context(), recipes.Env{Runner: w2Fake(nil)}, recipes.Recipe{ID: "x.y"}); err == nil {
		t.Error("recipe without dry-run accepted")
	}
}

func TestCondaMissing(t *testing.T) {
	w2WithTools(t)
	runner := w2Fake(nil)
	fs, err := w2Run(t, "conda.pkgs", recipes.Env{Runner: runner})
	w2WantNone(t, fs, err)
	if len(runner.Calls) != 0 {
		t.Errorf("ran %v without conda", runner.Calls)
	}
}
