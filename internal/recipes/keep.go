package recipes

import (
	"path/filepath"
	"strings"

	"github.com/sagnik26/macdoc/internal/pathfold"
)

// KeepFunc returns a scanner Keep callback built from the catalog: it keeps
// every direct child of each recipe's KeepChildrenOf directories, every
// directory whose base name is in a recipe's KeepNames, and the folders the
// path and paths detectors name (and each sandboxed app's Caches folder for
// container_caches), whatever their size, so a small cache is seen. Matching is
// case-insensitive. It is precomputed into sets, so each call is O(1) apart
// from the container check (the scanner calls it once per directory under a
// lock).
func KeepFunc(c *Catalog, home string) func(path string) bool {
	parents := map[string]bool{}
	names := map[string]bool{}
	exact := map[string]bool{}
	var containerRoots []string
	for _, r := range c.Recipes {
		for _, d := range r.KeepChildrenOf {
			parents[pathfold.Fold(filepath.Clean(ExpandHome(home, d)))] = true
		}
		for _, n := range r.KeepNames {
			names[pathfold.Fold(n)] = true
		}
		switch r.Detector {
		case "path":
			addKeepPath(exact, home, r.Params["path"])
		case "paths":
			for _, p := range strings.Split(r.Params["paths"], ",") {
				addKeepPath(exact, home, p)
			}
		case "container_caches":
			if d := r.Params["dir"]; d != "" {
				containerRoots = append(containerRoots, pathfold.Fold(filepath.Clean(ExpandHome(home, d))))
			}
		}
	}
	if len(parents) == 0 && len(names) == 0 && len(exact) == 0 && len(containerRoots) == 0 {
		return nil
	}
	return func(path string) bool {
		lp := pathfold.Fold(path)
		if parents[filepath.Dir(lp)] || parents[lp] || names[filepath.Base(lp)] || exact[lp] {
			return true
		}
		for _, root := range containerRoots {
			if isContainerCaches(root, lp) {
				return true
			}
		}
		return false
	}
}

// addKeepPath adds one literal ~-relative folder (no glob characters, no "..")
// to set. Anything else is ignored: the recipe still works, it just isn't kept
// below the scan's size threshold.
func addKeepPath(set map[string]bool, home, p string) {
	p = strings.TrimSpace(p)
	if !strings.HasPrefix(p, "~/") || strings.ContainsAny(p, "*?[\\") || strings.Contains(p, "..") {
		return
	}
	set[pathfold.Fold(filepath.Clean(ExpandHome(home, p)))] = true
}

// isContainerCaches reports whether lp (folded) is <root>/<id>/data/library/caches.
func isContainerCaches(root, lp string) bool {
	rest, ok := strings.CutPrefix(lp, root+"/")
	if !ok {
		return false
	}
	parts := strings.Split(rest, "/")
	return len(parts) == 4 && parts[0] != "" && parts[1] == "data" && parts[2] == "library" && parts[3] == "caches"
}

// SplitKeepFile splits a keep_files entry such as "~/Downloads/*.dmg" into its
// folder ("~/Downloads") and its extension with the dot (".dmg"). The folder
// must be a literal ~-relative path with no glob characters, and the extension
// must be letters and digits only.
func SplitKeepFile(pat string) (dir, ext string, ok bool) {
	i := strings.LastIndex(pat, "/*.")
	if i < 0 {
		return "", "", false
	}
	dir, ext = pat[:i], pat[i+2:]
	if !strings.HasPrefix(dir, "~/") || len(dir) <= 2 || strings.ContainsAny(dir, "*?[\\") || strings.Contains(dir, "..") {
		return "", "", false
	}
	if len(ext) < 2 || len(ext) > 9 {
		return "", "", false
	}
	for _, c := range ext[1:] {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') {
			return "", "", false
		}
	}
	return dir, ext, true
}

// KeepFileFunc returns a scanner KeepFile callback built from the catalog: it
// keeps every file directly in a recipe's KeepFiles folder whose extension
// matches, whatever its size. Matching is case-insensitive. The scanner calls
// it for every file, so a name with no listed extension is rejected before
// any path work.
func KeepFileFunc(c *Catalog, home string) func(dir, name string) bool {
	byDir := map[string]map[string]bool{}
	exts := map[string]bool{}
	for _, r := range c.Recipes {
		for _, pat := range r.KeepFiles {
			dir, ext, ok := SplitKeepFile(pat)
			if !ok {
				continue
			}
			key := pathfold.Fold(filepath.Clean(ExpandHome(home, dir)))
			if byDir[key] == nil {
				byDir[key] = map[string]bool{}
			}
			ext = strings.ToLower(ext)
			byDir[key][ext] = true
			exts[ext] = true
		}
	}
	if len(byDir) == 0 {
		return nil
	}
	return func(dir, name string) bool {
		ext := strings.ToLower(filepath.Ext(name))
		if !exts[ext] {
			return false
		}
		return byDir[pathfold.Fold(dir)][ext]
	}
}
