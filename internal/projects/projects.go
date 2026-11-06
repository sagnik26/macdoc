// Package projects finds developer projects in the latest scan and works out
// how long each has been idle and whether it has uncommitted changes. It is
// read-only: it reads directory listings, small marker files and the output
// of read-only git commands, and never writes anything.
//
// A project is found through its build artifacts (node_modules, ios/Pods,
// android/build, .venv, …) that the scan kept, plus Xcode DerivedData folders
// mapped back to their workspace. Projects without artifacts aren't listed:
// they have nothing to reclaim.
package projects

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/sagnik26/macdoc/internal/fsread"
	"github.com/sagnik26/macdoc/internal/model"
	"github.com/sagnik26/macdoc/internal/platform"
	"github.com/sagnik26/macdoc/internal/safety"
)

// NodeSource is read-only access to the latest stored scan (satisfied by the
// engine's store adapter and recipes.NodeSource).
type NodeSource interface {
	Nodes(prefix string, maxDepth int) ([]model.Node, error)
}

// Env is what discovery may use.
type Env struct {
	Home   string
	Nodes  NodeSource
	Runner platform.Runner // read-only git commands; nil skips git
	Now    time.Time
}

// Options tune discovery. The zero value uses the defaults.
type Options struct {
	// GitMinBytes: git is consulted only for projects with at least this
	// many artifact bytes (default 50 MB), so a scan of many small
	// projects stays fast.
	GitMinBytes int64
	// WalkLimit caps the entries read when looking for the newest source
	// file of one project (default 20,000). Past it the source time is
	// unknown.
	WalkLimit int
}

const (
	defaultGitMinBytes = 50_000_000
	defaultWalkLimit   = 20_000
	walkMaxDepth       = 8
	gitTimeout         = 10 * time.Second
)

// GitState is what git says about a project's working tree.
type GitState string

// What is known about a project's git working tree.
const (
	GitClean   GitState = "clean"   // no uncommitted changes
	GitDirty   GitState = "dirty"   // uncommitted or untracked changes
	GitNone    GitState = "none"    // not inside a git repository
	GitUnknown GitState = "unknown" // not checked, git missing, or git failed
)

// Activity sources: what the project's last activity time came from.
const (
	ActivityCommit = "git commit"
	ActivityFiles  = "file change"
	ActivityBuild  = "build output"
)

// Artifact is one regenerable folder that belongs to a project.
type Artifact struct {
	Rel      string `json:"rel"` // e.g. "node_modules", "ios/Pods", "DerivedData"
	Path     string `json:"path"`
	Bytes    int64  `json:"bytes"`
	MtimeMax int64  `json:"mtime_max"` // newest mtime inside, unix seconds
}

// Project is one project root with its artifacts and activity.
type Project struct {
	Root           string     `json:"root"`
	Name           string     `json:"name"`
	Kinds          []string   `json:"kinds"` // node, react-native, ios, android, python
	Artifacts      []Artifact `json:"artifacts"`
	ArtifactBytes  int64      `json:"artifact_bytes"`
	LastActive     time.Time  `json:"last_active,omitzero"` // zero if unknown
	ActivitySource string     `json:"activity_source,omitempty"`
	IdleDays       int        `json:"idle_days"` // -1 if unknown
	Git            GitState   `json:"git"`
	GitNote        string     `json:"git_note,omitempty"` // why the state is unknown
	// Protected projects (e.g. in ~/Desktop or ~/Documents) are listed but
	// macdoc never suggests moving anything inside them.
	Protected bool `json:"protected"`
}

// DerivedData is one Xcode DerivedData folder and the workspace it was
// built from.
type DerivedData struct {
	Path      string `json:"path"`
	Bytes     int64  `json:"bytes"`
	MtimeMax  int64  `json:"mtime_max"`
	Workspace string `json:"workspace,omitempty"` // from info.plist WorkspacePath; "" if unreadable
	Project   string `json:"project,omitempty"`   // project root, if the workspace still exists
	Missing   bool   `json:"missing"`             // the workspace no longer exists
}

// Result is everything discovery found.
type Result struct {
	Projects    []Project     `json:"projects"`
	DerivedData []DerivedData `json:"derived_data"`
	Warnings    []string      `json:"warnings,omitempty"`
}

// ArtifactNames are the folder names the scan keeps so discovery can see
// them, whatever their size.
var ArtifactNames = []string{"node_modules", "Pods", "build", ".gradle", ".venv", ".next", ".expo"}

// skipAncestors: an artifact below one of these belongs to a dependency or
// tool, not to a project.
var skipAncestors = map[string]bool{"node_modules": true, "Pods": true, ".venv": true, ".git": true}

// Keep returns a scanner Keep callback that keeps artifact folders of
// projects below home (not in hidden or Library trees, not inside another
// artifact). It never touches the filesystem.
func Keep(home string) func(path string) bool {
	names := map[string]bool{}
	for _, n := range ArtifactNames {
		names[n] = true
	}
	prefix := strings.TrimSuffix(home, "/") + "/"
	return func(p string) bool {
		rest, ok := strings.CutPrefix(p, prefix)
		if !ok || !names[filepath.Base(p)] {
			return false
		}
		comps := strings.Split(rest, "/")
		if len(comps) < 2 || strings.HasPrefix(comps[0], ".") || comps[0] == "Library" {
			return false
		}
		for _, c := range comps[:len(comps)-1] {
			if skipAncestors[c] {
				return false
			}
		}
		return true
	}
}

// DerivedDataDir is Xcode's DerivedData folder below home.
func DerivedDataDir(home string) string {
	return filepath.Join(home, "Library/Developer/Xcode/DerivedData")
}

// Discover finds projects in the latest scan. It returns an empty result
// when there is no scan (env.Nodes nil).
func Discover(ctx context.Context, env Env, o Options) (Result, error) {
	res := Result{Projects: []Project{}, DerivedData: []DerivedData{}}
	if env.Nodes == nil {
		return res, nil
	}
	if o.GitMinBytes <= 0 {
		o.GitMinBytes = defaultGitMinBytes
	}
	if o.WalkLimit <= 0 {
		o.WalkLimit = defaultWalkLimit
	}
	if env.Now.IsZero() {
		env.Now = time.Now()
	}
	pol, err := safety.NewPolicy(env.Home, nil)
	if err != nil {
		return res, err
	}
	nodes, err := env.Nodes.Nodes(env.Home, 0)
	if err != nil {
		return res, err
	}

	byRoot := map[string]*Project{}
	add := func(root string, a Artifact) {
		p := byRoot[root]
		if p == nil {
			p = &Project{Root: root, Name: filepath.Base(root), IdleDays: -1, Git: GitUnknown}
			byRoot[root] = p
		}
		p.Artifacts = append(p.Artifacts, a)
		p.ArtifactBytes += a.Bytes
	}
	keep := Keep(env.Home)
	for _, n := range nodes {
		if n.Kind == model.NodeFile || !keep(n.Path) {
			continue
		}
		if root, rel, ok := classify(n.Path); ok && within(env.Home, root) {
			add(root, Artifact{Rel: rel, Path: n.Path, Bytes: n.Bytes, MtimeMax: n.MtimeMax})
		}
	}

	dd, err := derivedData(env)
	if err != nil {
		res.Warnings = append(res.Warnings, "DerivedData: "+err.Error())
	}
	for i, d := range dd {
		if d.Project == "" {
			continue
		}
		if !within(env.Home, d.Project) {
			dd[i].Project = ""
			continue
		}
		add(d.Project, Artifact{Rel: "DerivedData", Path: d.Path, Bytes: d.Bytes, MtimeMax: d.MtimeMax})
	}
	res.DerivedData = append(res.DerivedData, dd...)

	for _, p := range byRoot {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		p.Kinds = kinds(p.Root)
		if c, err := safety.Canonicalize(p.Root); err == nil {
			p.Protected, _ = pol.IsProtected(c)
		} else {
			p.Protected = true
		}
		sort.Slice(p.Artifacts, func(i, j int) bool { return p.Artifacts[i].Bytes > p.Artifacts[j].Bytes })
		p.Git, p.GitNote = GitUnknown, "not checked: artifacts under "+mb(o.GitMinBytes)
		var commit time.Time
		if p.ArtifactBytes >= o.GitMinBytes {
			p.Git, commit, p.GitNote = gitState(ctx, env, p.Root)
		}
		activity(p, env.Now, commit, o.WalkLimit)
		res.Projects = append(res.Projects, *p)
	}
	sort.Slice(res.Projects, func(i, j int) bool {
		a, b := res.Projects[i], res.Projects[j]
		if a.ArtifactBytes != b.ArtifactBytes {
			return a.ArtifactBytes > b.ArtifactBytes
		}
		return a.Root < b.Root
	})
	return res, nil
}

// Find returns the project whose root is root.
func (r Result) Find(root string) (Project, bool) {
	for _, p := range r.Projects {
		if p.Root == root {
			return p, true
		}
	}
	return Project{}, false
}

// Owner returns the project an artifact path belongs to.
func (r Result) Owner(artifact string) (Project, Artifact, bool) {
	for _, p := range r.Projects {
		for _, a := range p.Artifacts {
			if a.Path == artifact {
				return p, a, true
			}
		}
	}
	return Project{}, Artifact{}, false
}

// classify maps a kept artifact folder to its project root and its path
// relative to the root. Marker files are checked with Lstat, so symlinked
// markers don't count.
func classify(path string) (root, rel string, ok bool) {
	base, parent := filepath.Base(path), filepath.Dir(path)
	pbase := filepath.Base(parent)
	switch base {
	case "node_modules":
		if pbase != "lib" && fsread.IsRegular(filepath.Join(parent, "package.json")) {
			return parent, base, true
		}
	case ".next", ".expo":
		if fsread.IsRegular(filepath.Join(parent, "package.json")) {
			return parent, base, true
		}
	case ".venv":
		if fsread.IsRegular(filepath.Join(path, "pyvenv.cfg")) && isPythonProject(parent) {
			return parent, base, true
		}
	case "Pods":
		if fsread.IsRegular(filepath.Join(parent, "Podfile")) {
			if pbase == "ios" {
				return filepath.Dir(parent), "ios/Pods", true
			}
			return parent, base, true
		}
	case "build":
		switch {
		case pbase == "ios" && isAppleDir(parent):
			return filepath.Dir(parent), "ios/build", true
		case pbase == "android" && isGradleDir(parent):
			return filepath.Dir(parent), "android/build", true
		case pbase == "app" && filepath.Base(filepath.Dir(parent)) == "android" && isGradleDir(filepath.Dir(parent)):
			return filepath.Dir(filepath.Dir(parent)), "android/app/build", true
		}
	case ".gradle":
		if pbase == "android" && isGradleDir(parent) {
			return filepath.Dir(parent), "android/.gradle", true
		}
	}
	return "", "", false
}

// within reports whether p is strictly inside home and not in a hidden or
// Library tree.
func within(home, p string) bool {
	rest, ok := strings.CutPrefix(p, strings.TrimSuffix(home, "/")+"/")
	if !ok || rest == "" {
		return false
	}
	first := strings.SplitN(rest, "/", 2)[0]
	return !strings.HasPrefix(first, ".") && first != "Library"
}

var gradleFiles = []string{"build.gradle", "build.gradle.kts", "settings.gradle", "settings.gradle.kts"}

func isGradleDir(dir string) bool {
	for _, f := range gradleFiles {
		if fsread.IsRegular(filepath.Join(dir, f)) {
			return true
		}
	}
	return false
}

// isAppleDir: a Podfile, or an Xcode project or workspace.
func isAppleDir(dir string) bool {
	if fsread.IsRegular(filepath.Join(dir, "Podfile")) {
		return true
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() && (strings.HasSuffix(e.Name(), ".xcodeproj") || strings.HasSuffix(e.Name(), ".xcworkspace")) {
			return true
		}
	}
	return false
}

// pythonProjectFiles mark a directory as a Python project.
var pythonProjectFiles = map[string]bool{
	"pyproject.toml": true, "requirements.txt": true, "setup.py": true, "setup.cfg": true,
	"Pipfile": true, "poetry.lock": true, "uv.lock": true,
}

func isPythonProject(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		n := e.Name()
		if e.Type().IsRegular() && (pythonProjectFiles[n] ||
			strings.HasPrefix(n, "requirements-") && strings.HasSuffix(n, ".txt")) {
			return true
		}
	}
	return false
}

// kinds lists what a project is built with, from its marker files.
func kinds(root string) []string {
	var out []string
	node := fsread.IsRegular(filepath.Join(root, "package.json"))
	ios := isAppleDir(filepath.Join(root, "ios"))
	android := isGradleDir(filepath.Join(root, "android"))
	switch {
	case node && (ios || android):
		out = append(out, "react-native")
	case node:
		out = append(out, "node")
	}
	if ios || isAppleDir(root) {
		out = append(out, "ios")
	}
	if android || isGradleDir(root) {
		out = append(out, "android")
	}
	if isPythonProject(root) {
		out = append(out, "python")
	}
	if out == nil {
		out = []string{}
	}
	return out
}

func mb(n int64) string { return strconv.FormatInt(n/1_000_000, 10) + " MB" }
