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
	Register("node_modules_stale", detectNodeModulesStale)
	Register("nvm_old_versions", detectNvmOldVersions)
}

// detectNodeModulesStale emits top-level node_modules directories (no
// node_modules ancestor) whose newest mtime is older than stale_days. It
// never runs npm, pnpm or yarn.
func detectNodeModulesStale(_ context.Context, env Env, r Recipe) ([]Finding, error) {
	if env.Nodes == nil {
		return nil, nil
	}
	nodes, err := projectDirs(env, "node_modules")
	if err != nil {
		return nil, err
	}
	var out []Finding
	for _, n := range nodes {
		if n.Bytes <= 0 || !stale(env, n.MtimeMax, r.StaleDays) {
			continue
		}
		f := nodeFinding(n)
		f.Title = "node_modules in " + filepath.Base(filepath.Dir(n.Path))
		out = append(out, f)
	}
	return out, nil
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

// detectNvmOldVersions emits installed nvm Node versions (kept children of
// params["dir"]) except the newest and the one ~/.nvm/alias/default resolves
// to. If the default alias exists but can't be resolved, it emits nothing,
// because any version might be the default.
func detectNvmOldVersions(_ context.Context, env Env, r Recipe) ([]Finding, error) {
	dir, err := requireParam(r, "dir")
	if err != nil {
		return nil, err
	}
	aliasDir, err := requireParam(r, "alias")
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
		f.Note = "Default is " + orNone(def) + "; newest installed is " + newest.name + "."
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
		b, err := os.ReadFile(filepath.Join(aliasDir, alias)) //nolint:gosec // G304: read-only, fixed dir under ~/.nvm/alias, ".." rejected above.
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				if alias == "default" {
					return "", true, nil
				}
				return "", false, nil
			}
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

func orNone(s string) string {
	if s == "" {
		return "not set"
	}
	return s
}
