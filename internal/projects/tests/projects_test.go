package tests

import (
	"context"
	"github.com/sagnik26/macdoc/internal/projects"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/platform"
	"github.com/sagnik26/macdoc/internal/testutil/fstree"
)

var now = time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)

func daysAgo(d int) time.Time { return now.Add(-time.Duration(d) * 24 * time.Hour) }

// fakeNodes serves kept scan nodes like the store does.
type fakeNodes []model.Node

func (f fakeNodes) Nodes(prefix string, maxDepth int) ([]model.Node, error) {
	var out []model.Node
	pd := strings.Count(prefix, "/")
	for _, n := range f {
		if (n.Path == prefix || strings.HasPrefix(n.Path, prefix+"/")) &&
			(maxDepth == 0 || strings.Count(n.Path, "/")-pd <= maxDepth) {
			out = append(out, n)
		}
	}
	return out, nil
}

// age sets the mtime of everything below root (directories included).
func age(t *testing.T, root string, when time.Time) {
	t.Helper()
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.Type()&fs.ModeSymlink != 0 {
			return err
		}
		return os.Chtimes(p, when, when) //nolint:gosec // G122: test fixture in a temp dir
	})
	if err != nil {
		t.Fatal(err)
	}
}

// node is a kept artifact folder with size and newest mtime.
func node(p string, mb int64, mtime time.Time) model.Node {
	return model.Node{Path: p, Kind: model.NodeDir, Bytes: mb * 1_000_000, MtimeMax: mtime.Unix()}
}

// gitRunner answers the two read-only git commands for root.
func gitRunner(root, status string, commit time.Time) *platform.FakeRunner {
	base := "git -C " + root + " --no-pager --no-optional-locks -c core.fsmonitor=false -c core.untrackedCache=false "
	return &platform.FakeRunner{Responses: map[string]platform.Result{
		base + "status --porcelain --untracked-files=normal -- .": {Stdout: []byte(status)},
		base + "log -1 --format=%ct -- .":                         {Stdout: []byte(strconv.FormatInt(commit.Unix(), 10) + "\n")},
	}}
}

// rnHome builds ~/code/app, a React Native project with every artifact kind,
// idle since 120 days (files) and committed to 150 days ago.
func rnHome(t *testing.T) (string, fakeNodes) {
	t.Helper()
	home := t.TempDir()
	app := filepath.Join(home, "code/app")
	fstree.Build(t, app,
		fstree.File("package.json", 10), fstree.File("package-lock.json", 10), fstree.File("src/index.js", 10),
		fstree.File("ios/Podfile", 10), fstree.Dir("ios/App.xcodeproj"), fstree.Dir("ios/Pods/Alamofire"), fstree.Dir("ios/build"),
		fstree.File("android/build.gradle", 10), fstree.Dir("android/build"), fstree.Dir("android/app/build"), fstree.Dir("android/.gradle"),
		fstree.Dir("node_modules/react/build"), fstree.Dir(".next"), fstree.Dir(".git"),
	)
	age(t, app, daysAgo(120))
	old := daysAgo(200)
	return home, fakeNodes{
		node(filepath.Join(app, "node_modules"), 500, old),
		node(filepath.Join(app, "node_modules/react/build"), 5, old), // inside a dependency
		node(filepath.Join(app, "ios/Pods"), 300, old),
		node(filepath.Join(app, "ios/build"), 200, old),
		node(filepath.Join(app, "android/build"), 50, old),
		node(filepath.Join(app, "android/app/build"), 400, old),
		node(filepath.Join(app, "android/.gradle"), 20, old),
		node(filepath.Join(app, ".next"), 100, old),
	}
}

func discover(t *testing.T, env projects.Env) projects.Result {
	t.Helper()
	if env.Now.IsZero() {
		env.Now = now
	}
	res, err := projects.Discover(context.Background(), env, projects.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestDiscoverReactNativeProject(t *testing.T) {
	home, nodes := rnHome(t)
	app := filepath.Join(home, "code/app")
	r := gitRunner(app, "", daysAgo(150))
	res := discover(t, projects.Env{Home: home, Nodes: nodes, Runner: r})
	if len(res.Projects) != 1 {
		t.Fatalf("projects = %+v", res.Projects)
	}
	p := res.Projects[0]
	if p.Root != app || p.Name != "app" || p.Protected {
		t.Errorf("project = %+v", p)
	}
	if want := []string{"react-native", "ios", "android"}; !slices.Equal(p.Kinds, want) {
		t.Errorf("kinds = %v, want %v", p.Kinds, want)
	}
	var rels []string
	for _, a := range p.Artifacts {
		rels = append(rels, a.Rel)
	}
	want := []string{"node_modules", "android/app/build", "ios/Pods", "ios/build", ".next", "android/build", "android/.gradle"}
	if !slices.Equal(rels, want) {
		t.Errorf("artifacts = %v, want %v (biggest first, dependency builds excluded)", rels, want)
	}
	if p.ArtifactBytes != 1570_000_000 {
		t.Errorf("artifact bytes = %d", p.ArtifactBytes)
	}
	if p.Git != projects.GitClean || p.IdleDays != 120 || p.ActivitySource != projects.ActivityFiles {
		t.Errorf("git %s idle %d (%s), want clean, 120 days from files", p.Git, p.IdleDays, p.ActivitySource)
	}
	// git runs read-only: no optional locks, no fsmonitor hook.
	for _, c := range r.Calls {
		args := strings.Join(c.Args, " ")
		if c.Name != "git" || !strings.Contains(args, "--no-optional-locks") || !strings.Contains(args, "core.fsmonitor=false") {
			t.Errorf("git call %q", args)
		}
	}
	if len(r.Calls) != 2 {
		t.Errorf("git calls = %d, want 2", len(r.Calls))
	}
}

func TestActivityUsesNewestSignal(t *testing.T) {
	home, nodes := rnHome(t)
	app := filepath.Join(home, "code/app")
	// A commit 10 days ago is newer than any file.
	p := discover(t, projects.Env{Home: home, Nodes: nodes, Runner: gitRunner(app, "", daysAgo(10))}).Projects[0]
	if p.IdleDays != 10 || p.ActivitySource != projects.ActivityCommit {
		t.Errorf("idle %d (%s), want 10 from the commit", p.IdleDays, p.ActivitySource)
	}
	// A build 3 days ago counts as use.
	nodes[2].MtimeMax = daysAgo(3).Unix()
	p = discover(t, projects.Env{Home: home, Nodes: nodes, Runner: gitRunner(app, "", daysAgo(150))}).Projects[0]
	if p.IdleDays != 3 || p.ActivitySource != projects.ActivityBuild {
		t.Errorf("idle %d (%s), want 3 from a build", p.IdleDays, p.ActivitySource)
	}
}

func TestGitStates(t *testing.T) {
	home, nodes := rnHome(t)
	app := filepath.Join(home, "code/app")
	cases := []struct {
		name   string
		runner platform.Runner
		want   projects.GitState
	}{
		{"modified file", gitRunner(app, " M src/index.js\n", daysAgo(150)), projects.GitDirty},
		{"untracked file", gitRunner(app, "?? notes.txt\n", daysAgo(150)), projects.GitDirty},
		{"only Finder files", gitRunner(app, "?? .DS_Store\n?? ios/.DS_Store\n", daysAgo(150)), projects.GitClean},
		{"git fails", &platform.FakeRunner{Errors: map[string]error{}}, projects.GitUnknown},
		{"no runner", nil, projects.GitUnknown},
	}
	for _, c := range cases {
		p := discover(t, projects.Env{Home: home, Nodes: nodes, Runner: c.runner}).Projects[0]
		if p.Git != c.want {
			t.Errorf("%s: git = %s (%s), want %s", c.name, p.Git, p.GitNote, c.want)
		}
	}

	if err := os.Remove(filepath.Join(app, ".git")); err != nil {
		t.Fatal(err)
	}
	r := &platform.FakeRunner{}
	if p := discover(t, projects.Env{Home: home, Nodes: nodes, Runner: r}).Projects[0]; p.Git != projects.GitNone || len(r.Calls) != 0 {
		t.Errorf("without .git: git = %s, %d calls", p.Git, len(r.Calls))
	}
}

func TestGitSkippedForSmallProjects(t *testing.T) {
	home, nodes := rnHome(t)
	r := &platform.FakeRunner{}
	res, err := projects.Discover(context.Background(), projects.Env{Home: home, Nodes: nodes[:1], Runner: r, Now: now}, projects.Options{GitMinBytes: 1 << 40})
	if err != nil {
		t.Fatal(err)
	}
	if p := res.Projects[0]; p.Git != projects.GitUnknown || !strings.Contains(p.GitNote, "not checked") || len(r.Calls) != 0 {
		t.Errorf("git = %s (%s), %d calls", p.Git, p.GitNote, len(r.Calls))
	}
}

func TestWalkLimitMakesIdleUnknown(t *testing.T) {
	home, nodes := rnHome(t)
	app := filepath.Join(home, "code/app")
	env := projects.Env{Home: home, Nodes: nodes, Now: now}
	// Not confirmed clean: a cut-short walk can't prove the project idle.
	res, _ := projects.Discover(context.Background(), env, projects.Options{WalkLimit: 3})
	if p := res.Projects[0]; p.IdleDays != -1 || !p.LastActive.IsZero() {
		t.Errorf("idle = %d, want unknown", p.IdleDays)
	}
	// Clean in git: the commit time is trusted.
	env.Runner = gitRunner(app, "", daysAgo(150))
	res, _ = projects.Discover(context.Background(), env, projects.Options{WalkLimit: 3})
	if p := res.Projects[0]; p.IdleDays < 0 {
		t.Errorf("clean project idle = %d, want known", p.IdleDays)
	}
}

// Look-alikes: folders with artifact names that don't belong to a project.
func TestLookAlikesAreNotProjects(t *testing.T) {
	home := t.TempDir()
	fstree.Build(t, home,
		fstree.Dir("code/notes/build"),                                               // no gradle or Xcode files
		fstree.Dir("code/ios-only/ios/build"),                                        // "ios" without a Podfile or Xcode project
		fstree.Dir("code/lib/node_modules"), fstree.File("code/lib/package.json", 1), // a global-style lib/node_modules
		fstree.Dir("code/nopkg/node_modules"),                                 // no package.json
		fstree.Dir("code/pods/Pods"),                                          // no Podfile
		fstree.Dir("code/py/.venv"), fstree.File("code/py/pyproject.toml", 1), // no pyvenv.cfg
		fstree.File("code/real.json", 1), fstree.Symlink("code/link/package.json", "../real.json"),
		fstree.Dir("code/link/node_modules"), // symlinked package.json doesn't count
		fstree.Dir(".cache/x/node_modules"), fstree.File(".cache/x/package.json", 1),
		fstree.Dir("Library/y/node_modules"), fstree.File("Library/y/package.json", 1),
	)
	var nodes fakeNodes
	for _, p := range []string{
		"code/notes/build", "code/ios-only/ios/build", "code/lib/node_modules", "code/nopkg/node_modules",
		"code/pods/Pods", "code/py/.venv", "code/link/node_modules", ".cache/x/node_modules", "Library/y/node_modules",
	} {
		nodes = append(nodes, node(filepath.Join(home, p), 100, daysAgo(200)))
	}
	if res := discover(t, projects.Env{Home: home, Nodes: nodes}); len(res.Projects) != 0 {
		t.Errorf("projects = %+v", res.Projects)
	}
}

func TestProtectedProjectsAreMarked(t *testing.T) {
	home := t.TempDir()
	fstree.Build(t, home, fstree.File("Desktop/site/package.json", 1), fstree.Dir("Desktop/site/node_modules"))
	nodes := fakeNodes{node(filepath.Join(home, "Desktop/site/node_modules"), 100, daysAgo(200))}
	res := discover(t, projects.Env{Home: home, Nodes: nodes})
	if len(res.Projects) != 1 || !res.Projects[0].Protected {
		t.Errorf("projects = %+v, want one protected", res.Projects)
	}
}

func TestDerivedDataMapping(t *testing.T) {
	home, nodes := rnHome(t)
	dd := projects.DerivedDataDir(home)
	plist := func(ws string) string {
		return `<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict>` +
			"<key>LastAccessedDate</key><date>2026-01-01T00:00:00Z</date>\n\t<key>WorkspacePath</key>\n\t<string>" + ws + "</string></dict></plist>"
	}
	ws := filepath.Join(home, "code/app/ios/App.xcworkspace")
	fstree.Build(t, home, fstree.Dir("code/app/ios/App.xcworkspace"))
	writes := map[string]string{
		"App-abc/info.plist":  plist(ws),
		"Gone-def/info.plist": plist(filepath.Join(home, "old/Gone &amp; Done.xcodeproj")),
		"Bin-ghi/info.plist":  "bplist00\x00\x01",
	}
	for p, c := range writes {
		fstree.Build(t, dd, fstree.Dir(filepath.Dir(p)))
		if err := os.WriteFile(filepath.Join(dd, p), []byte(c), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	nodes = append(nodes,
		node(dd, 3000, daysAgo(40)),
		node(filepath.Join(dd, "App-abc"), 1000, daysAgo(40)),
		node(filepath.Join(dd, "Gone-def"), 1500, daysAgo(40)),
		node(filepath.Join(dd, "Bin-ghi"), 500, daysAgo(40)),
	)
	res := discover(t, projects.Env{Home: home, Nodes: nodes})
	byName := map[string]projects.DerivedData{}
	for _, d := range res.DerivedData {
		byName[filepath.Base(d.Path)] = d
	}
	if d := byName["App-abc"]; d.Workspace != ws || d.Project != filepath.Join(home, "code/app") || d.Missing {
		t.Errorf("App = %+v", d)
	}
	if d := byName["Gone-def"]; !d.Missing || d.Project != "" || !strings.HasSuffix(d.Workspace, "Gone & Done.xcodeproj") {
		t.Errorf("Gone = %+v", d)
	}
	if d := byName["Bin-ghi"]; d.Workspace != "" || d.Missing {
		t.Errorf("binary plist = %+v", d)
	}
	p, a, ok := res.Owner(filepath.Join(dd, "App-abc"))
	if !ok || p.Root != filepath.Join(home, "code/app") || a.Rel != "DerivedData" {
		t.Errorf("owner = %v %+v %+v", ok, p, a)
	}
}

func TestKeep(t *testing.T) {
	home := "/Users/u"
	keep := projects.Keep(home)
	for p, want := range map[string]bool{
		"/Users/u/code/app/node_modules":              true,
		"/Users/u/code/app/ios/Pods":                  true,
		"/Users/u/code/app/android/.gradle":           true,
		"/Users/u/app/node_modules":                   true,
		"/Users/u/node_modules":                       false, // top level of home
		"/Users/u/code/app/node_modules/x/build":      false, // inside a dependency
		"/Users/u/.cursor/x/node_modules":             false,
		"/Users/u/Library/Caches/x/build":             false,
		"/opt/homebrew/lib/node_modules":              false,
		"/Users/u/code/app/src":                       false,
		"/Users/u/code/app/ios/Pods/Firebase/build":   false,
		"/Users/u/code/app/.venv/lib/python3/x/build": false,
	} {
		if got := keep(p); got != want {
			t.Errorf("keep(%s) = %v, want %v", p, got, want)
		}
	}
}

func TestNoScan(t *testing.T) {
	res, err := projects.Discover(context.Background(), projects.Env{Home: t.TempDir()}, projects.Options{})
	if err != nil || len(res.Projects) != 0 || res.Projects == nil {
		t.Errorf("res = %+v, err = %v", res, err)
	}
}

func TestChanged(t *testing.T) {
	for in, want := range map[string]bool{
		"":                         false,
		"?? .DS_Store\n":           false,
		"?? \"a b/.DS_Store\"\n":   false,
		" M .DS_Store\n":           true, // a tracked .DS_Store was modified
		"?? x.txt\n":               true,
		"D  gone.go\n?? .DS_Store": true,
	} {
		if got := projects.Changed([]byte(in)); got != want {
			t.Errorf("changed(%q) = %v, want %v", in, got, want)
		}
	}
}
