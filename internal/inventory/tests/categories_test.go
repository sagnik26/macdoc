package tests

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/sagnik26/macdoc/internal/inventory"
	"github.com/sagnik26/macdoc/internal/platform"
)

// discoverFrom runs every source against fake output and returns what the
// named source found, plus its state.
func discoverFrom(t *testing.T, source string, fake *platform.Fake, tools func(string) bool, home string) ([]inventory.Software, inventory.Status) {
	t.Helper()
	if tools == nil {
		tools = func(string) bool { return true }
	}
	res := inventory.Discover(context.Background(), inventory.Env{
		Home: home, AppDirs: []string{}, BinDirs: []string{}, Runner: fake, HasTool: tools,
	})
	var got []inventory.Software
	for _, s := range res.Software {
		if s.Source == source {
			got = append(got, s)
		}
	}
	var st inventory.Status
	for _, s := range res.Statuses {
		if s.Source == source {
			st = s
		}
	}
	return got, st
}

func findSource(list []inventory.Software, id string) (inventory.Software, bool) {
	for _, s := range list {
		if s.ID == id {
			return s, true
		}
	}
	return inventory.Software{}, false
}

// Positive: pip3's JSON list gives each package and its version.
func TestPipPackages(t *testing.T) {
	fake := &platform.Fake{Responses: map[string]platform.Result{
		"pip3 list --format=json": {Stdout: []byte(`[{"name": "anyio", "version": "4.14.2"}, {"name": "attrs", "version": "26.1.0"}]`)},
	}}
	got, st := discoverFrom(t, "pip", fake, nil, t.TempDir())
	if st.State != "ok" {
		t.Fatalf("pip status = %+v", st)
	}
	if sw, ok := findSource(got, "pip:anyio"); !ok || sw.Kind != "pip" || sw.Version != "4.14.2" {
		t.Errorf("anyio = %+v, %v", sw, ok)
	}
	if len(got) != 2 {
		t.Errorf("got %d packages, want 2", len(got))
	}
}

// Precondition: pip3 not installed is absent, and bad JSON is unknown.
func TestPipAbsentAndBadOutput(t *testing.T) {
	_, st := discoverFrom(t, "pip", &platform.Fake{}, func(n string) bool { return n != "pip3" }, t.TempDir())
	if st.State != "absent" {
		t.Errorf("no pip3: status = %+v, want absent", st)
	}
	fake := &platform.Fake{Responses: map[string]platform.Result{"pip3 list --format=json": {Stdout: []byte("not json")}}}
	_, st = discoverFrom(t, "pip", fake, nil, t.TempDir())
	if st.State != "unknown" {
		t.Errorf("bad JSON: status = %+v, want unknown", st)
	}
}

// Positive: pipx lists one application per virtualenv, from its JSON.
func TestPipxApplications(t *testing.T) {
	fake := &platform.Fake{Responses: map[string]platform.Result{
		"pipx list --json": {Stdout: []byte(`{"venvs": {"black": {"metadata": {"main_package": {"package": "black", "package_version": "24.1.0"}}}}}`)},
	}}
	got, _ := discoverFrom(t, "pipx", fake, nil, t.TempDir())
	if sw, ok := findSource(got, "pipx:black"); !ok || sw.Kind != "pipx" || sw.Version != "24.1.0" {
		t.Errorf("black = %+v, %v", sw, ok)
	}
}

// Positive: `uv tool list` gives one line per tool; the indented command lines
// under it are not tools of their own.
func TestUvTools(t *testing.T) {
	fake := &platform.Fake{Responses: map[string]platform.Result{
		"uv tool list": {Stdout: []byte("graphifyy v0.9.12\n- graphify\n- graphify-mcp\n")},
	}}
	got, _ := discoverFrom(t, "uv-tool", fake, nil, t.TempDir())
	if len(got) != 1 {
		t.Fatalf("got %+v, want one tool", got)
	}
	if sw := got[0]; sw.Name != "graphifyy" || sw.Version != "0.9.12" || sw.Kind != "uv-tool" {
		t.Errorf("tool = %+v", sw)
	}
}

// Positive: gem lines are "name (version, other versions)"; the first version
// is the one reported.
func TestGems(t *testing.T) {
	fake := &platform.Fake{Responses: map[string]platform.Result{
		"gem list --local --no-details": {Stdout: []byte("activesupport (6.1.7.6)\nbigdecimal (3.1.4, default: 3.1.3)\n")},
	}}
	got, _ := discoverFrom(t, "gem", fake, nil, t.TempDir())
	if sw, ok := findSource(got, "gem:bigdecimal"); !ok || sw.Version != "3.1.4" {
		t.Errorf("bigdecimal = %+v, %v", sw, ok)
	}
	if len(got) != 2 {
		t.Errorf("got %d gems, want 2", len(got))
	}
}

// Positive: a crate is "name vX:" with its binaries indented under it.
func TestCargoCrates(t *testing.T) {
	fake := &platform.Fake{Responses: map[string]platform.Result{
		"cargo install --list": {Stdout: []byte("ripgrep v14.1.0:\n    rg\nbat v0.24.0:\n    bat\n")},
	}}
	got, _ := discoverFrom(t, "cargo", fake, nil, t.TempDir())
	if sw, ok := findSource(got, "crate:ripgrep"); !ok || sw.Version != "14.1.0" || sw.Kind != "crate" {
		t.Errorf("ripgrep = %+v, %v", sw, ok)
	}
	if len(got) != 2 {
		t.Errorf("got %d crates, want 2 (binaries are not crates)", len(got))
	}
}

// Positive: installer receipts give the version and install time; a package
// whose details can't be read is still listed, without a version.
func TestInstallerReceipts(t *testing.T) {
	fake := &platform.Fake{
		Responses: map[string]platform.Result{
			"pkgutil --pkgs":                   {Stdout: []byte("org.golang.go\norg.broken.pkg\n")},
			"pkgutil --pkg-info org.golang.go": {Stdout: []byte("package-id: org.golang.go\nversion: go1.27.1\nvolume: /\ninstall-time: 1790967419\n")},
		},
		Errors: map[string]error{"pkgutil --pkg-info org.broken.pkg": os.ErrPermission},
	}
	got, st := discoverFrom(t, "pkgutil", fake, nil, t.TempDir())
	if st.State != "ok" {
		t.Errorf("status = %+v", st)
	}
	sw, ok := findSource(got, "package:org.golang.go")
	if !ok || sw.Version != "go1.27.1" || sw.Installed.IsZero() || sw.Kind != "package" {
		t.Errorf("golang = %+v, %v", sw, ok)
	}
	if broken, ok := findSource(got, "package:org.broken.pkg"); !ok || broken.Version != "" {
		t.Errorf("broken package = %+v, %v; want it listed without a version", broken, ok)
	}
}

// Positive: App Store entries are "<id> <name> (<version>)".
func TestAppStoreApps(t *testing.T) {
	fake := &platform.Fake{Responses: map[string]platform.Result{
		"mas list": {Stdout: []byte("497799835  Xcode  (16.2)\n")},
	}}
	got, _ := discoverFrom(t, "mas", fake, nil, t.TempDir())
	if sw, ok := findSource(got, "store:497799835"); !ok || sw.Name != "Xcode" || sw.Version != "16.2" || sw.Kind != "app" {
		t.Errorf("Xcode = %+v, %v", sw, ok)
	}
}

// Positive and precondition: Docker images are listed; the dangling "<none>"
// image is not. A stopped daemon is unknown, not empty.
func TestDockerImages(t *testing.T) {
	fake := &platform.Fake{Responses: map[string]platform.Result{
		"docker images --format {{.Repository}}:{{.Tag}}": {Stdout: []byte("postgres:16\n<none>:<none>\n")},
	}}
	got, _ := discoverFrom(t, "docker", fake, nil, t.TempDir())
	if len(got) != 1 || got[0].Name != "postgres:16" || got[0].Kind != "image" {
		t.Errorf("images = %+v", got)
	}
	fake = &platform.Fake{Errors: map[string]error{"docker images --format {{.Repository}}:{{.Tag}}": os.ErrDeadlineExceeded}}
	if _, st := discoverFrom(t, "docker", fake, nil, t.TempDir()); st.State != "unknown" {
		t.Errorf("daemon down: status = %+v, want unknown", st)
	}
}

// Positive: Ollama's first line is a header; each model is a row.
func TestOllamaModels(t *testing.T) {
	fake := &platform.Fake{Responses: map[string]platform.Result{
		"ollama list": {Stdout: []byte("NAME            ID              SIZE      MODIFIED\nllama3:8b       abc123          4.7 GB    2 days ago\n")},
	}}
	got, _ := discoverFrom(t, "ollama", fake, nil, t.TempDir())
	if len(got) != 1 || got[0].Name != "llama3:8b" || got[0].Kind != "model" {
		t.Errorf("models = %+v", got)
	}
}

// Positive: runtimes are read from the folders version managers use, and an
// absent folder means nothing installed, not an error.
func TestRuntimeFolders(t *testing.T) {
	home := t.TempDir()
	for _, d := range []string{".nvm/versions/node/v20.1.0", ".rustup/toolchains/stable-aarch64-apple-darwin"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	_, st := discoverFrom(t, "runtimes", &platform.Fake{}, nil, home)
	if st.State != "ok" {
		t.Errorf("runtimes status = %+v", st)
	}
	got, _ := discoverFrom(t, "nvm", &platform.Fake{}, nil, home)
	if sw, ok := findSource(got, "runtime:nvm:v20.1.0"); !ok || sw.Name != "node v20.1.0" || sw.Version != "20.1.0" {
		t.Errorf("node = %+v, %v", sw, ok)
	}
	rust, _ := discoverFrom(t, "rustup", &platform.Fake{}, nil, home)
	if len(rust) != 1 || rust[0].Name != "rust stable-aarch64-apple-darwin" {
		t.Errorf("rustup = %+v", rust)
	}
	none, _ := discoverFrom(t, "pyenv", &platform.Fake{}, nil, t.TempDir())
	if len(none) != 0 {
		t.Errorf("pyenv with no folder = %+v, want nothing", none)
	}
}

// Every new source reports absent when its tool is missing, never unknown.
func TestNewSourcesAbsentWhenToolMissing(t *testing.T) {
	none := func(string) bool { return false }
	for _, src := range []string{"pipx", "uv-tool", "gem", "cargo", "pkgutil", "mas", "docker", "ollama"} {
		_, st := discoverFrom(t, src, &platform.Fake{}, none, t.TempDir())
		if st.State != "absent" {
			t.Errorf("%s with no tool: status = %+v, want absent", src, st)
		}
	}
}

// Every kind belongs to exactly one category, and an unknown kind goes to Other.
func TestCategories(t *testing.T) {
	for kind, want := range map[string]string{
		"app": "apps and system packages", "package": "apps and system packages", "formula": "apps and system packages",
		"npm": "language packages", "pip": "language packages", "gem": "language packages", "crate": "language packages",
		"runtime": "runtimes", "tool": "programs on PATH", "image": "images and models", "model": "images and models",
		"mystery": "Other",
	} {
		if got := inventory.Category(kind); got != want {
			t.Errorf("Category(%q) = %q, want %q", kind, got, want)
		}
	}
}
