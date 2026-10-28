package projects

import (
	"context"
	"html"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"macdoc/internal/fsread"
	"macdoc/internal/platform"
)

// walkSkip are folders that never count as source activity: artifacts,
// dependency trees, caches and git's own data.
var walkSkip = map[string]bool{
	".git": true, "node_modules": true, "Pods": true, "build": true, ".gradle": true,
	".venv": true, "venv": true, ".next": true, ".expo": true, "dist": true, "DerivedData": true,
	".cache": true, ".turbo": true, "coverage": true, "__pycache__": true, ".pytest_cache": true,
	".mypy_cache": true, ".ruff_cache": true, ".tox": true, "target": true,
}

// activity sets LastActive, ActivitySource and IdleDays from the newest of:
// the last commit touching the project, the newest source file or folder
// (artifacts excluded) and the newest artifact (a build or install is use
// too). Access times are never used. If the source walk is cut short, the
// time is only trusted when git says the tree is clean.
func activity(p *Project, now, commit time.Time, walkLimit int) {
	src, complete := newestSource(p.Root, walkLimit)
	if !complete && (p.Git != GitClean || commit.IsZero()) {
		p.IdleDays = -1
		return
	}
	best, source := time.Time{}, ""
	consider := func(t time.Time, s string) {
		if t.After(best) {
			best, source = t, s
		}
	}
	consider(commit, ActivityCommit)
	if src > 0 {
		consider(time.Unix(src, 0).UTC(), ActivityFiles)
	}
	for _, a := range p.Artifacts {
		if a.MtimeMax > 0 {
			consider(time.Unix(a.MtimeMax, 0).UTC(), ActivityBuild)
		}
	}
	if best.IsZero() {
		p.IdleDays = -1
		return
	}
	p.LastActive, p.ActivitySource = best, source
	p.IdleDays = max(0, int(now.Sub(best).Hours()/24))
}

// newestSource returns the newest mtime (unix seconds) of the files and
// folders below root, skipping walkSkip folders and never following
// symlinks. complete is false if the walk stopped at limit entries or hit
// an unreadable folder.
func newestSource(root string, limit int) (newest int64, complete bool) {
	type dir struct {
		path  string
		depth int
	}
	complete = true
	stack := []dir{{root, 0}}
	seen := 0
	for len(stack) > 0 {
		d := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		entries, err := os.ReadDir(d.path)
		if err != nil {
			complete = false
			continue
		}
		for _, e := range entries {
			if seen++; seen > limit {
				return newest, false
			}
			fi, err := e.Info() // Lstat semantics: symlinks aren't followed
			if err != nil {
				continue
			}
			newest = max(newest, fi.ModTime().Unix())
			if e.IsDir() && !walkSkip[e.Name()] && d.depth < walkMaxDepth {
				stack = append(stack, dir{filepath.Join(d.path, e.Name()), d.depth + 1})
			}
		}
	}
	return newest, complete
}

// gitState asks git, read-only, whether the project has uncommitted changes
// and when it was last committed to. The commands never take git's optional
// locks (so the index isn't rewritten), disable fsmonitor hooks (a repo's
// config could otherwise start a program) and are limited to the project's
// own folder with the "." pathspec.
func gitState(ctx context.Context, env Env, root string) (GitState, time.Time, string) {
	if !inRepo(env.Home, root) {
		return GitNone, time.Time{}, ""
	}
	if env.Runner == nil {
		return GitUnknown, time.Time{}, "git not consulted"
	}
	base := []string{"-C", root, "--no-pager", "--no-optional-locks",
		"-c", "core.fsmonitor=false", "-c", "core.untrackedCache=false"}
	st, err := env.Runner.Run(ctx, platform.Command{Name: "git", Timeout: gitTimeout,
		Args: append(append([]string{}, base...), "status", "--porcelain", "--untracked-files=normal", "--", ".")})
	if err != nil {
		return GitUnknown, time.Time{}, "git couldn't run: " + err.Error()
	}
	if st.ExitCode != 0 {
		return GitUnknown, time.Time{}, "git status failed: " + firstLine(st.Stderr)
	}
	state := GitClean
	if changed(st.Stdout) {
		state = GitDirty
	}
	var commit time.Time
	lg, err := env.Runner.Run(ctx, platform.Command{Name: "git", Timeout: gitTimeout,
		Args: append(append([]string{}, base...), "log", "-1", "--format=%ct", "--", ".")})
	if err == nil && lg.ExitCode == 0 {
		if sec, err := strconv.ParseInt(strings.TrimSpace(string(lg.Stdout)), 10, 64); err == nil && sec > 0 {
			commit = time.Unix(sec, 0).UTC()
		}
	}
	return state, commit, ""
}

// changed reports whether `git status --porcelain` output lists a change
// other than Finder's untracked .DS_Store files.
func changed(porcelain []byte) bool {
	for _, line := range strings.Split(string(porcelain), "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if strings.HasPrefix(line, "?? ") && filepath.Base(strings.Trim(line[3:], `"`)) == ".DS_Store" {
			continue
		}
		return true
	}
	return false
}

// inRepo reports whether root or one of its parents up to (not including)
// home has a .git folder or file (a worktree or submodule).
func inRepo(home, root string) bool {
	home = filepath.Clean(home)
	for d := root; d != home && d != "/" && d != "."; d = filepath.Dir(d) {
		if _, err := os.Lstat(filepath.Join(d, ".git")); err == nil {
			return true
		}
	}
	return false
}

func firstLine(b []byte) string {
	s, _, _ := strings.Cut(strings.TrimSpace(string(b)), "\n")
	return s
}

// workspacePathRe finds WorkspacePath in an XML info.plist.
var workspacePathRe = regexp.MustCompile(`<key>WorkspacePath</key>\s*<string>([^<]*)</string>`)

// derivedData lists the kept DerivedData folders and maps each to the
// workspace it was built from (info.plist WorkspacePath, XML form). A folder
// whose plist is missing or binary keeps an empty Workspace.
func derivedData(env Env) ([]DerivedData, error) {
	dir := DerivedDataDir(env.Home)
	nodes, err := env.Nodes.Nodes(dir, 1)
	if err != nil {
		return nil, err
	}
	var out []DerivedData
	for _, n := range nodes {
		if n.Path == dir || filepath.Dir(n.Path) != dir || strings.EqualFold(filepath.Base(n.Path), "ModuleCache.noindex") {
			continue
		}
		d := DerivedData{Path: n.Path, Bytes: n.Bytes, MtimeMax: n.MtimeMax}
		if b, err := fsread.SmallFile(filepath.Join(n.Path, "info.plist"), fsread.MaxSmallFile); err == nil {
			if m := workspacePathRe.FindSubmatch(b); m != nil {
				d.Workspace = html.UnescapeString(strings.TrimSpace(string(m[1])))
			}
		}
		if d.Workspace != "" && filepath.IsAbs(d.Workspace) {
			if _, err := os.Lstat(d.Workspace); err != nil {
				d.Missing = true
			} else {
				d.Project = workspaceRoot(d.Workspace)
			}
		}
		out = append(out, d)
	}
	return out, nil
}

// workspaceRoot is the project root for an Xcode workspace or project: its
// folder, or the folder above an "ios" folder (React Native).
func workspaceRoot(ws string) string {
	dir := filepath.Dir(filepath.Clean(ws))
	if filepath.Base(dir) == "ios" {
		return filepath.Dir(dir)
	}
	return dir
}

// GitStatus reports whether dir has uncommitted changes, with the same
// read-only git invocation discovery uses. It's for apply's git_clean
// precondition; outside a repo it returns GitNone.
func GitStatus(ctx context.Context, env Env, dir string) (GitState, string) {
	st, _, note := gitState(ctx, env, dir)
	return st, note
}
