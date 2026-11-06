package tests

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/platform"
	"github.com/sagnik26/macdoc/internal/precond"
)

// gitStatusKey is the exact command projects.GitStatus runs for dir.
func gitStatusKey(dir string) string {
	return "git -C " + dir + " --no-pager --no-optional-locks -c core.fsmonitor=false -c core.untrackedCache=false status --porcelain --untracked-files=normal -- ."
}

// repoHome returns a temp home holding a project folder with a .git folder.
func repoHome(t *testing.T, rel string) (home, project string) {
	t.Helper()
	home = t.TempDir()
	project = filepath.Join(home, rel)
	if err := os.MkdirAll(filepath.Join(project, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	return home, project
}

func gitCheck(t *testing.T, home string, r *platform.FakeRunner, target string) error {
	t.Helper()
	c := &precond.Checker{Runner: r, Home: home}
	return c.Check(context.Background(), model.PlanItem{Preconditions: []string{"git_clean"}, Target: target})
}

func TestGitClean(t *testing.T) {
	home, project := repoHome(t, "work/app")
	status := func(res platform.Result) *platform.FakeRunner {
		return &platform.FakeRunner{Responses: map[string]platform.Result{gitStatusKey(project): res}}
	}
	target := filepath.Join(project, "node_modules")

	t.Run("clean project passes", func(t *testing.T) {
		if err := gitCheck(t, home, status(platform.Result{}), target); err != nil {
			t.Errorf("clean = %v", err)
		}
	})
	t.Run("uncommitted changes fail", func(t *testing.T) {
		err := gitCheck(t, home, status(platform.Result{Stdout: []byte(" M main.go\n")}), target)
		if err == nil || !strings.Contains(err.Error(), "uncommitted changes") {
			t.Errorf("dirty = %v, want an uncommitted-changes error", err)
		}
	})
	t.Run("untracked files fail", func(t *testing.T) {
		if err := gitCheck(t, home, status(platform.Result{Stdout: []byte("?? new.txt\n")}), target); err == nil {
			t.Error("untracked files passed")
		}
	})
	t.Run("git failing is not clean", func(t *testing.T) {
		err := gitCheck(t, home, status(platform.Result{ExitCode: 128, Stderr: []byte("fatal: not a git repository\n")}), target)
		if err == nil || !strings.Contains(err.Error(), "couldn't check") {
			t.Errorf("git exit 128 = %v, want a could-not-check error", err)
		}
	})
	t.Run("git not runnable is not clean", func(t *testing.T) {
		r := &platform.FakeRunner{Errors: map[string]error{gitStatusKey(project): errors.New("no git")}}
		err := gitCheck(t, home, r, target)
		if err == nil || !strings.Contains(err.Error(), "couldn't check") {
			t.Errorf("git error = %v, want a could-not-check error", err)
		}
	})
	t.Run("no target", func(t *testing.T) {
		err := gitCheck(t, home, status(platform.Result{}), "")
		if err == nil || !strings.Contains(err.Error(), "no project") {
			t.Errorf("empty target = %v", err)
		}
	})
	t.Run("outside a repository is not an obstacle", func(t *testing.T) {
		plain := t.TempDir()
		if err := gitCheck(t, plain, &platform.FakeRunner{}, filepath.Join(plain, "app", "build")); err != nil {
			t.Errorf("no repo = %v", err)
		}
	})
}

// A build folder under ios/ or android/ belongs to the project above it, so
// git is asked about that project, not about the platform folder.
func TestGitCleanPlatformFolders(t *testing.T) {
	for _, platformDir := range []string{"ios", "android"} {
		home, project := repoHome(t, "rn")
		r := &platform.FakeRunner{Responses: map[string]platform.Result{gitStatusKey(project): {Stdout: []byte(" M App.tsx\n")}}}
		target := filepath.Join(project, platformDir, "build")
		if err := gitCheck(t, home, r, target); err == nil || !strings.Contains(err.Error(), "uncommitted") {
			t.Errorf("%s: dirty project above = %v", platformDir, err)
		}
		// Clean project: passing proves git was asked about the project; asking
		// about the platform folder would hit a command with no canned answer.
		r = &platform.FakeRunner{Responses: map[string]platform.Result{gitStatusKey(project): {}}}
		if err := gitCheck(t, home, r, target); err != nil {
			t.Errorf("%s: clean project above = %v", platformDir, err)
		}
	}
	// Any other parent folder is the project itself, not the one above it.
	home, project := repoHome(t, "rn")
	sub := filepath.Join(project, "sub")
	if err := os.MkdirAll(filepath.Join(sub, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	r := &platform.FakeRunner{Responses: map[string]platform.Result{
		gitStatusKey(sub):     {},
		gitStatusKey(project): {Stdout: []byte(" M x\n")},
	}}
	if err := gitCheck(t, home, r, filepath.Join(sub, "build")); err != nil {
		t.Errorf("nested repo = %v, want its own (clean) state", err)
	}
}

func TestPsFailureNamesTheExitCode(t *testing.T) {
	r := &platform.FakeRunner{Responses: map[string]platform.Result{psKey: {ExitCode: 3}}}
	c := &precond.Checker{Runner: r}
	err := c.Check(context.Background(), model.PlanItem{Preconditions: []string{"simulator_not_running"}})
	if err == nil || !strings.Contains(err.Error(), "ps exited 3") {
		t.Errorf("ps exit 3 = %v, want it to say so", err)
	}
}

func TestProcessMatchersIgnoreLookalikes(t *testing.T) {
	cases := []struct {
		pc, ps string
		fail   bool
	}{
		{"emulator_not_running", "/Users/me/Library/Android/sdk/emulator/emulator -avd Pixel\n", true},
		{"emulator_not_running", "/usr/bin/emulatorctl --status\n", false},
		{"simulator_not_running", "/usr/libexec/launchd_sim /x\n", true},
		{"simulator_not_running", "/usr/libexec/launchd_sims /x\n", false},
		{"metro_not_running", "node /p/node_modules/.bin/react-native start --port 8081\n", true},
		{"metro_not_running", "/usr/local/bin/node /p/node_modules/metro/src/cli.js\n", true},
		{"metro_not_running", "react-native start\n", false},
		{"metro_not_running", "node /p/server.js\n", false},
		{"metro_not_running", "python /p/metro/tool.py\n", false},
	}
	for _, c := range cases {
		err := check(t, c.ps, nil, model.PlanItem{Preconditions: []string{c.pc}})
		if (err != nil) != c.fail {
			t.Errorf("%s with %q = %v, want fail %v", c.pc, strings.TrimSpace(c.ps), err, c.fail)
		}
	}
}

func TestDockerReachableNeedsAVersion(t *testing.T) {
	key := "docker info --format {{.ServerVersion}}"
	item := model.PlanItem{Preconditions: []string{"docker_reachable"}}
	for name, res := range map[string]platform.Result{
		"exit 0 and no version":     {Stdout: []byte("  \n")},
		"version but exit code 1":   {ExitCode: 1, Stdout: []byte("27.3.1\n")},
		"no output and exit code 1": {ExitCode: 1},
	} {
		if err := check(t, "", map[string]platform.Result{key: res}, item); err == nil {
			t.Errorf("%s: passed", name)
		}
	}
	r := &platform.FakeRunner{Errors: map[string]error{key: errors.New("no docker")}}
	if err := (&precond.Checker{Runner: r}).Check(context.Background(), item); err == nil {
		t.Error("docker not runnable: passed")
	}
}

func TestOllamaEdges(t *testing.T) {
	hdr := "NAME    ID    SIZE    PROCESSOR    UNTIL\n"
	item := func(argv ...string) model.PlanItem {
		return model.PlanItem{Preconditions: []string{"ollama_model_not_loaded"}, Argv: argv}
	}
	ps := func(out string, code int) map[string]platform.Result {
		return map[string]platform.Result{"ollama ps": {Stdout: []byte(out), ExitCode: code}}
	}

	if err := check(t, "", nil, item()); err == nil || !strings.Contains(err.Error(), "no model named") {
		t.Errorf("no argv = %v", err)
	}
	if err := check(t, "", ps(hdr, 1), item("ollama", "rm", "m")); err == nil || !strings.Contains(err.Error(), "couldn't ask ollama") {
		t.Errorf("ollama ps exit 1 = %v", err)
	}
	r := &platform.FakeRunner{Errors: map[string]error{"ollama ps": errors.New("not installed")}}
	c := &precond.Checker{Runner: r}
	if err := c.Check(context.Background(), item("ollama", "rm", "m")); err == nil {
		t.Error("ollama not runnable: passed")
	}

	// The header row is never a model, even when it looks like one.
	if err := check(t, "", ps("m    ID    SIZE\n", 0), item("ollama", "rm", "m")); err != nil {
		t.Errorf("header treated as a model: %v", err)
	}
	// Exact name, and :latest on either side, count as loaded.
	for _, tt := range []struct{ listed, name string }{
		{"llama3.2:7b", "llama3.2:7b"},
		{"llama3.2", "llama3.2:latest"},
		{"llama3.2:latest", "llama3.2"},
	} {
		err := check(t, "", ps(hdr+tt.listed+"  a  3 GB  100% GPU  4 minutes\n", 0), item("ollama", "rm", tt.name))
		if err == nil || !strings.Contains(err.Error(), "loaded") {
			t.Errorf("listed %q, removing %q = %v, want loaded", tt.listed, tt.name, err)
		}
	}
	// A different tag of the same model is a different model.
	if err := check(t, "", ps(hdr+"llama3.2:7b  a  3 GB  100% GPU  4 minutes\n\n", 0), item("ollama", "rm", "llama3.2:70b")); err != nil {
		t.Errorf("other tag loaded = %v, want nil", err)
	}
}

func TestKnownRejectsLongUnknownNames(t *testing.T) {
	// Longer than "app_not_running:" and not starting with it.
	if precond.Known([]string{"a_precondition_name_that_is_not_known"}) {
		t.Error("long unknown name accepted")
	}
	if precond.Known([]string{"app_not_running"}) {
		t.Error("prefix without its colon accepted")
	}
	if !precond.Known(nil) || !precond.Known([]string{"docker_reachable", "app_not_running:Xcode"}) {
		t.Error("known names rejected")
	}
}
