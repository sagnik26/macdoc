package recipes

import (
	"path/filepath"

	"github.com/sagnik26/macdoc/internal/pathfold"
)

// KeepFunc returns a scanner Keep callback built from the catalog: it keeps
// every direct child of each recipe's KeepChildrenOf directories and every
// directory whose base name is in a recipe's KeepNames. Matching is
// case-insensitive. It is precomputed into sets, so each call is O(1)
// (the scanner calls it once per directory under a lock).
func KeepFunc(c *Catalog, home string) func(path string) bool {
	parents := map[string]bool{}
	names := map[string]bool{}
	for _, r := range c.Recipes {
		for _, d := range r.KeepChildrenOf {
			parents[pathfold.Fold(filepath.Clean(ExpandHome(home, d)))] = true
		}
		for _, n := range r.KeepNames {
			names[pathfold.Fold(n)] = true
		}
	}
	if len(parents) == 0 && len(names) == 0 {
		return nil
	}
	return func(path string) bool {
		lp := pathfold.Fold(path)
		return parents[filepath.Dir(lp)] || parents[lp] || names[filepath.Base(lp)]
	}
}
