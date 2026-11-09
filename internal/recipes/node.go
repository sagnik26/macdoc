package recipes

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func init() {
	Register("node_modules_stale", DetectNodeModulesStale)
	Register("nvm_old_versions", DetectNvmOldVersions)
}

// nodeLockfiles are the lockfiles that mark a project's node_modules.
var nodeLockfiles = []string{"package-lock.json", "npm-shrinkwrap.json", "yarn.lock", "pnpm-lock.yaml", "bun.lockb", "bun.lock"}

// DetectNodeModulesStale emits project node_modules directories that are at
// least params["min_bytes"] and idle for stale_days. A project is a parent
// with package.json and a lockfile (regular files, via os.Lstat); global and
// tool installs (…/lib/node_modules, hidden or Library trees) are skipped.
// Idle time is the newest of the folder's mtime and the manifest/lockfile
// mtimes, so a recently edited project isn't Stale. It never runs npm, pnpm
// or yarn.
func DetectNodeModulesStale(_ context.Context, env Env, r Recipe) ([]Finding, error) {
	if env.Nodes == nil {
		return nil, nil
	}
	minBytes, err := paramInt(r, "min_bytes", 50_000_000)
	if err != nil {
		return nil, err
	}
	nodes, err := ProjectDirs(env, "node_modules")
	if err != nil {
		return nil, err
	}
	var out []Finding
	for _, n := range nodes {
		parent := filepath.Dir(n.Path)
		if n.Bytes < minBytes || strings.EqualFold(filepath.Base(parent), "lib") {
			continue
		}
		mtime, ok := nodeProjectMtime(parent)
		if !ok {
			continue
		}
		mtime = max(mtime, n.MtimeMax)
		if !Stale(env, mtime, r.StaleDays) {
			continue
		}
		f := nodeFinding(n)
		f.LastUsed = unixTime(mtime)
		f.Title = "node_modules in " + filepath.Base(parent)
		if !WithProject(env, &f, parent, r.StaleDays) {
			continue
		}
		out = append(out, f)
	}
	return out, nil
}

// nodeProjectMtime reports whether dir holds package.json and a lockfile as
// regular files, and the newest of their mtimes.
func nodeProjectMtime(dir string) (int64, bool) {
	pkg, err := os.Lstat(filepath.Join(dir, "package.json"))
	if err != nil || !pkg.Mode().IsRegular() {
		return 0, false
	}
	newest, found := pkg.ModTime().Unix(), false
	for _, name := range nodeLockfiles {
		fi, err := os.Lstat(filepath.Join(dir, name))
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		found = true
		newest = max(newest, fi.ModTime().Unix())
	}
	return newest, found
}

// nodeVersion is a parsed "vMAJOR.MINOR.PATCH" directory name.
type nodeVersion struct {
	name string
	v    [3]int
}

func parseNodeVersion(name string) (nodeVersion, bool) {
	parts := strings.Split(strings.TrimPrefix(name, "v"), ".")
	if !strings.HasPrefix(name, "v") || len(parts) != 3 {
		return nodeVersion{}, false
	}
	nv := nodeVersion{name: name}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return nodeVersion{}, false
		}
		nv.v[i] = n
	}
	return nv, true
}

func (a nodeVersion) less(b nodeVersion) bool {
	for i := range a.v {
		if a.v[i] != b.v[i] {
			return a.v[i] < b.v[i]
		}
	}
	return false
}

// matches reports whether the version satisfies an nvm version prefix such
// as "24", "v24.22" or "v24.22.0".
func (a nodeVersion) matches(prefix string) bool {
	p := strings.TrimPrefix(prefix, "v")
	s := strings.TrimPrefix(a.name, "v")
	return s == p || strings.HasPrefix(s, p+".")
}

// DetectNvmOldVersions emits installed nvm Node versions (kept children of
// params["dir"]) except the newest and the one ~/.nvm/alias/default resolves
// to. If the default alias exists but can't be resolved, it emits nothing,
// because any version might be the default.
func DetectNvmOldVersions(_ context.Context, env Env, r Recipe) ([]Finding, error) {
	dir, err := RequireParam(r, "dir")
	if err != nil {
		return nil, err
	}
	aliasDir, err := RequireParam(r, "alias")
	if err != nil || env.Nodes == nil {
		return nil, err
	}
	dir = ExpandHome(env.Home, dir)
	nodes, err := env.Nodes.Nodes(dir, 1)
	if err != nil {
		return nil, err
	}
	byName := map[string]int{}
	var versions []nodeVersion
	for i, n := range nodes {
		if filepath.Dir(n.Path) != dir {
			continue
		}
		if v, ok := parseNodeVersion(filepath.Base(n.Path)); ok {
			versions = append(versions, v)
			byName[v.name] = i
		}
	}
	if len(versions) < 2 {
		return nil, nil
	}
	newest := versions[0]
	for _, v := range versions[1:] {
		if newest.less(v) {
			newest = v
		}
	}
	def, ok, err := nvmDefault(ExpandHome(env.Home, aliasDir), versions)
	if err != nil || !ok {
		return nil, err
	}
	var out []Finding
	for _, v := range versions {
		if v.name == newest.name || v.name == def {
			continue
		}
		n := nodes[byName[v.name]]
		if n.Bytes <= 0 {
			continue
		}
		f := nodeFinding(n)
		f.Title = "Node.js " + v.name + " (nvm)"
		f.Note = "Default is " + OrNone(def) + "; newest installed is " + newest.name + "."
		out = append(out, f)
	}
	return out, nil
}

// nvmDefault resolves aliasDir/default to an installed version name. ok is
// false if the alias exists but can't be resolved safely. A missing default
// alias gives ("", true, nil). Aliases may chain (default -> lts/iron ->
// v20.x); at most 5 hops are followed.
func nvmDefault(aliasDir string, versions []nodeVersion) (string, bool, error) {
	alias := "default"
	for range 5 {
		if alias == "" || strings.Contains(alias, "..") || strings.HasPrefix(alias, "/") {
			return "", false, nil
		}
		b, err := ReadSmallFile(filepath.Join(aliasDir, alias), maxSmallFile)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				if alias == "default" {
					return "", true, nil
				}
				return "", false, nil
			}
			// Includes a symlink, FIFO, directory or oversized alias file
			// (ReadSmallFile): a warning, and nothing is emitted.
			return "", false, err
		}
		val := strings.TrimSpace(string(b))
		switch val {
		case "node", "stable":
			best, found := newestMatching(versions, "")
			return best, found, nil
		}
		if v := strings.TrimPrefix(val, "v"); v != "" && v[0] >= '0' && v[0] <= '9' {
			best, found := newestMatching(versions, val)
			return best, found, nil
		}
		alias = val
	}
	return "", false, nil
}

// newestMatching returns the newest version matching prefix ("" matches all).
func newestMatching(versions []nodeVersion, prefix string) (string, bool) {
	var best *nodeVersion
	for i, v := range versions {
		if (prefix == "" || v.matches(prefix)) && (best == nil || best.less(v)) {
			best = &versions[i]
		}
	}
	if best == nil {
		return "", false
	}
	return best.name, true
}

// OrNone returns s, or "not set" when s is empty.
func OrNone(s string) string {
	if s == "" {
		return "not set"
	}
	return s
}
